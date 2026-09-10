package fs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

const (
	maxDownloadURLLength  = 8192
	maxDownloadPathLength = 4096
	maxSafeDownloadBytes  = int64(1<<53 - 1)
	maxDownloadTasks      = 100
	maxQueuedDownloads    = 100
)

var (
	downloadFilenameStar    = regexp.MustCompile(`(?i)(?:^|;)\s*filename\*\s*=\s*UTF-8''([^;]+)`)
	downloadFilenamePattern = regexp.MustCompile(`(?i)(?:^|;)\s*filename\s*=\s*(?:"([^"]*)"|([^;]+))`)
)

type DownloadRequest struct {
	URL           string `json:"url"`
	DirectoryPath string `json:"directoryPath"`
	Filename      string `json:"filename,omitempty"`
}

type DownloadTask struct {
	ID                  string `json:"id"`
	URL                 string `json:"url"`
	DirectoryPath       string `json:"directoryPath"`
	Filename            string `json:"filename,omitempty"`
	Status              string `json:"status"`
	BytesDownloaded     int64  `json:"bytesDownloaded"`
	TotalBytes          *int64 `json:"totalBytes,omitempty"`
	Progress            *int   `json:"progress,omitempty"`
	SpeedBytesPerSecond *int64 `json:"speedBytesPerSecond,omitempty"`
	DestinationPath     string `json:"destinationPath,omitempty"`
	CreatedAt           string `json:"createdAt"`
	StartedAt           string `json:"startedAt,omitempty"`
	FinishedAt          string `json:"finishedAt,omitempty"`
	Error               string `json:"error,omitempty"`
	cancel              context.CancelFunc
	lastProgress        time.Time
	startedAt           time.Time
	retryOf             string
	cancelSource        string
	lifecycleStarted    bool
	lifecycleTerminal   bool
	order               uint64
}

// downloadLifecycleObserver receives only real Managed Task transitions. It
// is intentionally private so the HTTP task API remains unchanged.
type downloadLifecycleObserver interface {
	downloadAccepted(downloadLifecycleTask)
	downloadStarted(downloadLifecycleTask)
	downloadCompleted(downloadLifecycleTask)
	downloadCancelled(downloadLifecycleTask)
	downloadFailed(downloadLifecycleTask, error)
}

type DownloadManager struct {
	workspace        *Workspace
	client           *http.Client
	headerTimeout    time.Duration
	idleTimeout      time.Duration
	progressInterval time.Duration
	mu               sync.Mutex
	tasks            map[string]*DownloadTask
	queue            []string
	running          int
	nextOrder        uint64
	closed           bool
	subscriptions    map[*taskSubscription[DownloadTask]]struct{}
	now              func() time.Time
	observer         downloadLifecycleObserver
	workers          sync.WaitGroup
}

func NewDownloadManager(workspace *Workspace) *DownloadManager {
	return newDownloadManager(workspace, time.Now, nil)
}

func newDownloadManager(workspace *Workspace, now func() time.Time, observer downloadLifecycleObserver) *DownloadManager {
	if now == nil {
		now = time.Now
	}
	return &DownloadManager{
		workspace:        workspace,
		client:           &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }},
		headerTimeout:    60 * time.Second,
		idleTimeout:      60 * time.Second,
		progressInterval: 250 * time.Millisecond,
		tasks:            make(map[string]*DownloadTask),
		subscriptions:    make(map[*taskSubscription[DownloadTask]]struct{}),
		now:              now,
		observer:         observer,
	}
}

// setLifecycle installs the service observer before the listener is exposed.
// Existing standalone manager callers continue to run without presentation.
func (manager *DownloadManager) setLifecycle(observer downloadLifecycleObserver, now func() time.Time) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.observer = observer
	if now != nil {
		manager.now = now
	}
}

func (manager *DownloadManager) List() []DownloadTask {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.listLocked()
}

func (manager *DownloadManager) listLocked() []DownloadTask {
	result := make([]DownloadTask, 0, len(manager.tasks))
	for _, task := range manager.tasks {
		result = append(result, publicDownloadTask(task))
	}
	slices.SortFunc(result, func(left, right DownloadTask) int {
		if left.order > right.order {
			return -1
		}
		if left.order < right.order {
			return 1
		}
		return strings.Compare(right.CreatedAt, left.CreatedAt)
	})
	return result
}

func (manager *DownloadManager) Subscribe() (<-chan []DownloadTask, func()) {
	manager.mu.Lock()
	subscription := newTaskSubscription(manager.listLocked())
	manager.subscriptions[subscription] = struct{}{}
	manager.mu.Unlock()
	return subscription.output, func() {
		manager.mu.Lock()
		delete(manager.subscriptions, subscription)
		manager.mu.Unlock()
		subscription.close()
	}
}

func (manager *DownloadManager) notifyLocked() {
	snapshot := manager.listLocked()
	for subscription := range manager.subscriptions {
		subscription.publish(snapshot)
	}
}

func (manager *DownloadManager) closeSubscriptionsLocked() {
	for subscription := range manager.subscriptions {
		delete(manager.subscriptions, subscription)
		subscription.close()
	}
}

func (manager *DownloadManager) Enqueue(request DownloadRequest) (DownloadTask, error) {
	return manager.enqueue(request, "")
}

func (manager *DownloadManager) enqueue(request DownloadRequest, retryOf string) (DownloadTask, error) {
	if len(request.DirectoryPath) > maxDownloadPathLength || len(request.Filename) > maxDownloadPathLength {
		return DownloadTask{}, &ServiceError{Code: "INVALID_DOWNLOAD", Message: "Download request is invalid"}
	}
	url, err := validateDownloadURL(request.URL)
	if err != nil {
		return DownloadTask{}, err
	}
	directory, err := ParseWorkspacePath(request.DirectoryPath)
	if err != nil {
		return DownloadTask{}, err
	}
	filename := ""
	if request.Filename != "" {
		filename, err = uploadFilename(request.Filename)
		if err != nil {
			return DownloadTask{}, &ServiceError{Code: "INVALID_DOWNLOAD", Message: "Filename must be a single safe file name"}
		}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return DownloadTask{}, &ServiceError{Code: "DOWNLOAD_SERVICE_STOPPED", Message: "Download service is stopped"}
	}
	if len(manager.queue) >= maxQueuedDownloads {
		return DownloadTask{}, &ServiceError{Code: "DOWNLOAD_QUEUE_FULL", Message: "Download queue is full"}
	}
	id, err := newTaskID()
	if err != nil {
		return DownloadTask{}, err
	}
	task := &DownloadTask{
		ID:            id,
		URL:           url.String(),
		DirectoryPath: directory.String(),
		Filename:      filename,
		Status:        "queued",
		CreatedAt:     formatTaskTime(manager.now()),
		retryOf:       retryOf,
		order:         manager.nextOrder,
	}
	manager.nextOrder++
	manager.tasks[id] = task
	manager.queue = append(manager.queue, id)
	manager.pruneTerminalLocked()
	manager.emitDownloadAcceptedLocked(task)
	manager.pumpLocked()
	manager.notifyLocked()
	return publicDownloadTask(task), nil
}

func (manager *DownloadManager) Cancel(id string) (DownloadTask, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	task, found := manager.tasks[id]
	if !found {
		return DownloadTask{}, &ServiceError{Code: "DOWNLOAD_NOT_FOUND", Message: "Download task was not found"}
	}
	if task.Status == "queued" {
		task.Status, task.FinishedAt = "cancelled", formatTaskTime(manager.now())
		task.cancelSource = "client"
		manager.removeQueuedLocked(id)
		manager.emitDownloadTerminalLocked(task, nil)
		manager.pruneTerminalLocked()
		manager.notifyLocked()
		return publicDownloadTask(task), nil
	}
	if task.Status == "running" {
		task.Status, task.FinishedAt = "cancelled", formatTaskTime(manager.now())
		task.cancelSource = "client"
		if task.cancel != nil {
			task.cancel()
		}
		manager.emitDownloadTerminalLocked(task, nil)
		manager.pruneTerminalLocked()
		manager.notifyLocked()
		return publicDownloadTask(task), nil
	}
	return publicDownloadTask(task), nil
}

func (manager *DownloadManager) Retry(id string) (DownloadTask, error) {
	manager.mu.Lock()
	task, found := manager.tasks[id]
	if !found {
		manager.mu.Unlock()
		return DownloadTask{}, &ServiceError{Code: "DOWNLOAD_NOT_FOUND", Message: "Download task was not found"}
	}
	if task.Status != "error" && task.Status != "cancelled" {
		manager.mu.Unlock()
		return DownloadTask{}, &ServiceError{Code: "DOWNLOAD_ACTIVE", Message: "Download task is still active"}
	}
	request := DownloadRequest{URL: task.URL, DirectoryPath: task.DirectoryPath, Filename: task.Filename}
	manager.mu.Unlock()
	return manager.enqueue(request, task.ID)
}

func (manager *DownloadManager) ClearTerminal() {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for id, task := range manager.tasks {
		if task.Status == "done" || task.Status == "error" || task.Status == "cancelled" {
			delete(manager.tasks, id)
		}
	}
	manager.notifyLocked()
}

func (manager *DownloadManager) Close() {
	_, _ = manager.closeWithStats()
}

func (manager *DownloadManager) closeWithStats() (queued, active int) {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return 0, 0
	}
	manager.closed = true
	// Service shutdown folds task cancellation into its summary. Disable the
	// per-task observer before touching queued or running work.
	manager.observer = nil
	for _, id := range manager.queue {
		if task := manager.tasks[id]; task != nil && task.Status == "queued" {
			queued++
			task.Status, task.FinishedAt = "cancelled", formatTaskTime(manager.now())
			task.cancelSource = "shutdown"
		}
	}
	manager.queue = nil
	for _, task := range manager.tasks {
		if task.Status == "running" {
			active++
			task.Status, task.FinishedAt = "cancelled", formatTaskTime(manager.now())
			task.cancelSource = "shutdown"
			if task.cancel != nil {
				task.cancel()
			}
		}
	}
	manager.pruneTerminalLocked()
	manager.notifyLocked()
	manager.closeSubscriptionsLocked()
	manager.mu.Unlock()
	manager.workers.Wait()
	return queued, active
}

func (manager *DownloadManager) pumpLocked() {
	for manager.running < 2 && len(manager.queue) > 0 {
		id := manager.queue[0]
		manager.queue = manager.queue[1:]
		task := manager.tasks[id]
		if task == nil || task.Status != "queued" {
			continue
		}
		manager.running++
		manager.workers.Add(1)
		context, cancel := context.WithCancel(context.Background())
		task.cancel = cancel
		task.startedAt = manager.now()
		task.Status, task.StartedAt = "running", formatTaskTime(task.startedAt)
		task.lifecycleStarted = true
		manager.emitDownloadStartedLocked(task)
		go manager.run(context, task)
	}
}

func (manager *DownloadManager) downloadLifecycleSnapshotLocked(task *DownloadTask) downloadLifecycleTask {
	if task == nil {
		return downloadLifecycleTask{}
	}
	return downloadLifecycleTask{
		ID:              task.ID,
		URL:             task.URL,
		DirectoryPath:   task.DirectoryPath,
		Filename:        task.Filename,
		DestinationPath: task.DestinationPath,
		BytesDownloaded: task.BytesDownloaded,
		TotalBytes:      task.TotalBytes,
		StartedAt:       task.startedAt,
		RetryOf:         task.retryOf,
	}
}

func (manager *DownloadManager) emitDownloadAcceptedLocked(task *DownloadTask) {
	if manager.observer != nil {
		manager.observer.downloadAccepted(manager.downloadLifecycleSnapshotLocked(task))
	}
}

func (manager *DownloadManager) emitDownloadStartedLocked(task *DownloadTask) {
	if manager.observer != nil {
		manager.observer.downloadStarted(manager.downloadLifecycleSnapshotLocked(task))
	}
}

func (manager *DownloadManager) emitDownloadTerminalLocked(task *DownloadTask, err error) {
	if task == nil || task.lifecycleTerminal {
		return
	}
	task.lifecycleTerminal = true
	if manager.observer == nil {
		return
	}
	snapshot := manager.downloadLifecycleSnapshotLocked(task)
	switch task.Status {
	case "done":
		manager.observer.downloadCompleted(snapshot)
	case "cancelled":
		manager.observer.downloadCancelled(snapshot)
	case "error":
		manager.observer.downloadFailed(snapshot, err)
	}
}

func (manager *DownloadManager) run(ctx context.Context, task *DownloadTask) {
	defer manager.workers.Done()
	err := manager.download(ctx, task)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.running--
	task.cancel = nil
	task.FinishedAt = formatTaskTime(manager.now())
	if task.Status == "cancelled" || ctx.Err() != nil {
		task.Status = "cancelled"
		if task.cancelSource == "" {
			task.cancelSource = "client"
		}
	} else if err != nil {
		task.Status, task.Error = "error", err.Error()
	} else {
		task.Status = "done"
	}
	manager.emitDownloadTerminalLocked(task, err)
	manager.pruneTerminalLocked()
	manager.pumpLocked()
	manager.notifyLocked()
}

func (manager *DownloadManager) lifecycleSnapshot() fsShutdownSnapshot {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	snapshot := fsShutdownSnapshot{}
	for _, task := range manager.tasks {
		switch task.Status {
		case "queued":
			snapshot.QueuedDownloads++
		case "running":
			snapshot.ActiveDownloads++
		}
	}
	return snapshot
}

func (manager *DownloadManager) download(ctx context.Context, task *DownloadTask) error {
	current, err := validateDownloadURL(task.URL)
	if err != nil {
		return err
	}
	for redirects := 0; redirects <= 5; redirects++ {
		headerContext, cancelHeader := context.WithCancel(ctx)
		headerTimer := time.AfterFunc(manager.headerTimeout, cancelHeader)
		request, err := http.NewRequestWithContext(headerContext, http.MethodGet, current.String(), nil)
		if err != nil {
			headerTimer.Stop()
			cancelHeader()
			return &ServiceError{Code: "DOWNLOAD_UNAVAILABLE", Message: "Download request could not be created"}
		}
		client := *manager.client
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		response, err := client.Do(request)
		headerTimer.Stop()
		if err != nil {
			cancelHeader()
			return &ServiceError{Code: "DOWNLOAD_UNAVAILABLE", Message: "Download request failed"}
		}
		if downloadRedirectStatus(response.StatusCode) {
			location := response.Header.Get("Location")
			if response.Body != nil {
				_ = response.Body.Close()
			}
			cancelHeader()
			if location == "" || redirects == 5 {
				return &ServiceError{Code: "DOWNLOAD_UNAVAILABLE", Message: "Download redirect failed"}
			}
			redirect, parseErr := url.Parse(location)
			if parseErr != nil {
				return &ServiceError{Code: "DOWNLOAD_UNAVAILABLE", Message: "Download redirect failed"}
			}
			current, err = validateDownloadURL(current.ResolveReference(redirect).String())
			if err != nil {
				return err
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			if response.Body != nil {
				_ = response.Body.Close()
			}
			cancelHeader()
			return &ServiceError{Code: "DOWNLOAD_UNAVAILABLE", Message: "Remote server returned an unsuccessful status"}
		}
		if response.Body == nil {
			cancelHeader()
			return &ServiceError{Code: "DOWNLOAD_UNAVAILABLE", Message: "Remote response has no body"}
		}
		defer cancelHeader()
		defer response.Body.Close()
		stopBodyClose := context.AfterFunc(ctx, func() { _ = response.Body.Close() })
		defer stopBodyClose()
		filename := task.Filename
		if filename == "" {
			filename = responseFilename(response, current)
		}
		totalBytes, err := responseSize(response)
		if err != nil {
			return err
		}
		directory, err := ParseWorkspacePath(task.DirectoryPath)
		if err != nil {
			return err
		}
		manager.mu.Lock()
		task.Filename, task.TotalBytes = filename, totalBytes
		manager.notifyLocked()
		manager.mu.Unlock()
		started := manager.now()
		result, err := manager.workspace.Download(directory, filename, idleDownloadReader{reader: response.Body, close: response.Body.Close, timeout: manager.idleTimeout}, func(downloaded int64) {
			manager.updateProgress(task, downloaded, started, false)
		})
		if err != nil {
			return err
		}
		manager.updateProgress(task, result.Size, started, true)
		manager.mu.Lock()
		task.Filename, task.DestinationPath = result.Filename, result.Path
		manager.notifyLocked()
		manager.mu.Unlock()
		return nil
	}
	return &ServiceError{Code: "DOWNLOAD_UNAVAILABLE", Message: "Download redirect failed"}
}

func downloadRedirectStatus(status int) bool {
	return status == http.StatusMovedPermanently || status == http.StatusFound || status == http.StatusSeeOther || status == http.StatusTemporaryRedirect || status == http.StatusPermanentRedirect
}

func (manager *DownloadManager) removeQueuedLocked(id string) {
	for index, queuedID := range manager.queue {
		if queuedID == id {
			manager.queue = append(manager.queue[:index], manager.queue[index+1:]...)
			return
		}
	}
}

func (manager *DownloadManager) pruneTerminalLocked() {
	for len(manager.tasks) > maxDownloadTasks {
		var oldest *DownloadTask
		for _, task := range manager.tasks {
			if !terminalDownloadStatus(task.Status) {
				continue
			}
			if oldest == nil || task.order < oldest.order {
				oldest = task
			}
		}
		if oldest == nil {
			return
		}
		delete(manager.tasks, oldest.ID)
	}
}

func terminalDownloadStatus(status string) bool {
	return status == "done" || status == "error" || status == "cancelled"
}

func validateDownloadURL(value string) (*url.URL, error) {
	if len(value) == 0 || len(value) > maxDownloadURLLength {
		return nil, &ServiceError{Code: "INVALID_DOWNLOAD", Message: "Download URL is invalid"}
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return nil, &ServiceError{Code: "INVALID_DOWNLOAD", Message: "Download URL is invalid"}
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, &ServiceError{Code: "INVALID_DOWNLOAD", Message: "Download URL is invalid"}
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, &ServiceError{Code: "URL_FORBIDDEN", Message: "Download targets cannot use local hostnames"}
	}
	address := net.ParseIP(host)
	if address == nil {
		var numeric bool
		address, numeric = downloadIPv4Literal(host)
		if numeric && address == nil {
			return nil, &ServiceError{Code: "INVALID_DOWNLOAD", Message: "Download URL is invalid"}
		}
	}
	if address != nil && blockedDownloadAddress(address) {
		return nil, &ServiceError{Code: "URL_FORBIDDEN", Message: "Download targets cannot use private or reserved addresses"}
	}
	if address != nil {
		host = address.String()
	} else {
		host, err = idna.Lookup.ToASCII(host)
		if err != nil || host == "" {
			return nil, &ServiceError{Code: "INVALID_DOWNLOAD", Message: "Download URL is invalid"}
		}
		host = strings.ToLower(host)
	}
	port := parsed.Port()
	if port != "" {
		portNumber, portErr := strconv.ParseUint(port, 10, 16)
		if portErr != nil || portNumber > 65535 {
			return nil, &ServiceError{Code: "INVALID_DOWNLOAD", Message: "Download URL is invalid"}
		}
		if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
			port = ""
		}
	}
	if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	} else {
		parsed.Host = host
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	}
	if parsed.Path != "" {
		trailingSlash := strings.HasSuffix(parsed.Path, "/")
		parsed.Path = path.Clean(parsed.Path)
		if parsed.Path == "." {
			parsed.Path = ""
		} else if trailingSlash && parsed.Path != "/" {
			parsed.Path += "/"
		}
	}
	return parsed, nil
}

func downloadIPv4Literal(host string) (net.IP, bool) {
	host = strings.TrimSuffix(host, ".")
	if host == "" {
		return nil, false
	}
	parts := strings.Split(host, ".")
	if len(parts) > 4 {
		for _, part := range parts {
			if _, numeric := downloadIPv4Part(part); !numeric {
				return nil, false
			}
		}
		return nil, true
	}
	values := make([]uint64, len(parts))
	for index, part := range parts {
		value, numeric := downloadIPv4Part(part)
		if !numeric {
			return nil, false
		}
		values[index] = value
	}
	for _, value := range values[:len(values)-1] {
		if value > 255 {
			return nil, true
		}
	}
	lastBits := uint(8 * (5 - len(values)))
	if values[len(values)-1] >= uint64(1)<<lastBits {
		return nil, true
	}
	number := values[len(values)-1]
	for index, value := range values[:len(values)-1] {
		number |= value << (8 * (3 - index))
	}
	return net.IPv4(byte(number>>24), byte(number>>16), byte(number>>8), byte(number)), true
}

func downloadIPv4Part(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	base := 10
	digits := value
	if len(value) > 2 && (value[0:2] == "0x" || value[0:2] == "0X") {
		base, digits = 16, value[2:]
	} else if len(value) > 1 && value[0] == '0' {
		base = 8
		for _, digit := range value {
			if digit < '0' || digit > '7' {
				base = 10
				break
			}
		}
	}
	for _, digit := range digits {
		valid := digit >= '0' && digit <= '9'
		if base == 16 {
			valid = valid || digit >= 'a' && digit <= 'f' || digit >= 'A' && digit <= 'F'
		}
		if !valid {
			return 0, false
		}
	}
	parsed, err := strconv.ParseUint(digits, base, 64)
	return parsed, err == nil
}

func blockedDownloadAddress(address net.IP) bool {
	if address = address.To4(); address != nil {
		first, second := address[0], address[1]
		return first == 0 || first == 10 || (first == 100 && second >= 64 && second <= 127) || first == 127 ||
			(first == 169 && second == 254) || (first == 172 && second >= 16 && second <= 31) ||
			(first == 192 && (second == 0 || second == 2 || second == 88 || second == 168)) ||
			(first == 198 && (second == 18 || second == 19 || second == 51)) || (first == 203 && second == 0) || first >= 224
	}
	address = address.To16()
	if address == nil {
		return true
	}
	first, second := uint16(address[0])<<8|uint16(address[1]), uint16(address[2])<<8|uint16(address[3])
	return first == 0 || first == 0xffff || first >= 0xff00 || (first == 0x2001 && second == 0x0db8) ||
		first&0xfe00 == 0xfc00 || first&0xffc0 == 0xfe80
}

func responseFilename(response *http.Response, location *url.URL) string {
	if name := headerFilename(response.Header.Get("Content-Disposition")); name != "" {
		return name
	}
	name, err := safeResponseFilename(path.Base(location.Path))
	if err == nil {
		return name
	}
	return "download"
}

func headerFilename(value string) string {
	if value == "" {
		return ""
	}
	encoded := downloadFilenameStar.FindStringSubmatch(value)
	if len(encoded) > 1 {
		name, err := url.PathUnescape(encoded[1])
		if err != nil || !utf8.ValidString(name) {
			return ""
		}
		result, _ := safeResponseFilename(name)
		return result
	}
	plain := downloadFilenamePattern.FindStringSubmatch(value)
	if len(plain) == 0 {
		return ""
	}
	name := plain[1]
	if name == "" {
		name = strings.TrimSpace(plain[2])
	}
	result, _ := safeResponseFilename(name)
	return result
}

func safeResponseFilename(value string) (string, error) {
	value = path.Base(strings.ReplaceAll(value, "\\", "/"))
	if !utf8.ValidString(value) {
		return "", &ServiceError{Code: "INVALID_DOWNLOAD", Message: "Download filename is invalid"}
	}
	return uploadFilename(value)
}

func responseSize(response *http.Response) (*int64, error) {
	if response.Header.Get("Content-Encoding") != "" {
		return nil, nil
	}
	value := response.Header.Get("Content-Length")
	if value == "" {
		return nil, nil
	}
	size, err := strconv.ParseInt(value, 10, 64)
	if err != nil || size < 0 || size > maxSafeDownloadBytes {
		return nil, &ServiceError{Code: "DOWNLOAD_UNAVAILABLE", Message: "Remote response size is too large or invalid"}
	}
	return &size, nil
}

func (manager *DownloadManager) updateProgress(task *DownloadTask, downloaded int64, started time.Time, force bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !force && time.Since(task.lastProgress) < manager.progressInterval {
		return
	}
	task.lastProgress = manager.now()
	task.BytesDownloaded = downloaded
	if task.TotalBytes != nil {
		progress := 100
		if *task.TotalBytes > 0 {
			progress = min(100, int(downloaded*100 / *task.TotalBytes))
		}
		task.Progress = &progress
	}
	elapsed := max(time.Since(started).Seconds(), 0.001)
	speed := int64(math.Round(float64(downloaded) / elapsed))
	task.SpeedBytesPerSecond = &speed
	manager.notifyLocked()
}

type idleDownloadReader struct {
	reader  io.Reader
	close   func() error
	timeout time.Duration
}

func (reader idleDownloadReader) Read(bytes []byte) (int, error) {
	timer := time.AfterFunc(reader.timeout, func() { _ = reader.close() })
	count, err := reader.reader.Read(bytes)
	timer.Stop()
	return count, err
}

func newTaskID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
func formatTaskTime(value time.Time) string { return value.UTC().Format("2006-01-02T15:04:05.000Z") }
func publicDownloadTask(task *DownloadTask) DownloadTask {
	result := *task
	result.cancel = nil
	result.startedAt = time.Time{}
	result.retryOf = ""
	result.cancelSource = ""
	result.lifecycleStarted = false
	result.lifecycleTerminal = false
	return result
}
