package filesession

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	goStateDirectoryName = "go-v1"
	sessionKeyFileName   = ".session-key"
	sessionLockFileName  = ".session.lock"
)

var (
	ErrDirectoryInUse     = errors.New("file session directory already in use")
	ErrInvalidKey         = errors.New("file session key is invalid")
	ErrStorageUnavailable = errors.New("file session storage is unavailable")
	ErrClosed             = errors.New("file session manager is closed")
	ErrInvalidSession     = errors.New("file session subject and credential revision are required")
)

// Options selects a base directory for fresh-Go session state. The module only
// accesses its go-v1 child, leaving all sibling state unexamined.
type Options struct {
	BaseDirectory      string
	IdleLifetime       time.Duration
	MaxSubjectSessions int
	MaxSessions        int
	Now                func() time.Time
}

// Session is an opaque login grant. Only its token is safe to place in a
// cookie; the on-disk record holds its hash instead.
type Session struct {
	Token     string
	Subject   string
	ExpiresAt time.Time
}

// Manager owns one fresh-Go session storage directory and its process lock.
type Manager struct {
	directory    string
	key          []byte
	lock         sessionLock
	idle         time.Duration
	maxSubject   int
	maxTotal     int
	now          func() time.Time
	sessions     map[string]sessionRecord
	observers    map[string]map[uint64]func()
	nextObserver uint64
	expiration   *time.Timer

	mu     sync.Mutex
	closed bool
}

type sessionLock struct {
	path string
	id   string
}

type lockOwner struct {
	ID  string `json:"id"`
	PID int    `json:"pid"`
}

type sessionRecord struct {
	Version      int    `json:"version"`
	TokenHash    string `json:"tokenHash"`
	Subject      string `json:"subject"`
	Revision     string `json:"revision"`
	CreatedAt    string `json:"createdAt"`
	LastAccessAt string `json:"lastAccessAt"`
	ExpiresAt    string `json:"expiresAt"`

	path string
}

func Open(options Options) (*Manager, error) {
	if strings.TrimSpace(options.BaseDirectory) == "" {
		return nil, fmt.Errorf("%w: empty base directory", ErrStorageUnavailable)
	}
	if options.IdleLifetime == 0 {
		options.IdleLifetime = 7 * 24 * time.Hour
	}
	if options.IdleLifetime < 0 {
		return nil, fmt.Errorf("%w: idle lifetime must be positive", ErrStorageUnavailable)
	}
	if options.MaxSubjectSessions == 0 {
		options.MaxSubjectSessions = 8
	}
	if options.MaxSessions == 0 {
		options.MaxSessions = 128
	}
	if options.MaxSubjectSessions < 0 || options.MaxSessions < 0 {
		return nil, fmt.Errorf("%w: session limits must be positive", ErrStorageUnavailable)
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	baseDirectory, err := filepath.Abs(options.BaseDirectory)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve base directory: %v", ErrStorageUnavailable, err)
	}
	directory := filepath.Join(baseDirectory, goStateDirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("%w: create session directory: %v", ErrStorageUnavailable, err)
	}
	if err := protectSessionPath(directory, 0o700); err != nil {
		return nil, fmt.Errorf("%w: protect session directory: %v", ErrStorageUnavailable, err)
	}

	lock, err := acquireLock(directory)
	if err != nil {
		return nil, err
	}
	key, err := readOrCreateKey(directory)
	if err != nil {
		_ = releaseLock(lock)
		return nil, err
	}
	manager := &Manager{
		directory:  directory,
		key:        key,
		lock:       lock,
		idle:       options.IdleLifetime,
		maxSubject: options.MaxSubjectSessions,
		maxTotal:   options.MaxSessions,
		now:        options.Now,
		sessions:   make(map[string]sessionRecord),
		observers:  make(map[string]map[uint64]func()),
	}
	if err := manager.loadStoredSessions(); err != nil {
		_ = releaseLock(lock)
		return nil, err
	}
	return manager, nil
}

func (manager *Manager) Directory() string {
	return manager.directory
}

func (manager *Manager) CredentialRevision(value string) (string, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return "", ErrClosed
	}
	mac := hmac.New(sha256.New, manager.key)
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func (manager *Manager) Issue(subject, revision string) (*Session, error) {
	manager.mu.Lock()
	var notifications []func()
	defer func() { notify(notifications) }()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, ErrClosed
	}
	if subject == "" || revision == "" {
		return nil, ErrInvalidSession
	}
	expired, err := manager.clearExpiredLocked()
	if err != nil {
		return nil, err
	}
	notifications = append(notifications, expired...)
	evicted, err := manager.enforceLimitsLocked(subject, true)
	if err != nil {
		return nil, err
	}
	notifications = append(notifications, evicted...)

	var token string
	var hash string
	for {
		var err error
		token, err = newToken()
		if err != nil {
			return nil, fmt.Errorf("%w: generate session token: %v", ErrStorageUnavailable, err)
		}
		hash = tokenHash(token)
		if _, exists := manager.sessions[hash]; !exists {
			break
		}
	}
	now := manager.now().UTC()
	record := sessionRecord{
		Version:      1,
		TokenHash:    hash,
		Subject:      subject,
		Revision:     revision,
		CreatedAt:    formatTimestamp(now),
		LastAccessAt: formatTimestamp(now),
		ExpiresAt:    formatTimestamp(now.Add(manager.idle)),
		path:         manager.sessionPath(hash),
	}
	if err := writeRecord(record); err != nil {
		return nil, err
	}
	manager.sessions[hash] = record
	manager.scheduleExpirationLocked()
	return sessionFromRecord(token, record), nil
}

func (manager *Manager) Resume(token string, currentRevision func(string) string) (*Session, error) {
	if token == "" {
		return nil, nil
	}
	hash := tokenHash(token)

	manager.mu.Lock()
	var notifications []func()
	if manager.closed {
		manager.mu.Unlock()
		return nil, nil
	}
	expired, err := manager.clearExpiredLocked()
	if err != nil {
		manager.mu.Unlock()
		return nil, err
	}
	notifications = append(notifications, expired...)
	record, found := manager.sessions[hash]
	manager.mu.Unlock()
	notify(notifications)
	if !found {
		return nil, nil
	}

	revision := ""
	if currentRevision != nil {
		revision = currentRevision(record.Subject)
	}

	manager.mu.Lock()
	notifications = nil
	defer func() { notify(notifications) }()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil, nil
	}
	expired, err = manager.clearExpiredLocked()
	if err != nil {
		return nil, err
	}
	notifications = append(notifications, expired...)
	record, found = manager.sessions[hash]
	if !found {
		return nil, nil
	}
	if currentRevision == nil || revision != record.Revision {
		revoked, err := manager.revokeHashLocked(hash)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, revoked...)
		manager.scheduleExpirationLocked()
		return nil, nil
	}
	now := manager.now().UTC()
	record.LastAccessAt = formatTimestamp(now)
	record.ExpiresAt = formatTimestamp(now.Add(manager.idle))
	if err := writeRecord(record); err != nil {
		return nil, err
	}
	manager.sessions[hash] = record
	manager.scheduleExpirationLocked()
	return sessionFromRecord(token, record), nil
}

func (manager *Manager) Revoke(token string) error {
	manager.mu.Lock()
	var notifications []func()
	defer func() { notify(notifications) }()
	defer manager.mu.Unlock()
	if manager.closed || token == "" {
		return nil
	}
	hash := tokenHash(token)
	revoked, err := manager.revokeHashLocked(hash)
	if err != nil {
		return err
	}
	notifications = append(notifications, revoked...)
	manager.scheduleExpirationLocked()
	return nil
}

func (manager *Manager) RevokeSubject(subject string) error {
	manager.mu.Lock()
	var notifications []func()
	defer func() { notify(notifications) }()
	defer manager.mu.Unlock()
	if manager.closed || subject == "" {
		return nil
	}
	for hash, record := range manager.sessions {
		if record.Subject != subject {
			continue
		}
		revoked, err := manager.revokeHashLocked(hash)
		if err != nil {
			return err
		}
		notifications = append(notifications, revoked...)
	}
	manager.scheduleExpirationLocked()
	return nil
}

func (manager *Manager) Observe(token string, listener func()) func() {
	if token == "" || listener == nil {
		return func() {}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return func() {}
	}
	hash := tokenHash(token)
	if _, found := manager.sessions[hash]; !found {
		return func() {}
	}
	manager.nextObserver++
	id := manager.nextObserver
	listeners := manager.observers[hash]
	if listeners == nil {
		listeners = make(map[uint64]func())
		manager.observers[hash] = listeners
	}
	listeners[id] = listener
	return func() {
		manager.mu.Lock()
		defer manager.mu.Unlock()
		listeners := manager.observers[hash]
		delete(listeners, id)
		if len(listeners) == 0 {
			delete(manager.observers, hash)
		}
	}
}

func (manager *Manager) Close() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil
	}
	manager.closed = true
	if manager.expiration != nil {
		manager.expiration.Stop()
		manager.expiration = nil
	}
	clear(manager.key)
	clear(manager.sessions)
	clear(manager.observers)
	return releaseLock(manager.lock)
}

func (manager *Manager) sessionPath(hash string) string {
	return filepath.Join(manager.directory, hash+".json")
}

func (manager *Manager) loadStoredSessions() error {
	entries, err := os.ReadDir(manager.directory)
	if err != nil {
		return fmt.Errorf("%w: list records: %v", ErrStorageUnavailable, err)
	}
	now := manager.now().UTC()
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(manager.directory, name)
		if strings.Contains(name, ".tmp-") {
			if entry.Type().IsRegular() {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("%w: remove interrupted record: %v", ErrStorageUnavailable, err)
				}
			}
			continue
		}
		if !entry.Type().IsRegular() || !strings.HasSuffix(name, ".json") {
			continue
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("%w: read record: %v", ErrStorageUnavailable, err)
		}
		record, valid := parseSessionRecord(contents, path)
		if !valid {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%w: remove invalid record: %v", ErrStorageUnavailable, err)
			}
			continue
		}
		expiresAt, _ := parseTimestamp(record.ExpiresAt)
		if !expiresAt.After(now) {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("%w: remove expired record: %v", ErrStorageUnavailable, err)
			}
			continue
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("%w: protect record: %v", ErrStorageUnavailable, err)
		}
		manager.sessions[record.TokenHash] = record
	}
	if _, err := manager.enforceLimitsLocked("", false); err != nil {
		return err
	}
	manager.scheduleExpirationLocked()
	return nil
}

func (manager *Manager) clearExpiredLocked() ([]func(), error) {
	var notifications []func()
	now := manager.now().UTC()
	for hash, record := range manager.sessions {
		expiresAt, _ := parseTimestamp(record.ExpiresAt)
		if expiresAt.After(now) {
			continue
		}
		revoked, err := manager.revokeHashLocked(hash)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, revoked...)
	}
	return notifications, nil
}

func (manager *Manager) enforceLimitsLocked(subject string, reserve bool) ([]func(), error) {
	var notifications []func()
	subjects := map[string]struct{}{}
	if subject != "" {
		subjects[subject] = struct{}{}
	} else {
		for _, record := range manager.sessions {
			subjects[record.Subject] = struct{}{}
		}
	}
	perSubjectLimit := manager.maxSubject
	if reserve {
		perSubjectLimit--
	}
	for current := range subjects {
		for manager.countSubject(current) > perSubjectLimit {
			hash := manager.oldestHash(current)
			if hash == "" {
				break
			}
			revoked, err := manager.revokeHashLocked(hash)
			if err != nil {
				return nil, err
			}
			notifications = append(notifications, revoked...)
		}
	}
	totalLimit := manager.maxTotal
	if reserve {
		totalLimit--
	}
	for len(manager.sessions) > totalLimit {
		hash := manager.oldestHash("")
		if hash == "" {
			break
		}
		revoked, err := manager.revokeHashLocked(hash)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, revoked...)
	}
	return notifications, nil
}

func (manager *Manager) countSubject(subject string) int {
	count := 0
	for _, record := range manager.sessions {
		if record.Subject == subject {
			count++
		}
	}
	return count
}

func (manager *Manager) oldestHash(subject string) string {
	var oldestHash string
	var oldest time.Time
	for hash, record := range manager.sessions {
		if subject != "" && record.Subject != subject {
			continue
		}
		lastAccessAt, _ := parseTimestamp(record.LastAccessAt)
		if oldestHash == "" || lastAccessAt.Before(oldest) || (lastAccessAt.Equal(oldest) && hash < oldestHash) {
			oldestHash = hash
			oldest = lastAccessAt
		}
	}
	return oldestHash
}

func (manager *Manager) revokeHashLocked(hash string) ([]func(), error) {
	record, found := manager.sessions[hash]
	if !found {
		return nil, nil
	}
	if err := removeRecord(record); err != nil {
		return nil, err
	}
	delete(manager.sessions, hash)
	listeners := manager.observers[hash]
	delete(manager.observers, hash)
	callbacks := make([]func(), 0, len(listeners))
	for _, listener := range listeners {
		callbacks = append(callbacks, listener)
	}
	return callbacks, nil
}

func (manager *Manager) scheduleExpirationLocked() {
	if manager.expiration != nil {
		manager.expiration.Stop()
		manager.expiration = nil
	}
	if manager.closed || len(manager.sessions) == 0 {
		return
	}
	var next time.Time
	for _, record := range manager.sessions {
		expiresAt, _ := parseTimestamp(record.ExpiresAt)
		if next.IsZero() || expiresAt.Before(next) {
			next = expiresAt
		}
	}
	delay := next.Sub(manager.now().UTC())
	if delay < 0 {
		delay = 0
	}
	manager.expiration = time.AfterFunc(delay, manager.expire)
}

func (manager *Manager) expire() {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return
	}
	notifications, _ := manager.clearExpiredLocked()
	manager.scheduleExpirationLocked()
	manager.mu.Unlock()
	notify(notifications)
}

func notify(callbacks []func()) {
	for _, callback := range callbacks {
		callback()
	}
}

func acquireLock(directory string) (sessionLock, error) {
	lockPath := filepath.Join(directory, sessionLockFileName)
	ownerID, err := newUUID()
	if err != nil {
		return sessionLock{}, fmt.Errorf("%w: create lock owner ID: %v", ErrStorageUnavailable, err)
	}
	lock := sessionLock{path: lockPath, id: ownerID}
	owner := lockOwner{ID: ownerID, PID: os.Getpid()}
	for attempt := 0; attempt < 2; attempt++ {
		if err := writeLockOwner(lockPath, owner); err == nil {
			return lock, nil
		} else if !errors.Is(err, os.ErrExist) {
			return sessionLock{}, fmt.Errorf("%w: create lock: %v", ErrStorageUnavailable, err)
		}

		current, valid := readLockOwner(lockPath)
		if valid {
			alive, err := nativeProcessAlive(current.PID)
			if err != nil {
				return sessionLock{}, fmt.Errorf("%w: inspect lock owner: %v", ErrStorageUnavailable, err)
			}
			if alive {
				return sessionLock{}, fmt.Errorf("%w: %s", ErrDirectoryInUse, directory)
			}
		}
		if err := os.Remove(lockPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return sessionLock{}, fmt.Errorf("%w: remove stale lock: %v", ErrStorageUnavailable, err)
		}
	}
	return sessionLock{}, fmt.Errorf("%w: %s", ErrStorageUnavailable, directory)
}

func releaseLock(lock sessionLock) error {
	owner, valid := readLockOwner(lock.path)
	if !valid || owner.ID != lock.id {
		return nil
	}
	if err := os.Remove(lock.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: release lock: %v", ErrStorageUnavailable, err)
	}
	return nil
}

func readOrCreateKey(directory string) ([]byte, error) {
	keyPath := filepath.Join(directory, sessionKeyFileName)
	key, err := os.ReadFile(keyPath)
	if err == nil {
		if len(key) != sha256.Size {
			return nil, fmt.Errorf("%w: %s", ErrInvalidKey, keyPath)
		}
		if err := protectSessionPath(keyPath, 0o600); err != nil {
			return nil, fmt.Errorf("%w: protect key: %v", ErrStorageUnavailable, err)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: read key: %v", ErrStorageUnavailable, err)
	}

	key = make([]byte, sha256.Size)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("%w: generate key: %v", ErrStorageUnavailable, err)
	}
	file, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		if writeErr := writeAndSync(file, key); writeErr != nil {
			_ = file.Close()
			_ = os.Remove(keyPath)
			return nil, fmt.Errorf("%w: write key: %v", ErrStorageUnavailable, writeErr)
		}
		if closeErr := file.Close(); closeErr != nil {
			return nil, fmt.Errorf("%w: close key: %v", ErrStorageUnavailable, closeErr)
		}
		if err := protectSessionPath(keyPath, 0o600); err != nil {
			_ = os.Remove(keyPath)
			return nil, fmt.Errorf("%w: protect key: %v", ErrStorageUnavailable, err)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("%w: create key: %v", ErrStorageUnavailable, err)
	}
	key, err = os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("%w: read concurrent key: %v", ErrStorageUnavailable, err)
	}
	if len(key) != sha256.Size {
		return nil, fmt.Errorf("%w: %s", ErrInvalidKey, keyPath)
	}
	return key, nil
}

func writeLockOwner(lockPath string, owner lockOwner) error {
	contents, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	file, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if writeErr := writeAndSync(file, contents); writeErr != nil {
		_ = file.Close()
		_ = os.Remove(lockPath)
		return writeErr
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := protectSessionPath(lockPath, 0o600); err != nil {
		_ = os.Remove(lockPath)
		return err
	}
	return nil
}

func readLockOwner(lockPath string) (lockOwner, bool) {
	contents, err := os.ReadFile(lockPath)
	if err != nil {
		return lockOwner{}, false
	}
	var owner lockOwner
	if err := json.Unmarshal(contents, &owner); err != nil || owner.ID == "" || owner.PID <= 0 {
		return lockOwner{}, false
	}
	return owner, true
}

func writeAndSync(file *os.File, contents []byte) error {
	for len(contents) > 0 {
		written, err := file.Write(contents)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		contents = contents[written:]
	}
	return file.Sync()
}

func writeRecord(record sessionRecord) (err error) {
	candidateID, err := newUUID()
	if err != nil {
		return fmt.Errorf("%w: create record candidate ID: %v", ErrStorageUnavailable, err)
	}
	candidate := record.path + ".tmp-" + candidateID
	defer func() {
		if cleanupErr := os.Remove(candidate); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) && err == nil {
			err = fmt.Errorf("%w: clean up record candidate: %v", ErrStorageUnavailable, cleanupErr)
		}
	}()
	contents, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("%w: encode record: %v", ErrStorageUnavailable, err)
	}
	contents = append(contents, '\n')
	file, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("%w: create record candidate: %v", ErrStorageUnavailable, err)
	}
	if writeErr := writeAndSync(file, contents); writeErr != nil {
		_ = file.Close()
		return fmt.Errorf("%w: write record candidate: %v", ErrStorageUnavailable, writeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		return fmt.Errorf("%w: close record candidate: %v", ErrStorageUnavailable, closeErr)
	}
	if err := protectSessionPath(candidate, 0o600); err != nil {
		return fmt.Errorf("%w: protect record candidate: %v", ErrStorageUnavailable, err)
	}
	if err := replaceSessionFile(candidate, record.path); err != nil {
		return fmt.Errorf("%w: replace record: %v", ErrStorageUnavailable, err)
	}
	return nil
}

func removeRecord(record sessionRecord) error {
	if err := os.Remove(record.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: revoke record: %v", ErrStorageUnavailable, err)
	}
	return nil
}

func newToken() (string, error) {
	bytes := make([]byte, sha256.Size)
	if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func parseSessionRecord(contents []byte, path string) (sessionRecord, bool) {
	var record sessionRecord
	if json.Unmarshal(contents, &record) != nil || record.Version != 1 || !validTokenHash(record.TokenHash) || record.Subject == "" || record.Revision == "" {
		return sessionRecord{}, false
	}
	if filepath.Base(path) != record.TokenHash+".json" {
		return sessionRecord{}, false
	}
	if _, ok := parseTimestamp(record.CreatedAt); !ok {
		return sessionRecord{}, false
	}
	if _, ok := parseTimestamp(record.LastAccessAt); !ok {
		return sessionRecord{}, false
	}
	if _, ok := parseTimestamp(record.ExpiresAt); !ok {
		return sessionRecord{}, false
	}
	record.path = path
	return record, true
}

func validTokenHash(value string) bool {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func formatTimestamp(value time.Time) string {
	return value.UTC().Truncate(time.Millisecond).Format("2006-01-02T15:04:05.000Z")
}

func parseTimestamp(value string) (time.Time, bool) {
	parsed, err := time.Parse("2006-01-02T15:04:05.000Z", value)
	return parsed, err == nil
}

func sessionFromRecord(token string, record sessionRecord) *Session {
	expiresAt, valid := parseTimestamp(record.ExpiresAt)
	if !valid {
		return nil
	}
	return &Session{Token: token, Subject: record.Subject, ExpiresAt: expiresAt}
}

func newUUID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, bytes); err != nil {
		return "", err
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	encoded := hex.EncodeToString(bytes)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
