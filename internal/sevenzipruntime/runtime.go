package sevenzipruntime

import (
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/hackycy/hackycy-cli/internal/sevenzipmanifest"
)

const runtimeLockWait = 5 * time.Second

var materializeMu sync.Mutex

type LookupPath func(string) (string, error)

func Ensure() (string, error) {
	stateRoot, err := StateRoot()
	if err != nil {
		return "", err
	}
	return EnsureAt(stateRoot, Current(), exec.LookPath)
}

func StateRoot() (string, error) {
	if runtime.GOOS == "windows" {
		if root := os.Getenv("LOCALAPPDATA"); root != "" {
			return root, nil
		}
	}
	if runtime.GOOS == "linux" {
		if root := os.Getenv("XDG_STATE_HOME"); root != "" {
			return root, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve 7-Zip state root: %w", err)
	}
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(home, "AppData", "Local"), nil
	case "darwin":
		return filepath.Join(home, "Library", "Application Support"), nil
	default:
		return filepath.Join(home, ".local", "state"), nil
	}
}

func EnsureAt(stateRoot string, payload Payload, lookup LookupPath) (string, error) {
	if len(payload.Files) == 0 {
		return sourceRuntime(lookup)
	}
	if err := payload.Verify(); err != nil {
		return "", err
	}
	if stateRoot == "" {
		return "", errors.New("7-Zip state root is empty")
	}
	directory := filepath.Join(stateRoot, "ycy", "7zip", sevenzipmanifest.Version)
	return materialize(directory, payload)
}

func sourceRuntime(lookup LookupPath) (string, error) {
	if lookup == nil {
		lookup = exec.LookPath
	}
	for _, name := range []string{"7zz", "7z"} {
		if executable, err := lookup(name); err == nil {
			return executable, nil
		}
	}
	return "", errors.New("7-Zip runtime is unavailable; install 7zz or build with an embedded runtime")
}

func materialize(directory string, payload Payload) (string, error) {
	materializeMu.Lock()
	defer materializeMu.Unlock()
	parent := filepath.Dir(directory)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create 7-Zip state parent: %w", err)
	}
	if err := os.Chmod(parent, 0o700); err != nil && runtime.GOOS != "windows" {
		return "", fmt.Errorf("protect 7-Zip state parent: %w", err)
	}
	release, err := acquireRuntimeLock(parent)
	if err != nil {
		return "", err
	}
	defer release()
	if validRuntime(directory, payload) {
		return filepath.Join(directory, executableName(payload)), nil
	}
	candidate, err := os.MkdirTemp(parent, ".candidate-")
	if err != nil {
		return "", fmt.Errorf("create 7-Zip runtime candidate: %w", err)
	}
	defer os.RemoveAll(candidate)
	if err := os.Chmod(candidate, 0o700); err != nil && runtime.GOOS != "windows" {
		return "", fmt.Errorf("protect 7-Zip runtime candidate: %w", err)
	}
	for _, file := range payload.Files {
		mode := os.FileMode(0o600)
		if file.Metadata.Executable {
			mode = 0o700
		}
		target := filepath.Join(candidate, file.Metadata.Filename)
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return "", fmt.Errorf("create 7-Zip runtime file %s: %w", file.Metadata.Filename, err)
		}
		_, writeErr := output.Write(file.Bytes)
		if syncErr := output.Sync(); writeErr == nil {
			writeErr = syncErr
		}
		if closeErr := output.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			return "", fmt.Errorf("write 7-Zip runtime file %s: %w", file.Metadata.Filename, writeErr)
		}
	}
	if !validRuntime(candidate, payload) {
		return "", errors.New("materialized 7-Zip runtime did not verify")
	}
	if err := os.RemoveAll(directory); err != nil {
		return "", fmt.Errorf("remove invalid 7-Zip runtime: %w", err)
	}
	if err := os.Rename(candidate, directory); err != nil {
		return "", fmt.Errorf("publish 7-Zip runtime: %w", err)
	}
	return filepath.Join(directory, executableName(payload)), nil
}

func validRuntime(directory string, payload Payload) bool {
	for _, file := range payload.Files {
		info, err := os.Lstat(filepath.Join(directory, file.Metadata.Filename))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		if runtime.GOOS != "windows" {
			mode := os.FileMode(0o600)
			if file.Metadata.Executable {
				mode = 0o700
			}
			if info.Mode().Perm() != mode {
				return false
			}
		}
		bytes, err := os.ReadFile(filepath.Join(directory, file.Metadata.Filename))
		if err != nil || digest(bytes) != file.Metadata.SHA256 {
			return false
		}
	}
	return true
}

func executableName(payload Payload) string {
	for _, file := range payload.Files {
		if file.Metadata.Executable {
			return file.Metadata.Filename
		}
	}
	return ""
}

func acquireRuntimeLock(directory string) (func(), error) {
	path := filepath.Join(directory, ".runtime.lock")
	deadline := time.Now().Add(runtimeLockWait)
	for {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, _ = file.WriteString(randomLockToken())
			_ = file.Close()
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("lock 7-Zip runtime: %w", err)
		}
		if time.Now().After(deadline) {
			return nil, errors.New("timed out waiting for 7-Zip runtime materialization")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func randomLockToken() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "runtime-lock"
	}
	return fmt.Sprintf("%x", bytes)
}
