package fs

import (
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/hackycy/hackycy-cli/internal/filesession"
	"golang.org/x/crypto/argon2"
)

const (
	passwordMemoryKiB uint32 = 65_536
	passwordTimeCost  uint32 = 3
	passwordKeyBytes         = 32
	passwordSaltBytes        = 16
)

type Account struct {
	Username string
	key      string
	salt     []byte
	hash     []byte
	revision string
}

type SessionAccount struct {
	Username string `json:"username"`
}

type SessionGrant struct {
	Token     string
	Account   SessionAccount
	ExpiresAt time.Time
}

type SessionState struct {
	Account   SessionAccount
	ExpiresAt time.Time
}

type Authentication struct {
	accounts     map[string]Account
	store        *filesession.Manager
	idleLifetime time.Duration
	closed       bool
}

type AuthenticationOptions struct {
	SessionDirectory string
	IdleLifetime     time.Duration
	MaxAccountTokens int
	MaxTokens        int
}

func NewAuthentication(specifications []string, options AuthenticationOptions) (*Authentication, error) {
	if len(specifications) == 0 {
		return nil, nil
	}
	idleLifetime := options.IdleLifetime
	if idleLifetime == 0 {
		idleLifetime = 7 * 24 * time.Hour
	}
	parsed := make([]Account, 0, len(specifications))
	seen := make(map[string]struct{}, len(specifications))
	for _, specification := range specifications {
		username, password, err := parseAccount(specification)
		if err != nil {
			return nil, err
		}
		key := strings.ToLower(username)
		if _, found := seen[key]; found {
			return nil, fmt.Errorf("username %q is specified more than once", username)
		}
		seen[key] = struct{}{}
		salt := make([]byte, passwordSaltBytes)
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("generate account password salt: %w", err)
		}
		parsed = append(parsed, Account{Username: username, key: key, salt: salt, hash: deriveAccountHash(password, salt)})
	}
	store, err := filesession.Open(filesession.Options{
		BaseDirectory:      options.SessionDirectory,
		IdleLifetime:       options.IdleLifetime,
		MaxSubjectSessions: options.MaxAccountTokens,
		MaxSessions:        options.MaxTokens,
	})
	if err != nil {
		return nil, err
	}
	authentication := &Authentication{
		accounts:     make(map[string]Account, len(parsed)),
		store:        store,
		idleLifetime: idleLifetime,
	}
	for _, account := range parsed {
		revision, err := store.CredentialRevision(account.key + "\x00" + passwordForAccount(specifications, account.Username))
		if err != nil {
			_ = store.Close()
			return nil, err
		}
		account.revision = revision
		authentication.accounts[account.key] = account
	}
	return authentication, nil
}

// SessionIdleLifetime reports the configured idle lifetime for safe startup
// presentation without exposing session-store internals.
func (authentication *Authentication) SessionIdleLifetime() time.Duration {
	if authentication == nil {
		return 0
	}
	return authentication.idleLifetime
}

func parseAccount(specification string) (string, string, error) {
	separator := strings.IndexByte(specification, ':')
	if separator < 0 {
		return "", "", errors.New("account must use <username>:<password>")
	}
	username, password := specification[:separator], specification[separator+1:]
	if !validAccountUsername(username) {
		return "", "", errors.New("username must contain 1-64 ASCII letters, numbers, dots, underscores, or hyphens")
	}
	if countUTF16Units(password) < 5 || countUTF16Units(password) > 256 {
		return "", "", errors.New("password must contain 5-256 characters")
	}
	return username, password, nil
}

func passwordForAccount(specifications []string, username string) string {
	for _, specification := range specifications {
		candidate, password, err := parseAccount(specification)
		if err == nil && strings.EqualFold(candidate, username) {
			return password
		}
	}
	return ""
}

func validAccountUsername(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, byteValue := range []byte(value) {
		if (byteValue >= 'a' && byteValue <= 'z') || (byteValue >= 'A' && byteValue <= 'Z') || (byteValue >= '0' && byteValue <= '9') || byteValue == '.' || byteValue == '_' || byteValue == '-' {
			continue
		}
		return false
	}
	return true
}

func countUTF16Units(value string) int {
	return len(utf16.Encode([]rune(value)))
}

func deriveAccountHash(password string, salt []byte) []byte {
	return argon2.IDKey([]byte(password), salt, passwordTimeCost, passwordMemoryKiB, 1, passwordKeyBytes)
}

func (authentication *Authentication) SignIn(username, password string) (*SessionGrant, error) {
	if authentication == nil || authentication.closed {
		return nil, nil
	}
	key := strings.ToLower(username)
	account, found := authentication.accounts[key]
	if !validAccountUsername(username) {
		found = false
	}
	if !found {
		for _, candidate := range authentication.accounts {
			account = candidate
			break
		}
	}
	candidate := deriveAccountHash(password, account.salt)
	valid := subtle.ConstantTimeCompare(candidate, account.hash) == 1
	if !found || !valid {
		return nil, nil
	}
	session, err := authentication.store.Issue(account.key, account.revision)
	if err != nil {
		return nil, err
	}
	return &SessionGrant{Token: session.Token, Account: SessionAccount{Username: account.Username}, ExpiresAt: session.ExpiresAt}, nil
}

func (authentication *Authentication) Resume(token string) (*SessionState, error) {
	if authentication == nil || authentication.closed || token == "" {
		return nil, nil
	}
	session, err := authentication.store.Resume(token, func(subject string) string {
		return authentication.accounts[subject].revision
	})
	if err != nil || session == nil {
		return nil, err
	}
	account, found := authentication.accounts[session.Subject]
	if !found {
		return nil, nil
	}
	return &SessionState{Account: SessionAccount{Username: account.Username}, ExpiresAt: session.ExpiresAt}, nil
}

func (authentication *Authentication) SignOut(token string) error {
	if authentication == nil || authentication.closed {
		return nil
	}
	return authentication.store.Revoke(token)
}

func (authentication *Authentication) Observe(token string, listener func()) func() {
	if authentication == nil || authentication.closed {
		return func() {}
	}
	return authentication.store.Observe(token, listener)
}

func (authentication *Authentication) SessionDirectory() string {
	if authentication == nil {
		return ""
	}
	return authentication.store.Directory()
}

func (authentication *Authentication) Close() error {
	if authentication == nil || authentication.closed {
		return nil
	}
	authentication.closed = true
	return authentication.store.Close()
}
