package zip

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type zipPublishFile interface {
	Write([]byte) (int, error)
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}

type zipPublishOps struct {
	create  func(string, string, os.FileMode) (string, zipPublishFile, error)
	replace func(string, string) error
}

func defaultZipPublishOps() zipPublishOps {
	return zipPublishOps{create: createZipCandidate, replace: replaceZipFile}
}

func publishZipFile(outputPath string, content []byte, ops zipPublishOps) (err error) {
	mode := os.FileMode(0o666)
	info, statErr := os.Lstat(outputPath)
	if statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		// Keep WriteFile's behavior for existing file links without replacing
		// the link itself. Unresolvable links must remain untouched.
		resolved, err := filepath.EvalSymlinks(outputPath)
		if err != nil {
			return fmt.Errorf("resolve archive destination: %w", err)
		}
		outputPath = resolved
		info, statErr = os.Stat(outputPath)
	}
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect archive destination: %w", statErr)
	}
	if statErr == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("archive destination is not a regular file: %w", os.ErrInvalid)
		}
		mode = info.Mode().Perm()
	}
	candidate, file, err := ops.create(filepath.Dir(outputPath), ".ycy-zip.tmp-", mode)
	if err != nil {
		return fmt.Errorf("create archive candidate: %w", err)
	}
	published := false
	defer func() {
		if !published {
			if cleanupErr := os.Remove(candidate); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				err = errors.Join(err, fmt.Errorf("clean up archive candidate: %w", cleanupErr))
			}
		}
	}()

	if err := writeZipCandidate(file, content, mode, statErr == nil); err != nil {
		return err
	}
	if err := ops.replace(candidate, outputPath); err != nil {
		return fmt.Errorf("publish archive: %w", err)
	}
	published = true
	return nil
}

func writeZipCandidate(file zipPublishFile, content []byte, mode os.FileMode, preserveMode bool) (err error) {
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close archive candidate: %w", closeErr))
		}
	}()
	for len(content) > 0 {
		written, err := file.Write(content)
		if err != nil {
			return fmt.Errorf("write archive candidate: %w", err)
		}
		if written == 0 {
			return fmt.Errorf("write archive candidate: %w", io.ErrShortWrite)
		}
		content = content[written:]
	}
	// Creation applies umask; restore the existing destination's permissions
	// exactly before syncing and publishing the candidate.
	if preserveMode {
		if err := file.Chmod(mode); err != nil {
			return fmt.Errorf("set archive candidate permissions: %w", err)
		}
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync archive candidate: %w", err)
	}
	return nil
}

func createZipCandidate(directory, prefix string, mode os.FileMode) (string, zipPublishFile, error) {
	for {
		randomPart := make([]byte, 16)
		if _, err := rand.Read(randomPart); err != nil {
			return "", nil, err
		}
		path := filepath.Join(directory, prefix+hex.EncodeToString(randomPart))
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return path, file, nil
	}
}
