package env

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteOutputResolvesTargetFromWorkingDirectory(t *testing.T) {
	writer := &recordingWriter{}

	err := WriteOutput("/working-directory", "nested/output.json", "{\"VALUE\":\"value\"}", writer)

	if err != nil {
		t.Fatalf("WriteOutput returned an error: %v", err)
	}
	if writer.path != filepath.Join("/working-directory", "nested", "output.json") {
		t.Fatalf("writer path = %q", writer.path)
	}
	if writer.content != "{\"VALUE\":\"value\"}" {
		t.Fatalf("writer content = %q", writer.content)
	}
}

func TestWriteOutputKeepsAbsoluteTarget(t *testing.T) {
	writer := &recordingWriter{}
	target := filepath.Join(t.TempDir(), "outside", "output.json")

	err := WriteOutput("/working-directory", target, "{}", writer)

	if err != nil {
		t.Fatalf("WriteOutput returned an error: %v", err)
	}
	if writer.path != filepath.Clean(target) {
		t.Fatalf("writer path = %q", writer.path)
	}
}

func TestWriteOutputReturnsWriterFailure(t *testing.T) {
	want := errors.New("write failure")

	err := WriteOutput("/working-directory", "output.json", "{}", failingWriter{err: want})

	if !errors.Is(err, want) {
		t.Fatalf("WriteOutput error = %v, want %v", err, want)
	}
}

func TestOSExportEnvWriterPublishesExistingDestinationAndPreservesMode(t *testing.T) {
	output := filepath.Join(t.TempDir(), "output.json")
	old := []byte("obsolete")
	if err := os.WriteFile(output, old, 0o600); err != nil {
		t.Fatalf("write old output: %v", err)
	}
	var oldReader *os.File
	if runtime.GOOS != "windows" {
		var err error
		oldReader, err = os.Open(output)
		if err != nil {
			t.Fatal(err)
		}
		defer oldReader.Close()
		if err := os.Chmod(output, 0o660); err != nil {
			t.Fatalf("Chmod(): %v", err)
		}
	}
	want := []byte(`{"VALUE":"value"}`)
	if err := (osExportEnvWriter{}).WriteFile(output, want); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if runtime.GOOS != "windows" {
		oldContents, err := io.ReadAll(oldReader)
		if err != nil || !bytes.Equal(oldContents, old) {
			t.Fatalf("publication changed an open reader's original output: %q, %v", oldContents, err)
		}
		info, err := os.Stat(output)
		if err != nil {
			t.Fatalf("Stat() = %v", err)
		}
		if got, want := info.Mode().Perm(), os.FileMode(0o660); got != want {
			t.Fatalf("published mode = %04o, want %04o", got, want)
		}
	}
}

type recordingWriter struct {
	path    string
	content string
}

func (writer *recordingWriter) WriteFile(path string, content []byte) error {
	writer.path = path
	writer.content = string(content)
	return nil
}

type failingWriter struct {
	err error
}

func (writer failingWriter) WriteFile(string, []byte) error {
	return writer.err
}
