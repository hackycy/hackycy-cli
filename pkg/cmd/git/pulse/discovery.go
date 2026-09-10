// Package pulse owns workspace Git activity reporting.
package pulse

import (
	"context"
	"os"
	"path/filepath"
)

const scanYieldEvery = 100

var excludedDirectoryNames = map[string]struct{}{
	"node_modules":     {},
	"vendor":           {},
	"dist":             {},
	".cache":           {},
	"Library":          {},
	".Trash":           {},
	"bower_components": {},
	"__pycache__":      {},
	".venv":            {},
	"venv":             {},
}

// DirectoryReader is the command-owned filesystem boundary for repository scanning.
type DirectoryReader interface {
	ReadDir(string) ([]os.DirEntry, error)
}

// ScanRepositoryResult keeps normal discovery separate from unreadable child
// directories. The latter remain non-fatal, but presentation needs a bounded
// warning without changing repository membership.
type ScanRepositoryResult struct {
	Repositories          []string
	UnreadableDirectories []string
}

// ScanRepositories finds directories that contain a direct .git directory.
// It intentionally keeps walking below a discovered repository so nested repositories remain visible.
func ScanRepositories(ctx context.Context, root string, reader DirectoryReader, onFound func(string), yield func()) ([]string, error) {
	result, err := ScanRepositoryDetails(ctx, root, reader, onFound, yield)
	return result.Repositories, err
}

// ScanRepositoryDetails is ScanRepositories with non-fatal child-read evidence
// for the terminal presentation layer.
func ScanRepositoryDetails(ctx context.Context, root string, reader DirectoryReader, onFound func(string), yield func()) (ScanRepositoryResult, error) {
	repositories := make([]string, 0)
	unreadable := make([]string, 0)
	stack := []string{root}
	scanned := 0

	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return ScanRepositoryResult{}, err
		}
		last := len(stack) - 1
		current := stack[last]
		stack = stack[:last]
		scanned++

		entries, err := reader.ReadDir(current)
		if err != nil {
			unreadable = append(unreadable, current)
			continue
		}

		hasGitDirectory := false
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			if entry.Name() == ".git" {
				hasGitDirectory = true
				continue
			}
			if _, excluded := excludedDirectoryNames[entry.Name()]; excluded {
				continue
			}
			stack = append(stack, filepath.Join(current, entry.Name()))
		}

		if hasGitDirectory {
			repositories = append(repositories, current)
			if onFound != nil {
				onFound(current)
			}
		}
		if scanned%scanYieldEvery == 0 && yield != nil {
			yield()
		}
	}

	return ScanRepositoryResult{Repositories: repositories, UnreadableDirectories: unreadable}, nil
}
