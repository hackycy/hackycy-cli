package gitprocess

import (
	"errors"
	"io/fs"
	"os/exec"
)

// processNotFoundError preserves the platform-specific message while exposing
// the filesystem-level missing executable contract to callers.
type processNotFoundError struct {
	err error
}

func (err processNotFoundError) Error() string {
	return err.err.Error()
}

func (err processNotFoundError) Unwrap() error {
	return fs.ErrNotExist
}

func (err processNotFoundError) Is(target error) bool {
	return target == fs.ErrNotExist || errors.Is(err.err, target)
}

func normalizeProcessStartError(err error) error {
	if err == nil || !errors.Is(err, exec.ErrNotFound) {
		return err
	}
	return processNotFoundError{err: err}
}
