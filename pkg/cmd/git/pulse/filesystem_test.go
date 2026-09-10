package pulse

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePulseRootResolvesDefaultRelativeAndAbsoluteDirectories(t *testing.T) {
	root := t.TempDir()
	relative := filepath.Join(root, "relative")
	absolute := filepath.Join(root, "absolute")
	makePulseDirectory(t, relative)
	makePulseDirectory(t, absolute)

	testCases := []struct {
		name      string
		directory string
		want      string
	}{
		{name: "default", want: root},
		{name: "relative", directory: "relative", want: relative},
		{name: "absolute", directory: absolute, want: absolute},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := resolvePulseRoot(root, testCase.directory, osPulseStater{})
			if err != nil {
				t.Fatalf("resolvePulseRoot() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("root = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestResolvePulseRootMapsMissingAndFileTargetsToLegacyErrors(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("file"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	_, err := resolvePulseRoot(root, "missing", osPulseStater{})
	if got, want := err.Error(), "Directory not found: "+filepath.Join(root, "missing"); got != want {
		t.Fatalf("missing error = %q, want %q", got, want)
	}
	_, err = resolvePulseRoot(root, "file", osPulseStater{})
	if got, want := err.Error(), "Path is not a directory: "+file; got != want {
		t.Fatalf("file error = %q, want %q", got, want)
	}
	_, err = resolvePulseRoot(root, "unreadable", pathStaterFunc(func(string) (fs.FileInfo, error) {
		return nil, errors.New("permission denied")
	}))
	if got, want := err.Error(), "Directory not found: "+filepath.Join(root, "unreadable"); got != want {
		t.Fatalf("read failure error = %q, want %q", got, want)
	}
}

type osPulseStater struct{}

func (osPulseStater) Stat(path string) (fs.FileInfo, error) {
	return os.Stat(path)
}

type pathStaterFunc func(string) (fs.FileInfo, error)

func (function pathStaterFunc) Stat(path string) (fs.FileInfo, error) {
	return function(path)
}
