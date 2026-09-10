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

func TestSaveAddMapsTheValidatedInputToAppconfig(t *testing.T) {
	store := &recordingAddStore{}
	input := AddInput{
		Alias:  "work",
		Host:   "https://gitlab.example/path",
		Type:   "gitlab",
		Scheme: "http",
		Token:  " token ",
	}

	if err := SaveAdd(store, input); err != nil {
		t.Fatalf("SaveAdd() returned an error: %v", err)
	}
	if got, want := store.names, []string{"work"}; !sameAddStrings(got, want) {
		t.Fatalf("save names = %#v, want %#v", got, want)
	}
	if got, want := store.inputs, []appconfig.ForkInput{{Host: "https://gitlab.example/path", Scheme: "http", Type: "gitlab", Token: " token "}}; !sameForkInputs(got, want) {
		t.Fatalf("save inputs = %#v, want %#v", got, want)
	}
}

func TestSaveAddRejectsInvalidInputBeforeTheStore(t *testing.T) {
	store := &recordingAddStore{}
	err := SaveAdd(store, AddInput{Alias: "two words", Host: "gitlab.example", Type: "gitlab", Scheme: "https", Token: "token"})
	if err == nil || err.Error() != "Name cannot contain spaces" {
		t.Fatalf("SaveAdd() error = %v", err)
	}
	if len(store.names) != 0 || len(store.inputs) != 0 {
		t.Fatalf("SaveAdd() called the store: %#v %#v", store.names, store.inputs)
	}
}

func TestSaveAddReturnsTheStoreFailure(t *testing.T) {
	failure := errors.New("write configuration")
	store := &recordingAddStore{err: failure}
	err := SaveAdd(store, AddInput{Alias: "work", Host: "gitlab.example", Type: "gitlab", Scheme: "https", Token: "token"})
	if !errors.Is(err, failure) {
		t.Fatalf("SaveAdd() error = %v, want %v", err, failure)
	}
}

func TestSaveAddSilentlyOverwritesThroughEncryptedAppconfig(t *testing.T) {
	store, home := newAddStore(t)
	if err := SaveAdd(store, AddInput{Alias: "work", Host: "https://gitlab.example/path", Type: "gitlab", Scheme: "https", Token: "first-token"}); err != nil {
		t.Fatalf("SaveAdd(first) returned an error: %v", err)
	}
	if err := SaveAdd(store, AddInput{Alias: "work", Host: "replacement.example", Type: "github", Scheme: "http", Token: "replacement-token"}); err != nil {
		t.Fatalf("SaveAdd(replacement) returned an error: %v", err)
	}

	instances, err := store.ListForkInstances()
	if err != nil {
		t.Fatalf("ListForkInstances() returned an error: %v", err)
	}
	if len(instances) != 1 || instances[0].Name != "work" || instances[0].Host != "replacement.example" || instances[0].Scheme != "http" || instances[0].Type != "github" {
		t.Fatalf("ListForkInstances() = %#v", instances)
	}
	credentials, found, err := store.ForkInstance("work")
	if err != nil || !found || credentials.Token != "replacement-token" {
		t.Fatalf("ForkInstance() = (%#v, %t, %v)", credentials, found, err)
	}

	contents, err := os.ReadFile(filepath.Join(home, ".ycy-cli", "config.json"))
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	if strings.Contains(string(contents), "first-token") || strings.Contains(string(contents), "replacement-token") {
		t.Fatalf("persisted config exposed a plaintext token: %q", contents)
	}
	instance := persistedAddFork(t, contents, "work")
	if got, want := instance["host"], "replacement.example"; got != want {
		t.Fatalf("persisted host = %#v, want %#v", got, want)
	}
	if got, want := instance["scheme"], "http"; got != want {
		t.Fatalf("persisted scheme = %#v, want %#v", got, want)
	}
	if got, want := instance["type"], "github"; got != want {
		t.Fatalf("persisted type = %#v, want %#v", got, want)
	}
	ciphertext, _ := instance["token"].(string)
	if ciphertext == "" || ciphertext == "replacement-token" || strings.Count(ciphertext, ":") != 2 {
		t.Fatalf("persisted token = %q, want encrypted legacy-compatible shape", ciphertext)
	}
}

func TestSaveAddPreservesConcurrentUnrelatedConfigUpdates(t *testing.T) {
	store, home := newAddStore(t)
	start := make(chan struct{})
	errors := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		errors <- SaveAdd(store, AddInput{Alias: "work", Host: "gitlab.example", Type: "gitlab", Scheme: "https", Token: "fork-token"})
	}()
	go func() {
		defer group.Done()
		<-start
		errors <- store.AddCMProfile("work", "https://provider.example", "model", "api-key")
	}()
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent update returned an error: %v", err)
		}
	}

	instances, err := store.ListForkInstances()
	if err != nil || len(instances) != 1 || instances[0].Name != "work" {
		t.Fatalf("ListForkInstances() = (%#v, %v)", instances, err)
	}
	profiles, err := store.ListCMProfiles()
	if err != nil || len(profiles.Profiles) != 1 || profiles.Profiles[0].Name != "work" {
		t.Fatalf("ListCMProfiles() = (%#v, %v)", profiles, err)
	}
	contents, err := os.ReadFile(filepath.Join(home, ".ycy-cli", "config.json"))
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	for _, plaintext := range []string{"fork-token", "api-key"} {
		if strings.Contains(string(contents), plaintext) {
			t.Fatalf("persisted config exposed %q", plaintext)
		}
	}
}

type recordingAddStore struct {
	names  []string
	inputs []appconfig.ForkInput
	err    error
}

func (store *recordingAddStore) SaveForkInstance(name string, input appconfig.ForkInput) error {
	store.names = append(store.names, name)
	store.inputs = append(store.inputs, input)
	return store.err
}

func newAddStore(t *testing.T) (*appconfig.Store, string) {
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

func persistedAddFork(t *testing.T, contents []byte, name string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("decode persisted config: %v", err)
	}
	fork, _ := document["fork"].(map[string]any)
	instances, _ := fork["instances"].(map[string]any)
	instance, _ := instances[name].(map[string]any)
	if instance == nil {
		t.Fatalf("persisted config omitted %q: %#v", name, document)
	}
	return instance
}

func sameAddStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func sameForkInputs(got, want []appconfig.ForkInput) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
