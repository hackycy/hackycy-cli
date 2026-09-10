package list

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestReadPreservesAppconfigOrderAndSafeFields(t *testing.T) {
	const plaintext = "token-that-must-not-escape"
	const ciphertext = "MDEyMzQ1Njc4OWFiY2RlZg==:tag:ciphertext"
	reader := fakeReader{instances: []appconfig.ForkInstance{
		{
			Name:         "work",
			Host:         "gitlab.example",
			Scheme:       "https",
			Type:         "gitlab",
			TokenPreview: "MDEy***",
		},
		{
			Name:         "personal",
			Host:         "github.example",
			Scheme:       "http",
			Type:         "github",
			TokenPreview: "QWVy***",
		},
	}}

	instances, err := Read(reader)
	if err != nil {
		t.Fatalf("Read() returned an error: %v", err)
	}
	want := []Instance{
		{Name: "work", Host: "gitlab.example", Scheme: "https", Type: "gitlab", TokenPreview: "MDEy***"},
		{Name: "personal", Host: "github.example", Scheme: "http", Type: "github", TokenPreview: "QWVy***"},
	}
	if !reflect.DeepEqual(instances, want) {
		t.Fatalf("Read() = %#v, want %#v", instances, want)
	}
	output := fmt.Sprintf("%#v", instances)
	if strings.Contains(output, plaintext) || strings.Contains(output, ciphertext) {
		t.Fatalf("Read() exposed a secret: %q", output)
	}
}

func TestReadKeepsAnEmptyListEmpty(t *testing.T) {
	instances, err := Read(fakeReader{})
	if err != nil {
		t.Fatalf("Read() returned an error: %v", err)
	}
	if len(instances) != 0 {
		t.Fatalf("Read() = %#v, want no instances", instances)
	}
}

func TestReadReturnsAppconfigFailure(t *testing.T) {
	want := errors.New("read configuration")
	_, err := Read(fakeReader{err: want})
	if !errors.Is(err, want) {
		t.Fatalf("Read() error = %v, want %v", err, want)
	}
}

type fakeReader struct {
	instances []appconfig.ForkInstance
	err       error
}

func (reader fakeReader) ListForkInstances() ([]appconfig.ForkInstance, error) {
	return reader.instances, reader.err
}
