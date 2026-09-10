package appconfig

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveCMProfileUsesTheLegacySelectionPrecedence(t *testing.T) {
	t.Run("explicit profile wins over environment and default", func(t *testing.T) {
		environment := map[string]string{"YCY_CM_PROFILE": "environment"}
		store := populatedCMResolutionStore(t, environment)

		resolved, err := store.ResolveCMProfile(CMResolveOptions{ProfileName: "explicit"})
		if err != nil {
			t.Fatalf("ResolveCMProfile() returned an error: %v", err)
		}
		assertResolvedCMProfile(t, resolved, "explicit", "https://explicit.example/v1", "explicit-model", "explicit-key")
	})

	t.Run("environment profile wins over the stored default", func(t *testing.T) {
		environment := map[string]string{"YCY_CM_PROFILE": "environment"}
		store := populatedCMResolutionStore(t, environment)

		resolved, err := store.ResolveCMProfile(CMResolveOptions{})
		if err != nil {
			t.Fatalf("ResolveCMProfile() returned an error: %v", err)
		}
		assertResolvedCMProfile(t, resolved, "environment", "https://environment.example/v1", "environment-model", "environment-key")
	})

	t.Run("stored default wins over the first stored profile", func(t *testing.T) {
		store := populatedCMResolutionStore(t, map[string]string{})

		resolved, err := store.ResolveCMProfile(CMResolveOptions{})
		if err != nil {
			t.Fatalf("ResolveCMProfile() returned an error: %v", err)
		}
		assertResolvedCMProfile(t, resolved, "default", "https://default.example/v1", "default-model", "default-key")
	})

	t.Run("first stored profile is used when no default is present", func(t *testing.T) {
		store := populatedCMResolutionStore(t, map[string]string{})
		if err := store.updateDocument(func(document *document) error {
			document.CM.DefaultProfile = ""
			return nil
		}); err != nil {
			t.Fatalf("clear default profile: %v", err)
		}

		resolved, err := store.ResolveCMProfile(CMResolveOptions{})
		if err != nil {
			t.Fatalf("ResolveCMProfile() returned an error: %v", err)
		}
		assertResolvedCMProfile(t, resolved, "first", "https://first.example/v1", "first-model", "first-key")
	})
}

func TestResolveCMProfileAppliesEnvironmentCompatibilityRules(t *testing.T) {
	t.Run("environment values independently override the selected profile", func(t *testing.T) {
		environment := map[string]string{
			"YCY_CM_BASE_URL":          " https://environment.example/v1/// ",
			"YCY_CM_MODEL":             "environment-model",
			"YCY_CM_API_KEY":           "environment-key",
			"YCY_CM_TEMPERATURE":       " ",
			"YCY_CM_TIMEOUT_MS":        "Infinity",
			"YCY_CM_MAX_OUTPUT_TOKENS": "0x80",
		}
		store := populatedCMResolutionStore(t, environment)
		if err := store.SetCMProfileValue("default", "temperature", "1.5"); err != nil {
			t.Fatalf("SetCMProfileValue(temperature) returned an error: %v", err)
		}
		if err := store.SetCMProfileValue("default", "timeoutMs", "2400"); err != nil {
			t.Fatalf("SetCMProfileValue(timeoutMs) returned an error: %v", err)
		}
		if err := store.SetCMProfileValue("default", "maxOutputTokens", "96"); err != nil {
			t.Fatalf("SetCMProfileValue(maxOutputTokens) returned an error: %v", err)
		}
		override := 7000.25

		resolved, err := store.ResolveCMProfile(CMResolveOptions{TimeoutOverrideMS: &override})
		if err != nil {
			t.Fatalf("ResolveCMProfile() returned an error: %v", err)
		}
		assertResolvedCMProfile(t, resolved, "default", "https://environment.example/v1", "environment-model", "environment-key")
		if resolved.Temperature != 0 || resolved.TimeoutMS != override || resolved.MaxOutputTokens != 128 {
			t.Fatalf("resolved numeric values = (%v, %v, %v), want (0, %v, 128)", resolved.Temperature, resolved.TimeoutMS, resolved.MaxOutputTokens, override)
		}
	})

	t.Run("an environment-only profile is named env", func(t *testing.T) {
		environment := map[string]string{
			"YCY_CM_BASE_URL": "https://environment.example/v1///",
			"YCY_CM_MODEL":    "environment-model",
			"YCY_CM_API_KEY":  "environment-key",
		}
		store := semanticStore(t, environment)

		resolved, err := store.ResolveCMProfile(CMResolveOptions{})
		if err != nil {
			t.Fatalf("ResolveCMProfile() returned an error: %v", err)
		}
		assertResolvedCMProfile(t, resolved, "env", "https://environment.example/v1", "environment-model", "environment-key")
		if resolved.Temperature != defaultCMTemperature || resolved.TimeoutMS != defaultCMTimeoutMS || resolved.MaxOutputTokens != defaultCMMaxOutputTokens {
			t.Fatalf("environment defaults = (%v, %v, %v)", resolved.Temperature, resolved.TimeoutMS, resolved.MaxOutputTokens)
		}
	})

	t.Run("environment API key avoids unnecessary stored-key decryption", func(t *testing.T) {
		environment := map[string]string{"YCY_CM_API_KEY": "environment-key"}
		store := populatedCMResolutionStore(t, environment)
		store.machineID = func() (string, error) { return "", errors.New("machine ID unavailable") }

		resolved, err := store.ResolveCMProfile(CMResolveOptions{})
		if err != nil {
			t.Fatalf("ResolveCMProfile() returned an error with an environment API key: %v", err)
		}
		if resolved.APIKey != "environment-key" {
			t.Fatal("ResolveCMProfile() did not retain the environment API key")
		}

		delete(environment, "YCY_CM_API_KEY")
		if _, err := store.ResolveCMProfile(CMResolveOptions{}); err == nil || !strings.Contains(err.Error(), "resolve machine ID") {
			t.Fatalf("ResolveCMProfile() error = %v, want stored-key decryption failure", err)
		}
	})

	t.Run("missing values retain the actionable legacy error", func(t *testing.T) {
		store := semanticStore(t, map[string]string{})
		_, err := store.ResolveCMProfile(CMResolveOptions{})
		const want = "No usable CM profile found. Run \"ycy config cm add\" or set YCY_CM_BASE_URL, YCY_CM_MODEL, and YCY_CM_API_KEY."
		if err == nil || err.Error() != want {
			t.Fatalf("ResolveCMProfile() error = %v, want %q", err, want)
		}
	})
}

func populatedCMResolutionStore(t *testing.T, environment map[string]string) *Store {
	t.Helper()
	store := semanticStore(t, environment)
	for _, profile := range []struct {
		name  string
		url   string
		model string
		key   string
	}{
		{name: "first", url: "https://first.example/v1", model: "first-model", key: "first-key"},
		{name: "default", url: "https://default.example/v1", model: "default-model", key: "default-key"},
		{name: "environment", url: "https://environment.example/v1", model: "environment-model", key: "environment-key"},
		{name: "explicit", url: "https://explicit.example/v1", model: "explicit-model", key: "explicit-key"},
	} {
		if err := store.AddCMProfile(profile.name, profile.url, profile.model, profile.key); err != nil {
			t.Fatalf("AddCMProfile(%q) returned an error: %v", profile.name, err)
		}
	}
	if err := store.SetDefaultCMProfile("default"); err != nil {
		t.Fatalf("SetDefaultCMProfile(default) returned an error: %v", err)
	}
	return store
}

func assertResolvedCMProfile(t *testing.T, resolved ResolvedCMProfile, name, baseURL, model, apiKey string) {
	t.Helper()
	if resolved.Name != name || resolved.BaseURL != baseURL || resolved.Model != model || resolved.APIKey != apiKey {
		t.Fatalf("resolved safe fields = (%q, %q, %q), want (%q, %q, %q)", resolved.Name, resolved.BaseURL, resolved.Model, name, baseURL, model)
	}
}
