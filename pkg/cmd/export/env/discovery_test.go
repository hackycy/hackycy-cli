package env

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiscoverRejectsDirectoryWithoutUsableEnvFiles(t *testing.T) {
	directory := t.TempDir()

	_, err := Discover(directory)

	if err == nil {
		t.Fatal("Discover returned nil error")
	}
	if want := fmt.Sprintf("No .env files found in %s", directory); err.Error() != want {
		t.Fatalf("Discover error = %q, want %q", err, want)
	}
}

func TestDiscoverListsSortedUsableDirectFiles(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{
		".env",
		".env.zebra",
		".env.local",
		".env.example.local",
		".env.example",
		".env.sample",
		".envrc",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(directory, ".env.directory"), 0o700); err != nil {
		t.Fatalf("make .env.directory: %v", err)
	}
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0o700); err != nil {
		t.Fatalf("make nested: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, "nested", ".env.nested"), nil, 0o600); err != nil {
		t.Fatalf("write nested .env: %v", err)
	}

	got, err := Discover(directory)

	if err != nil {
		t.Fatalf("Discover returned an error: %v", err)
	}
	want := Discovery{
		Directory:        directory,
		BaseFile:         ".env",
		EnvironmentFiles: []string{".env.example.local", ".env.local", ".env.zebra"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Discover() = %#v, want %#v", got, want)
	}
}

func TestDiscoverSupportsBaseOrEnvironmentFilesAlone(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		filename         string
		baseFile         string
		environmentFiles []string
	}{
		{
			name:             "base",
			filename:         ".env",
			baseFile:         ".env",
			environmentFiles: []string{},
		},
		{
			name:             "environment",
			filename:         ".env.production",
			environmentFiles: []string{".env.production"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, testCase.filename), nil, 0o600); err != nil {
				t.Fatalf("write %s: %v", testCase.filename, err)
			}

			got, err := Discover(directory)

			if err != nil {
				t.Fatalf("Discover returned an error: %v", err)
			}
			want := Discovery{
				Directory:        directory,
				BaseFile:         testCase.baseFile,
				EnvironmentFiles: testCase.environmentFiles,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Discover() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestDiscoverRejectsOnlyExcludedOrDirectoryEntries(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{".env.example", ".env.sample", ".envrc"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(directory, ".env.production"), 0o700); err != nil {
		t.Fatalf("make .env.production: %v", err)
	}

	_, err := Discover(directory)

	if err == nil {
		t.Fatal("Discover returned nil error")
	}
	if want := fmt.Sprintf("No .env files found in %s", directory); err.Error() != want {
		t.Fatalf("Discover error = %q, want %q", err, want)
	}
}
