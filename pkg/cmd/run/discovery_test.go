package run

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverProjectResolvesDefaultRelativeAndAbsoluteProjectPaths(t *testing.T) {
	root := t.TempDir()
	relativeProject := filepath.Join(root, "projects", "relative")
	absProject := filepath.Join(root, "absolute")
	for _, directory := range []string{root, relativeProject, absProject} {
		writeRunPackage(t, directory, `{"scripts":{"check":"go test ./..."}}`)
	}

	testCases := []struct {
		name        string
		projectPath string
		want        string
	}{
		{name: "default", want: root},
		{name: "relative", projectPath: filepath.Join("projects", "relative"), want: relativeProject},
		{name: "absolute", projectPath: absProject, want: absProject},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			discovery, err := DiscoverProject(root, testCase.projectPath, fileReaderFunc(os.ReadFile))
			if err != nil {
				t.Fatalf("DiscoverProject() error = %v", err)
			}
			if discovery.Directory != testCase.want {
				t.Fatalf("directory = %q, want %q", discovery.Directory, testCase.want)
			}
		})
	}
}

func TestDiscoverProjectFiltersScriptsAndPreservesDeclarationOrder(t *testing.T) {
	root := t.TempDir()
	writeRunPackage(t, root, `{
  "scripts": {
    "first": "go test ./...",
    "skip-null": null,
    "skip-empty": "  ",
    "second": "pnpm lint",
    "skip-array": ["not", "a", "command"],
    "third": "npm run build"
  }
}`)

	discovery, err := DiscoverProject(root, "", fileReaderFunc(os.ReadFile))
	if err != nil {
		t.Fatalf("DiscoverProject() error = %v", err)
	}
	want := []Script{
		{Name: "first", Command: "go test ./..."},
		{Name: "second", Command: "pnpm lint"},
		{Name: "third", Command: "npm run build"},
	}
	if !reflect.DeepEqual(discovery.Scripts, want) {
		t.Fatalf("scripts = %#v, want %#v", discovery.Scripts, want)
	}
}

func TestDiscoverProjectMapsLegacyPackageErrors(t *testing.T) {
	testCases := []struct {
		name     string
		contents string
		want     error
	}{
		{name: "malformed package", contents: `{`, want: errPackageParse},
		{name: "nonobject package", contents: `[]`, want: errNoScripts},
		{name: "missing scripts", contents: `{}`, want: errNoScripts},
		{name: "array scripts", contents: `{"scripts":[]}`, want: errNoScripts},
		{name: "no runnable scripts", contents: `{"scripts":{"empty":" ","number":1}}`, want: errNoRunnable},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			writeRunPackage(t, root, testCase.contents)
			_, err := DiscoverProject(root, "", fileReaderFunc(os.ReadFile))
			if !errors.Is(err, testCase.want) || err.Error() != testCase.want.Error() {
				t.Fatalf("DiscoverProject() error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestDiscoverProjectUsesTheLegacyMissingPackageMessageForExplicitPaths(t *testing.T) {
	root := t.TempDir()
	_, err := DiscoverProject(root, "missing-project", fileReaderFunc(os.ReadFile))
	if !errors.Is(err, errNoPackage) || err.Error() != "No package.json found in current directory." {
		t.Fatalf("DiscoverProject() error = %v", err)
	}
}

func TestDiscoverProjectMapsNonMissingPackageReadFailuresToParseFailure(t *testing.T) {
	failure := errors.New("read package")
	_, err := DiscoverProject(t.TempDir(), "", fileReaderFunc(func(string) ([]byte, error) {
		return nil, failure
	}))
	if !errors.Is(err, errPackageParse) || err.Error() != "Failed to parse package.json." {
		t.Fatalf("DiscoverProject() error = %v", err)
	}
}

func writeRunPackage(t *testing.T, directory, contents string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create package directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "package.json"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
}

type fileReaderFunc func(string) ([]byte, error)

func (function fileReaderFunc) ReadFile(path string) ([]byte, error) {
	return function(path)
}
