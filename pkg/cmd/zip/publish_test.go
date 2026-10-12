package zip

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestZipPublicationFailuresPreserveDestination(t *testing.T) {
	for _, stage := range []string{"create", "write", "no progress", "chmod", "sync", "close", "replace"} {
		t.Run(stage, func(t *testing.T) {
			directory := t.TempDir()
			output := filepath.Join(directory, "archive.zip")
			old := []byte("original output")
			if err := os.WriteFile(output, old, 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Stat(output)
			if err != nil {
				t.Fatal(err)
			}
			failure := errors.New(stage + " failed")
			wantErr := failure
			if stage == "no progress" {
				wantErr = io.ErrShortWrite
			}
			var candidate string
			var staged *faultZipCandidate
			ops := defaultZipPublishOps()
			ops.create = func(dir, prefix string, mode os.FileMode) (string, zipPublishFile, error) {
				if dir != directory {
					t.Fatalf("candidate directory = %q, want %q", dir, directory)
				}
				if stage == "create" {
					return "", nil, failure
				}
				path, file, err := createZipCandidate(dir, prefix, mode)
				if err != nil {
					return "", nil, err
				}
				candidate = path
				staged = &faultZipCandidate{zipPublishFile: file, stage: stage, failure: failure}
				return path, staged, nil
			}
			ops.replace = func(path, target string) error {
				if stage != "replace" {
					t.Fatal("replacement attempted after candidate failure")
				}
				if !staged.closed {
					t.Fatal("replacement attempted before closing the candidate")
				}
				return failure
			}

			if err := publishZipFile(output, []byte("complete new output"), ops); !errors.Is(err, wantErr) {
				t.Fatalf("publication error = %v, want %v", err, wantErr)
			}
			if staged != nil && !staged.closed {
				t.Fatal("candidate was not closed")
			}
			if candidate != "" {
				if _, err := os.Stat(candidate); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("candidate remains after failure: %v", err)
				}
			}
			got, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(got, old) {
				t.Fatalf("original output = %q, %v; want %q", got, err, old)
			}
			after, err := os.Stat(output)
			if err != nil {
				t.Fatal(err)
			}
			if !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatal("failed publication changed the original file or its permissions")
			}
			assertZipPublicationDirectory(t, directory, "archive.zip")
		})
	}
}

func TestZipPublicationCompletesShortWritesBeforeReplacement(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "archive.zip")
	old := []byte("original output")
	if err := os.WriteFile(output, old, 0o600); err != nil {
		t.Fatal(err)
	}
	want := []byte("complete new output")
	var staged *faultZipCandidate
	ops := defaultZipPublishOps()
	ops.create = func(dir, prefix string, mode os.FileMode) (string, zipPublishFile, error) {
		path, file, err := createZipCandidate(dir, prefix, mode)
		if err != nil {
			return "", nil, err
		}
		staged = &faultZipCandidate{zipPublishFile: file, maxWrite: 2}
		return path, staged, nil
	}
	ops.replace = func(candidate, target string) error {
		got, err := os.ReadFile(target)
		if err != nil || !bytes.Equal(got, old) {
			t.Fatalf("destination changed before replacement: %q, %v", got, err)
		}
		got, err = os.ReadFile(candidate)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("candidate incomplete at replacement: %q, %v", got, err)
		}
		if !staged.synced || !staged.closed || staged.writes <= 1 {
			t.Fatalf("candidate not ready: synced=%t closed=%t writes=%d", staged.synced, staged.closed, staged.writes)
		}
		return replaceZipFile(candidate, target)
	}
	if err := publishZipFile(output, want, ops); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("published output = %q, %v", got, err)
	}
	assertZipPublicationDirectory(t, directory, "archive.zip")
}

func TestZipPublicationCreatesDestinationWithDefaultPermissions(t *testing.T) {
	for _, content := range [][]byte{[]byte("new output"), nil} {
		t.Run(string(content), func(t *testing.T) {
			directory := t.TempDir()
			output := filepath.Join(directory, "archive.zip")
			reference := filepath.Join(t.TempDir(), "reference")
			if err := os.WriteFile(reference, nil, 0o666); err != nil {
				t.Fatal(err)
			}
			if err := WriteZipFile(output, content); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(output)
			if err != nil || !bytes.Equal(got, content) {
				t.Fatalf("new output = %q, %v; want %q", got, err, content)
			}
			info, err := os.Stat(output)
			if err != nil {
				t.Fatal(err)
			}
			refInfo, err := os.Stat(reference)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != refInfo.Mode().Perm() {
				t.Fatalf("new permissions = %04o, want %04o", info.Mode().Perm(), refInfo.Mode().Perm())
			}
			assertZipPublicationDirectory(t, directory, "archive.zip")
		})
	}
}

func TestZipPublicationFailureDoesNotCreateDestination(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "archive.zip")
	failure := errors.New("partial write failed")
	ops := defaultZipPublishOps()
	ops.create = func(dir, prefix string, mode os.FileMode) (string, zipPublishFile, error) {
		path, file, err := createZipCandidate(dir, prefix, mode)
		if err != nil {
			return "", nil, err
		}
		return path, &faultZipCandidate{zipPublishFile: file, stage: "write", failure: failure}, nil
	}
	if err := publishZipFile(output, []byte("new output"), ops); !errors.Is(err, failure) {
		t.Fatalf("publication error = %v, want %v", err, failure)
	}
	assertZipPublicationDirectory(t, directory, "")
}

func assertZipPublicationDirectory(t *testing.T, directory, target string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if target == "" {
		if len(entries) != 0 {
			t.Fatalf("failed publication left files: %v", entries)
		}
	} else if len(entries) != 1 || entries[0].Name() != target {
		t.Fatalf("publication left candidate files: %v", entries)
	}
}

type faultZipCandidate struct {
	zipPublishFile
	stage    string
	failure  error
	maxWrite int
	writes   int
	synced   bool
	closed   bool
}

func (file *faultZipCandidate) Write(content []byte) (int, error) {
	file.writes++
	if file.stage == "no progress" {
		return 0, nil
	}
	if file.stage == "write" {
		written, err := file.zipPublishFile.Write(content[:1])
		return written, errors.Join(err, file.failure)
	}
	if file.maxWrite > 0 && len(content) > file.maxWrite {
		content = content[:file.maxWrite]
	}
	return file.zipPublishFile.Write(content)
}

func (file *faultZipCandidate) Chmod(mode os.FileMode) error {
	if file.stage == "chmod" {
		return file.failure
	}
	return file.zipPublishFile.Chmod(mode)
}

func (file *faultZipCandidate) Sync() error {
	if file.stage == "sync" {
		return file.failure
	}
	if err := file.zipPublishFile.Sync(); err != nil {
		return err
	}
	file.synced = true
	return nil
}

func (file *faultZipCandidate) Close() error {
	file.closed = true
	err := file.zipPublishFile.Close()
	if file.stage == "close" {
		return errors.Join(err, file.failure)
	}
	return err
}

func TestZipPublicationPreservesSymlinkToExistingFile(t *testing.T) {
	linkDirectory := t.TempDir()
	targetDirectory := t.TempDir()
	resolvedDirectory, err := filepath.EvalSymlinks(targetDirectory)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(targetDirectory, "archive.zip")
	if err := os.WriteFile(target, []byte("original output"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(linkDirectory, "archive.zip")
	if err := os.Symlink(target, output); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	ops := defaultZipPublishOps()
	create := ops.create
	ops.create = func(dir, prefix string, mode os.FileMode) (string, zipPublishFile, error) {
		if dir != resolvedDirectory {
			t.Fatalf("candidate directory = %q, want link target directory %q", dir, resolvedDirectory)
		}
		return create(dir, prefix, mode)
	}
	want := []byte("complete new output")
	if err := publishZipFile(output, want, ops); err != nil {
		t.Fatal(err)
	}
	resolved, err := os.Readlink(output)
	if err != nil || resolved != target {
		t.Fatalf("output link changed: %q, %v", resolved, err)
	}
	got, err := os.ReadFile(target)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("link target contents = %q, %v; want %q", got, err, want)
	}
	assertZipPublicationDirectory(t, linkDirectory, "archive.zip")
	assertZipPublicationDirectory(t, targetDirectory, "archive.zip")
}

func TestZipPublicationRejectsDirectoryAndDanglingLink(t *testing.T) {
	for _, kind := range []string{"directory", "dangling link"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			output := filepath.Join(directory, "archive.zip")
			if kind == "directory" {
				if err := os.Mkdir(output, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Symlink(filepath.Join(directory, "missing"), output); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			before, err := os.Lstat(output)
			if err != nil {
				t.Fatal(err)
			}
			ops := defaultZipPublishOps()
			ops.create = func(string, string, os.FileMode) (string, zipPublishFile, error) {
				t.Fatal("candidate created for a non-file destination")
				return "", nil, nil
			}
			if err := publishZipFile(output, []byte("new output"), ops); err == nil {
				t.Fatal("publication unexpectedly accepted a non-file destination")
			}
			after, err := os.Lstat(output)
			if err != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() {
				t.Fatalf("non-file destination changed: %v", err)
			}
			assertZipPublicationDirectory(t, directory, "archive.zip")
		})
	}
}
