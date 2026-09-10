package fs

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"

	"github.com/hackycy/hackycy-cli/internal/sevenzipruntime"
)

// DefaultSessionDirectory returns the fresh-Go session base for the lexical
// command directory. It intentionally never inspects any preexisting state.
func DefaultSessionDirectory(directory string) (string, error) {
	return defaultSessionDirectory(directory, sevenzipruntime.StateRoot)
}

func defaultSessionDirectory(directory string, stateRoot func() (string, error)) (string, error) {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return "", fmt.Errorf("resolve session directory input: %w", err)
	}
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(abs))
	return filepath.Join(root, "ycy", "fs", "sessions", fmt.Sprintf("%x", digest)), nil
}
