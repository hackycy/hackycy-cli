package env

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReadReadsSelectedFilesInOrder(t *testing.T) {
	reader := &recordingReader{contents: map[string][]byte{
		filepath.Join("/project", ".env"):            []byte("BASE=base\n"),
		filepath.Join("/project", ".env.production"): []byte("PROD=production\n"),
	}}

	got, err := Read(
		Discovery{Directory: "/project"},
		Selection{Files: []string{".env", ".env.production"}},
		reader,
	)

	if err != nil {
		t.Fatalf("Read returned an error: %v", err)
	}
	if want := []string{"BASE=base\n", "PROD=production\n"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Read() = %#v, want %#v", got, want)
	}
	if want := []string{filepath.Join("/project", ".env"), filepath.Join("/project", ".env.production")}; !reflect.DeepEqual(reader.paths, want) {
		t.Fatalf("reader paths = %#v, want %#v", reader.paths, want)
	}
}

func TestReadReturnsReaderFailure(t *testing.T) {
	want := errors.New("read failure")

	_, err := Read(
		Discovery{Directory: "/project"},
		Selection{Files: []string{".env"}},
		failingReader{err: want},
	)

	if !errors.Is(err, want) {
		t.Fatalf("Read error = %v, want %v", err, want)
	}
}

type recordingReader struct {
	contents map[string][]byte
	paths    []string
}

func (reader *recordingReader) ReadFile(path string) ([]byte, error) {
	reader.paths = append(reader.paths, path)
	return reader.contents[path], nil
}

type failingReader struct {
	err error
}

func (reader failingReader) ReadFile(string) ([]byte, error) {
	return nil, reader.err
}
