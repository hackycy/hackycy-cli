package pulse

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"
)

func TestScanRepositoriesFindsDirectAndNestedGitDirectories(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	deeplyNested := filepath.Join(root, "outer", "inner")
	for _, repository := range []string{root, nested, deeplyNested} {
		makePulseDirectory(t, filepath.Join(repository, ".git"))
	}
	makePulseDirectory(t, filepath.Join(root, "not-a-repository"))
	if err := os.WriteFile(filepath.Join(root, "not-a-repository", ".git"), []byte("gitdir"), 0o600); err != nil {
		t.Fatalf("write .git file: %v", err)
	}

	var found []string
	repositories, err := ScanRepositories(context.Background(), root, testPulseDirectoryReader{}, func(path string) {
		found = append(found, path)
	}, nil)
	if err != nil {
		t.Fatalf("ScanRepositories() error = %v", err)
	}

	want := []string{root, nested, deeplyNested}
	if !samePulsePaths(repositories, want) {
		t.Fatalf("repositories = %q, want %q", repositories, want)
	}
	if !samePulsePaths(found, want) {
		t.Fatalf("onFound paths = %q, want %q", found, want)
	}
}

func TestScanRepositoriesSkipsExcludedDirectoriesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	for excluded := range excludedDirectoryNames {
		makePulseDirectory(t, filepath.Join(root, excluded, "ignored", ".git"))
	}
	visible := filepath.Join(root, "projects", "visible")
	makePulseDirectory(t, filepath.Join(visible, ".git"))

	outside := t.TempDir()
	makePulseDirectory(t, filepath.Join(outside, ".git"))
	link := filepath.Join(root, "linked-repository")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("create directory symlink: %v", err)
	}

	repositories, err := ScanRepositories(context.Background(), root, testPulseDirectoryReader{}, nil, nil)
	if err != nil {
		t.Fatalf("ScanRepositories() error = %v", err)
	}
	if !reflect.DeepEqual(repositories, []string{visible}) {
		t.Fatalf("repositories = %q, want only %q", repositories, visible)
	}
}

func TestScanRepositoriesIgnoresABareRepositoryLayout(t *testing.T) {
	root := t.TempDir()
	bare := filepath.Join(root, "bare.git")
	command := exec.Command("git", "init", "--bare", "-q", bare)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize bare repository: %v\n%s", err, output)
	}

	repositories, err := ScanRepositories(context.Background(), root, testPulseDirectoryReader{}, nil, nil)
	if err != nil {
		t.Fatalf("ScanRepositories() error = %v", err)
	}
	if len(repositories) != 0 {
		t.Fatalf("repositories = %q, want bare repository ignored", repositories)
	}
}

func TestScanRepositoriesSkipsUnreadableDirectoriesAndContinues(t *testing.T) {
	root := t.TempDir()
	unreadable := filepath.Join(root, "unreadable")
	visible := filepath.Join(root, "visible")
	makePulseDirectory(t, filepath.Join(unreadable, ".git"))
	makePulseDirectory(t, filepath.Join(visible, ".git"))

	reader := directoryReaderFunc(func(path string) ([]os.DirEntry, error) {
		if path == unreadable {
			return nil, errors.New("permission denied")
		}
		return os.ReadDir(path)
	})
	repositories, err := ScanRepositories(context.Background(), root, reader, nil, nil)
	if err != nil {
		t.Fatalf("ScanRepositories() error = %v", err)
	}
	if !reflect.DeepEqual(repositories, []string{visible}) {
		t.Fatalf("repositories = %q, want only %q", repositories, visible)
	}
}

func TestScanRepositoryDetailsRetainsUnreadableChildPathsWithoutChangingDiscovery(t *testing.T) {
	root := t.TempDir()
	unreadable := filepath.Join(root, "unreadable")
	visible := filepath.Join(root, "visible")
	makePulseDirectory(t, filepath.Join(unreadable, ".git"))
	makePulseDirectory(t, filepath.Join(visible, ".git"))

	reader := directoryReaderFunc(func(path string) ([]os.DirEntry, error) {
		if path == unreadable {
			return nil, errors.New("permission denied")
		}
		return os.ReadDir(path)
	})
	result, err := ScanRepositoryDetails(context.Background(), root, reader, nil, nil)
	if err != nil {
		t.Fatalf("ScanRepositoryDetails() error = %v", err)
	}
	if !reflect.DeepEqual(result.Repositories, []string{visible}) || !reflect.DeepEqual(result.UnreadableDirectories, []string{unreadable}) {
		t.Fatalf("details = %#v", result)
	}
}

func TestScanRepositoriesYieldsAfterEachFullScanBatch(t *testing.T) {
	root := t.TempDir()
	for index := range scanYieldEvery {
		makePulseDirectory(t, filepath.Join(root, "directory-"+strconv.Itoa(index)))
	}

	yields := 0
	repositories, err := ScanRepositories(context.Background(), root, testPulseDirectoryReader{}, nil, func() {
		yields++
	})
	if err != nil {
		t.Fatalf("ScanRepositories() error = %v", err)
	}
	if len(repositories) != 0 {
		t.Fatalf("repositories = %q, want none", repositories)
	}
	if yields != 1 {
		t.Fatalf("yield calls = %d, want 1", yields)
	}
}

func TestScanRepositoriesStopsBetweenDirectoriesAfterCancellation(t *testing.T) {
	root := t.TempDir()
	makePulseDirectory(t, filepath.Join(root, "child"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := directoryReaderFunc(func(path string) ([]os.DirEntry, error) {
		entries, err := os.ReadDir(path)
		if path == root {
			cancel()
		}
		return entries, err
	})

	_, err := ScanRepositories(ctx, root, reader, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ScanRepositories() error = %v, want context cancellation", err)
	}
}

func makePulseDirectory(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create directory %q: %v", directory, err)
	}
}

func samePulsePaths(got, want []string) bool {
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	return reflect.DeepEqual(got, want)
}

type testPulseDirectoryReader struct{}

func (testPulseDirectoryReader) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

type directoryReaderFunc func(string) ([]os.DirEntry, error)

func (function directoryReaderFunc) ReadDir(path string) ([]os.DirEntry, error) {
	return function(path)
}
