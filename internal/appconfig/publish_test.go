package appconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteDocumentPublishesCandidateAtomicallyAndRequestsPrivateModes(t *testing.T) {
	store := lockTestStore(t)
	if err := os.MkdirAll(filepath.Dir(store.configPath()), 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	if err := os.WriteFile(store.configPath(), []byte("old contents"), 0o600); err != nil {
		t.Fatalf("write original config: %v", err)
	}

	if err := store.writeDocument(testDocument()); err != nil {
		t.Fatalf("writeDocument() returned an error: %v", err)
	}
	contents, err := os.ReadFile(store.configPath())
	if err != nil {
		t.Fatalf("read published config: %v", err)
	}
	if !strings.HasSuffix(string(contents), "\n") {
		t.Fatal("published config has no trailing newline")
	}
	var raw map[string]any
	if err := json.Unmarshal(contents, &raw); err != nil {
		t.Fatalf("parse published config: %v", err)
	}
	if raw["salt"] != "c2FsdA==" {
		t.Fatalf("published salt = %#v", raw["salt"])
	}
	if _, exists := raw["unknown"]; exists {
		t.Fatal("published config retained an unknown field")
	}
	if candidates, err := filepath.Glob(store.configPath() + ".candidate-*"); err != nil || len(candidates) != 0 {
		t.Fatalf("candidate files = %v, glob error = %v", candidates, err)
	}
	if _, err := os.Stat(store.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("config lock remained after publication: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(store.configPath())
		if err != nil {
			t.Fatalf("stat published config: %v", err)
		}
		if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
			t.Fatalf("published config mode = %04o, want %04o", got, want)
		}
		directory, err := os.Stat(filepath.Dir(store.configPath()))
		if err != nil {
			t.Fatalf("stat config directory: %v", err)
		}
		if got, want := directory.Mode().Perm(), os.FileMode(0o700); got != want {
			t.Fatalf("config directory mode = %04o, want %04o", got, want)
		}
	}
}

func TestWriteDocumentCleansCandidateAndPreservesTargetAfterReplacementFailure(t *testing.T) {
	store := lockTestStore(t)
	if err := os.MkdirAll(filepath.Dir(store.configPath()), 0o700); err != nil {
		t.Fatalf("create config directory: %v", err)
	}
	const original = "original configuration\n"
	if err := os.WriteFile(store.configPath(), []byte(original), 0o600); err != nil {
		t.Fatalf("write original config: %v", err)
	}
	store.replaceConfigFile = func(string, string) error { return errors.New("replace failed") }

	err := store.writeDocument(testDocument())
	if err == nil || !strings.Contains(err.Error(), "replace") {
		t.Fatalf("writeDocument() error = %v", err)
	}
	contents, readErr := os.ReadFile(store.configPath())
	if readErr != nil {
		t.Fatalf("read original config: %v", readErr)
	}
	if string(contents) != original {
		t.Fatalf("target contents = %q, want %q", contents, original)
	}
	if candidates, globErr := filepath.Glob(store.configPath() + ".candidate-*"); globErr != nil || len(candidates) != 0 {
		t.Fatalf("candidate files = %v, glob error = %v", candidates, globErr)
	}
	if _, err := os.Stat(store.lockPath()); !os.IsNotExist(err) {
		t.Fatalf("config lock remained after publication failure: %v", err)
	}
}

func TestWriteDocumentDropsUnknownFieldsOnTheNextWrite(t *testing.T) {
	store := lockTestStore(t)
	writeConfigFixture(t, store, `{
  "salt": "c2FsdA==",
  "fork": {"instances": {}},
  "unknown": {"preserve": false}
}`)

	document, exists, err := store.readDocument()
	if err != nil || !exists {
		t.Fatalf("readDocument() = (%#v, %t, %v)", document, exists, err)
	}
	if err := store.writeDocument(document); err != nil {
		t.Fatalf("writeDocument() returned an error: %v", err)
	}
	contents, err := os.ReadFile(store.configPath())
	if err != nil {
		t.Fatalf("read published config: %v", err)
	}
	if strings.Contains(string(contents), "unknown") {
		t.Fatalf("published config retained unknown data: %q", contents)
	}
}

func testDocument() document {
	return document{
		Salt: "c2FsdA==",
		Fork: forkDocument{Instances: map[string]forkDocumentInstance{
			"github": {Host: "github.com", Scheme: "https", Type: "github", Token: "ciphertext"},
		}},
		CM: &cmDocument{Profiles: map[string]cmDocumentProfile{
			"work": {BaseURL: "https://provider.example/v1", Model: "model", APIKey: "api-ciphertext"},
		}},
	}
}
