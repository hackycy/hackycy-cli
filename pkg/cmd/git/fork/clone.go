package fork

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// CloneOutput is the captured outcome of one external Git clone invocation.
type CloneOutput struct {
	Stderr   []byte
	ExitCode int
}

// CloneRunner is Git Fork's command-owned external-Git clone boundary.
type CloneRunner interface {
	Run(context.Context, []string) (CloneOutput, error)
}

// DirectoryRemover removes the clone metadata after a successful clone.
type DirectoryRemover interface {
	RemoveAll(string) error
}

// CloneFallback performs the legacy shallow-clone fallback and removes its Git metadata.
func CloneFallback(ctx context.Context, runner CloneRunner, remover DirectoryRemover, repository Repository, destination string) error {
	return cloneFallbackWithProgress(ctx, runner, remover, repository, destination, nil)
}

type cloneFallbackStage uint8

const (
	cloneFallbackStarted cloneFallbackStage = iota
	cloneFallbackCompleted
	cloneFallbackFailed
	cloneMetadataRemovalStarted
	cloneMetadataRemovalCompleted
	cloneMetadataRemovalFailed
)

// cloneFallbackWithProgress keeps the public legacy helper intact while
// exposing the two real mutation boundaries to the terminal adapter.
func cloneFallbackWithProgress(ctx context.Context, runner CloneRunner, remover DirectoryRemover, repository Repository, destination string, progress func(cloneFallbackStage, error)) error {
	if runner == nil {
		return errors.New("git fork clone runner is required")
	}
	if remover == nil {
		return errors.New("git fork clone metadata remover is required")
	}
	if progress != nil {
		progress(cloneFallbackStarted, nil)
	}
	arguments := []string{"clone", "--depth=1", "--single-branch"}
	if repository.Ref != "" {
		arguments = append(arguments, "--branch", repository.Ref)
	}
	arguments = append(arguments, CloneURL(repository), destination)
	output, err := runner.Run(ctx, arguments)
	if err != nil {
		if progress != nil {
			progress(cloneFallbackFailed, err)
		}
		return err
	}
	if output.ExitCode != 0 {
		message := strings.TrimSpace(string(output.Stderr))
		if message == "" {
			message = fmt.Sprintf("git clone failed with exit code %d", output.ExitCode)
		}
		err = errors.New(message)
		if progress != nil {
			progress(cloneFallbackFailed, err)
		}
		return err
	}
	if progress != nil {
		progress(cloneFallbackCompleted, nil)
		progress(cloneMetadataRemovalStarted, nil)
	}
	err = remover.RemoveAll(filepath.Join(destination, ".git"))
	if progress != nil {
		if err != nil {
			progress(cloneMetadataRemovalFailed, err)
		} else {
			progress(cloneMetadataRemovalCompleted, nil)
		}
	}
	return err
}
