package appconfig

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"
)

const (
	configFileName = "config.json"
	configLockName = ".config.lock"
	saltLength     = 32
)

// Dependencies supplies process facts that appconfig owns.
type Dependencies struct {
	Environment func(string) string
	UserHomeDir func() (string, error)
	MachineID   func() (string, error)
	Username    func() (string, error)
	Hostname    func() (string, error)
}

// Store is the sole semantic owner of the local config.json document.
type Store struct {
	configDirectory   string
	environment       func(string) string
	random            io.Reader
	machineID         func() (string, error)
	username          func() (string, error)
	now               func() time.Time
	sleep             func(time.Duration)
	pid               int
	processAlive      func(int) (bool, error)
	newLockID         func() (string, error)
	replaceConfigFile func(string, string) error
	lockTimeout       time.Duration
	lockRetry         time.Duration
	lockGrace         time.Duration
}

// New constructs a Store without creating configuration state.
func New(dependencies Dependencies) (*Store, error) {
	if dependencies.Environment == nil {
		dependencies.Environment = os.Getenv
	}
	if dependencies.UserHomeDir == nil {
		dependencies.UserHomeDir = os.UserHomeDir
	}
	if dependencies.Username == nil {
		dependencies.Username = currentUsername
	}
	if dependencies.Hostname == nil {
		dependencies.Hostname = os.Hostname
	}
	if dependencies.MachineID == nil {
		dependencies.MachineID = func() (string, error) {
			return machineIDWithFallback(nativeMachineID, dependencies.Hostname, dependencies.Username)
		}
	}

	home, err := resolveHomeDirectory(dependencies.Environment, dependencies.UserHomeDir)
	if err != nil {
		return nil, err
	}
	store := &Store{
		configDirectory:   filepath.Join(home, ".ycy-cli"),
		environment:       dependencies.Environment,
		random:            rand.Reader,
		machineID:         dependencies.MachineID,
		username:          dependencies.Username,
		now:               time.Now,
		sleep:             time.Sleep,
		pid:               os.Getpid(),
		processAlive:      nativeProcessAlive,
		replaceConfigFile: replaceConfigFile,
		lockTimeout:       10 * time.Second,
		lockRetry:         25 * time.Millisecond,
		lockGrace:         time.Second,
	}
	store.newLockID = func() (string, error) {
		return randomUUID(store.random)
	}
	return store, nil
}

func currentUsername() (string, error) {
	for _, key := range []string{"USERNAME", "USER"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value, nil
		}
	}
	current, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("resolve current username: %w", err)
	}
	if value := strings.TrimSpace(current.Username); value != "" {
		return value, nil
	}
	return "", errors.New("resolve current username: empty username")
}

func (store *Store) keyForSalt(salt string) ([]byte, error) {
	machineID, err := store.machineID()
	if err != nil {
		return nil, fmt.Errorf("resolve machine ID: %w", err)
	}
	username, err := store.username()
	if err != nil {
		return nil, fmt.Errorf("resolve username: %w", err)
	}
	return deriveKey(salt, machineID, username)
}

func resolveHomeDirectory(environment func(string) string, userHomeDir func() (string, error)) (string, error) {
	for _, key := range []string{"USERPROFILE", "HOME"} {
		if value := environment(key); value != "" {
			return value, nil
		}
	}
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	if home == "" {
		return "", errors.New("resolve user home directory: empty path")
	}
	return home, nil
}

func (store *Store) configPath() string {
	return filepath.Join(store.configDirectory, configFileName)
}

func (store *Store) lockPath() string {
	return filepath.Join(store.configDirectory, configLockName)
}

func (store *Store) newSalt() (string, error) {
	bytes := make([]byte, saltLength)
	if _, err := io.ReadFull(store.random, bytes); err != nil {
		return "", fmt.Errorf("generate config salt: %w", err)
	}
	return base64.StdEncoding.EncodeToString(bytes), nil
}

func (store *Store) emptyDocument() (document, error) {
	salt, err := store.newSalt()
	if err != nil {
		return document{}, err
	}
	return document{
		Salt: salt,
		Fork: forkDocument{Instances: map[string]forkDocumentInstance{}},
	}, nil
}
