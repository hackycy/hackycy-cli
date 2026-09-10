package rm

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
)

func TestDeletePathsRemovesContainedFilesDirectoriesAndFinalSymlinks(t *testing.T) {
	root := newDisposableRoot(t)
	file := writeDisposableFile(t, root, "file.txt")
	directory := makeDirectory(t, root, "directory", "nested")
	writeDisposableFile(t, directory, "child.txt")
	linkedTarget := writeDisposableFile(t, root, "linked-target.txt")
	link := filepath.Join(root, "link")
	if err := os.Symlink(linkedTarget, link); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	targets := []string{file, filepath.Dir(directory), link}
	assertContainedTargets(t, root, targets...)

	result := deletePaths(targets, pathRemoverFunc(os.RemoveAll))

	if result.succeeded != len(targets) || len(result.failures) != 0 {
		t.Fatalf("deletion result = %#v, want %d successes and no failures", result, len(targets))
	}
	for _, target := range targets {
		if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("deleted target %s = %v, want missing", target, err)
		}
	}
	if contents, err := os.ReadFile(linkedTarget); err != nil || string(contents) != "contents" {
		t.Fatalf("final symlink deletion changed target = (%v, %q)", err, contents)
	}
}

func TestDeletePathsTreatsMissingPathsAsSuccessfulForcefulDeletion(t *testing.T) {
	root := newDisposableRoot(t)
	missing := filepath.Join(root, "missing")
	assertContainedTargets(t, root, missing)

	result := deletePaths([]string{missing}, pathRemoverFunc(os.RemoveAll))

	if result.succeeded != 1 || len(result.failures) != 0 {
		t.Fatalf("missing deletion result = %#v, want one success", result)
	}
}

func TestDeletePathsAttemptsEveryTargetAndPreservesPartialFailureOrder(t *testing.T) {
	root := newDisposableRoot(t)
	first := writeDisposableFile(t, root, "first.txt")
	failed := writeDisposableFile(t, root, "failed.txt")
	last := writeDisposableFile(t, root, "last.txt")
	targets := []string{first, failed, last}
	assertContainedTargets(t, root, targets...)
	firstFailure := errors.New("first failure")
	secondFailure := errors.New("second failure")
	remover := &recordingRemover{failures: map[string]error{
		first:  firstFailure,
		failed: secondFailure,
	}}

	result := deletePaths(targets, remover)

	if result.succeeded != 1 || !reflect.DeepEqual(result.failures, []error{firstFailure, secondFailure}) {
		t.Fatalf("partial deletion result = %#v", result)
	}
	if !samePathMultiset(remover.callsSnapshot(), targets) {
		t.Fatalf("attempted targets = %#v, want %#v", remover.callsSnapshot(), targets)
	}
	for _, target := range []string{first, failed} {
		if _, err := os.Stat(target); err != nil {
			t.Fatalf("failed target %s = %v, want retained", target, err)
		}
	}
	if _, err := os.Stat(last); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("successful target %s = %v, want missing", last, err)
	}
}

type pathRemoverFunc func(string) error

func (remover pathRemoverFunc) RemovePath(path string) error {
	return remover(path)
}

type recordingRemover struct {
	mu       sync.Mutex
	failures map[string]error
	calls    []string
}

func (remover *recordingRemover) RemovePath(path string) error {
	remover.mu.Lock()
	defer remover.mu.Unlock()
	remover.calls = append(remover.calls, path)
	if failure, found := remover.failures[path]; found {
		return failure
	}
	return os.RemoveAll(path)
}

func (remover *recordingRemover) callsSnapshot() []string {
	remover.mu.Lock()
	defer remover.mu.Unlock()
	return append([]string(nil), remover.calls...)
}

func samePathMultiset(first, second []string) bool {
	first = append([]string(nil), first...)
	second = append([]string(nil), second...)
	sort.Strings(first)
	sort.Strings(second)
	return reflect.DeepEqual(first, second)
}

func newDisposableRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("get user home: %v", err)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine repository root")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	for _, forbidden := range []string{workingDirectory, home, repositoryRoot} {
		if pathsOverlap(root, forbidden) {
			t.Fatalf("disposable root %s overlaps forbidden path %s", root, forbidden)
		}
	}
	return root
}

func writeDisposableFile(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("contents"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func assertContainedTargets(t *testing.T, root string, targets ...string) {
	t.Helper()
	for _, target := range targets {
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
			t.Fatalf("target %s is not contained by disposable root %s", target, root)
		}
	}
}

func pathsOverlap(first, second string) bool {
	return pathContains(first, second) || pathContains(second, first)
}

func pathContains(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative)
}
