package add

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestSaveAddMapsValidatedInputToAppconfig(t *testing.T) {
	store := &recordingCMAddStore{}
	input := AddInput{
		Name:    "work",
		BaseURL: " https://provider.example/v1/// ",
		Model:   " gpt-4.1-mini ",
		APIKey:  " api-key ",
	}

	if err := SaveAdd(store, input); err != nil {
		t.Fatalf("SaveAdd() returned an error: %v", err)
	}
	if got, want := store.inputs, []AddInput{input}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("store inputs = %#v, want %#v", got, want)
	}
}

func TestSaveAddRejectsInvalidInputBeforeTheStore(t *testing.T) {
	store := &recordingCMAddStore{}
	err := SaveAdd(store, AddInput{Name: "two words", BaseURL: "https://provider.example", Model: "model", APIKey: "key"})
	if err == nil || err.Error() != "Name cannot contain spaces" {
		t.Fatalf("SaveAdd() error = %v", err)
	}
	if len(store.inputs) != 0 {
		t.Fatalf("SaveAdd() called the store: %#v", store.inputs)
	}
}

func TestSaveAddReturnsTheStoreFailure(t *testing.T) {
	failure := errors.New("write configuration")
	store := &recordingCMAddStore{err: failure}
	err := SaveAdd(store, AddInput{Name: "work", BaseURL: "https://provider.example", Model: "model", APIKey: "key"})
	if !errors.Is(err, failure) {
		t.Fatalf("SaveAdd() error = %v, want %v", err, failure)
	}
}

func TestSaveAddNormalizesEncryptsSetsTheFirstDefaultAndSilentlyOverwrites(t *testing.T) {
	store, home := newCMAddStore(t)
	if err := SaveAdd(store, AddInput{Name: "primary", BaseURL: " https://primary.example/v1/// ", Model: " primary-model ", APIKey: "primary-key"}); err != nil {
		t.Fatalf("SaveAdd(primary) returned an error: %v", err)
	}
	if err := SaveAdd(store, AddInput{Name: "work", BaseURL: "https://work.example/v1", Model: "work-model", APIKey: "first-work-key"}); err != nil {
		t.Fatalf("SaveAdd(first work) returned an error: %v", err)
	}
	if err := SaveAdd(store, AddInput{Name: "work", BaseURL: " https://replacement.example/v1/// ", Model: " replacement-model ", APIKey: "replacement-work-key"}); err != nil {
		t.Fatalf("SaveAdd(replacement work) returned an error: %v", err)
	}

	profiles, err := store.ListCMProfiles()
	if err != nil {
		t.Fatalf("ListCMProfiles() returned an error: %v", err)
	}
	if profiles.DefaultProfile != "primary" || len(profiles.Profiles) != 2 || profiles.Profiles[0].Name != "primary" || profiles.Profiles[1] != (appconfig.CMProfile{Name: "work", BaseURL: "https://replacement.example/v1", Model: "replacement-model"}) {
		t.Fatalf("ListCMProfiles() = %#v", profiles)
	}

	resolved, err := store.ResolveCMProfile(appconfig.CMResolveOptions{ProfileName: "work"})
	if err != nil {
		t.Fatalf("ResolveCMProfile() returned an error: %v", err)
	}
	if resolved.BaseURL != "https://replacement.example/v1" || resolved.Model != "replacement-model" || resolved.APIKey != "replacement-work-key" {
		t.Fatalf("ResolveCMProfile() = %#v", resolved)
	}

	contents, err := os.ReadFile(filepath.Join(home, ".ycy-cli", "config.json"))
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	for _, plaintext := range []string{"primary-key", "first-work-key", "replacement-work-key"} {
		if strings.Contains(string(contents), plaintext) {
			t.Fatalf("persisted config exposed plaintext %q: %q", plaintext, contents)
		}
	}
	persisted := persistedCMAddProfile(t, contents, "work")
	if got, want := persisted["baseURL"], "https://replacement.example/v1"; got != want {
		t.Fatalf("persisted base URL = %#v, want %#v", got, want)
	}
	if got, want := persisted["model"], "replacement-model"; got != want {
		t.Fatalf("persisted model = %#v, want %#v", got, want)
	}
	ciphertext, _ := persisted["apiKey"].(string)
	if ciphertext == "" || ciphertext == "replacement-work-key" || strings.Count(ciphertext, ":") != 2 {
		t.Fatalf("persisted API key = %q, want encrypted legacy-compatible shape", ciphertext)
	}
}

func TestSaveAddPreservesConcurrentUnrelatedForkUpdates(t *testing.T) {
	store, home := newCMAddStore(t)
	start := make(chan struct{})
	errors := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		errors <- SaveAdd(store, AddInput{Name: "work", BaseURL: "https://provider.example", Model: "model", APIKey: "api-key"})
	}()
	go func() {
		defer group.Done()
		<-start
		errors <- store.SaveForkInstance("github", appconfig.ForkInput{Host: "github.com", Scheme: "https", Type: "github", Token: "fork-token"})
	}()
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent update returned an error: %v", err)
		}
	}

	profiles, err := store.ListCMProfiles()
	if err != nil || len(profiles.Profiles) != 1 || profiles.Profiles[0].Name != "work" {
		t.Fatalf("ListCMProfiles() = (%#v, %v)", profiles, err)
	}
	instances, err := store.ListForkInstances()
	if err != nil || len(instances) != 1 || instances[0].Name != "github" {
		t.Fatalf("ListForkInstances() = (%#v, %v)", instances, err)
	}
	contents, err := os.ReadFile(filepath.Join(home, ".ycy-cli", "config.json"))
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	for _, plaintext := range []string{"api-key", "fork-token"} {
		if strings.Contains(string(contents), plaintext) {
			t.Fatalf("persisted config exposed plaintext %q: %q", plaintext, contents)
		}
	}
}

type recordingCMAddStore struct {
	inputs []AddInput
	err    error
}

func (store *recordingCMAddStore) AddCMProfile(name, baseURL, model, apiKey string) error {
	store.inputs = append(store.inputs, AddInput{Name: name, BaseURL: baseURL, Model: model, APIKey: apiKey})
	return store.err
}

func newCMAddStore(t *testing.T) (*appconfig.Store, string) {
	t.Helper()
	home := t.TempDir()
	store, err := appconfig.New(appconfig.Dependencies{
		Environment: func(key string) string {
			if key == "HOME" {
				return home
			}
			return ""
		},
		MachineID: func() (string, error) { return "test-machine-id", nil },
		Username:  func() (string, error) { return "test-user", nil },
	})
	if err != nil {
		t.Fatalf("appconfig.New() returned an error: %v", err)
	}
	return store, home
}

func persistedCMAddProfile(t *testing.T, contents []byte, name string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("decode persisted config: %v", err)
	}
	cm, _ := document["cm"].(map[string]any)
	profiles, _ := cm["profiles"].(map[string]any)
	profile, _ := profiles[name].(map[string]any)
	if profile == nil {
		t.Fatalf("persisted config omitted %q: %#v", name, document)
	}
	return profile
}
