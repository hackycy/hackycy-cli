package list

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestReadPreservesStoredOrderAndMarksOnlyTheDefaultProfile(t *testing.T) {
	const plaintext = "api-key-that-must-not-escape"
	const ciphertext = "MDEyMzQ1Njc4OWFiY2RlZg==:tag:ciphertext"
	reader := fakeReader{profiles: appconfig.CMProfileList{
		DefaultProfile: "personal",
		Profiles: []appconfig.CMProfile{
			{Name: "work", Model: "gpt-4.1-mini", BaseURL: "https://work.example/v1"},
			{Name: "personal", Model: "deepseek-chat", BaseURL: "https://personal.example/v1"},
		},
	}}

	profiles, err := Read(reader)
	if err != nil {
		t.Fatalf("Read() returned an error: %v", err)
	}
	want := []Profile{
		{Name: "work", Model: "gpt-4.1-mini", BaseURL: "https://work.example/v1"},
		{Name: "personal", Model: "deepseek-chat", BaseURL: "https://personal.example/v1", Default: true},
	}
	if !reflect.DeepEqual(profiles, want) {
		t.Fatalf("Read() = %#v, want %#v", profiles, want)
	}
	output := fmt.Sprintf("%#v", profiles)
	if strings.Contains(output, plaintext) || strings.Contains(output, ciphertext) {
		t.Fatalf("Read() exposed secret material: %q", output)
	}
}

func TestReadKeepsAnEmptyListEmpty(t *testing.T) {
	profiles, err := Read(fakeReader{})
	if err != nil {
		t.Fatalf("Read() returned an error: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("Read() = %#v, want no profiles", profiles)
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
	profiles appconfig.CMProfileList
	err      error
}

func (reader fakeReader) ListCMProfiles() (appconfig.CMProfileList, error) {
	return reader.profiles, reader.err
}
