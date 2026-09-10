package appconfig

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	legacySalt       = "bGVnYWN5LWNvbmZpZy1zYWx0"
	legacyCiphertext = "MDEyMzQ1Njc4OWFiY2RlZg==:dc4+2YzE3mJaY01o7OyLtw==:QvEGhYbu7lpp4lc2/N0a8IXumQ=="
	legacySecret     = "token: hello \u4f60\u597d"
)

func TestSemanticReadsDecryptBunWrittenCurrentAndLegacyConfig(t *testing.T) {
	t.Run("current shape", func(t *testing.T) {
		store := semanticStore(t, nil)
		writeConfigFixture(t, store, `{
  "salt": "bGVnYWN5LWNvbmZpZy1zYWx0",
  "fork": {"instances": {
    "beta": {"host": "gitlab.example", "type": "gitlab", "token": "MDEyMzQ1Njc4OWFiY2RlZg==:dc4+2YzE3mJaY01o7OyLtw==:QvEGhYbu7lpp4lc2/N0a8IXumQ=="},
    "alpha": {"host": "github.example", "scheme": "http", "type": "github", "token": "MDEyMzQ1Njc4OWFiY2RlZg==:dc4+2YzE3mJaY01o7OyLtw==:QvEGhYbu7lpp4lc2/N0a8IXumQ=="}
  }},
  "cm": {"defaultProfile": "work", "profiles": {
    "work": {"baseURL": "https://provider.example/v1", "model": "model", "apiKey": "MDEyMzQ1Njc4OWFiY2RlZg==:dc4+2YzE3mJaY01o7OyLtw==:QvEGhYbu7lpp4lc2/N0a8IXumQ=="}
  }},
  "tunnel": {"connections": {
    "v1_ca9ZFHpM2ZP-PeEpi-Fq8we7VIHwuyOP4iJe2ULvWOs": {"server": "https://tunnel.example", "token": "MDEyMzQ1Njc4OWFiY2RlZg==:dc4+2YzE3mJaY01o7OyLtw==:QvEGhYbu7lpp4lc2/N0a8IXumQ==", "lastAuthenticatedAt": "2026-01-01T00:00:00.000Z"}
  }}
}`)

		forks, err := store.ListForkInstances()
		if err != nil {
			t.Fatalf("ListForkInstances() returned an error: %v", err)
		}
		if got, want := forkNames(forks), []string{"beta", "alpha"}; !sameStrings(got, want) {
			t.Fatalf("Fork order = %#v, want %#v", got, want)
		}
		if forks[0].Scheme != "https" || forks[0].TokenPreview != "MDEy***" {
			t.Fatalf("safe Fork projection = %#v", forks[0])
		}
		if strings.Contains(fmt.Sprintf("%#v", forks), legacySecret) || strings.Contains(fmt.Sprintf("%#v", forks), legacyCiphertext) {
			t.Fatal("Fork list exposed a secret or full ciphertext")
		}
		fork, found, err := store.ForkInstance("beta")
		if err != nil || !found || fork.Token != legacySecret {
			t.Fatalf("ForkInstance() = (%#v, %t, %v)", fork, found, err)
		}

		profiles, err := store.ListCMProfiles()
		if err != nil {
			t.Fatalf("ListCMProfiles() returned an error: %v", err)
		}
		if profiles.DefaultProfile != "work" || len(profiles.Profiles) != 1 || profiles.Profiles[0].Name != "work" {
			t.Fatalf("ListCMProfiles() = %#v", profiles)
		}
		if strings.Contains(fmt.Sprintf("%#v", profiles), legacySecret) || strings.Contains(fmt.Sprintf("%#v", profiles), legacyCiphertext) {
			t.Fatal("CM list exposed a secret or full ciphertext")
		}
		resolved, err := store.ResolveCMProfile(CMResolveOptions{})
		if err != nil || resolved.APIKey != legacySecret || resolved.TimeoutMS != 300000 || resolved.MaxOutputTokens != 1000 {
			t.Fatalf("ResolveCMProfile() = (%#v, %v)", resolved, err)
		}

		connections, err := store.ReadTunnelConnections()
		if err != nil || len(connections) != 1 || connections[0].Token != legacySecret {
			t.Fatalf("ReadTunnelConnections() = (%#v, %v)", connections, err)
		}
	})

	t.Run("legacy instances and ai", func(t *testing.T) {
		store := semanticStore(t, nil)
		writeConfigFixture(t, store, `{
  "salt": "bGVnYWN5LWNvbmZpZy1zYWx0",
  "instances": {"legacy": {"host": "https://legacy.example", "type": "github", "token": "MDEyMzQ1Njc4OWFiY2RlZg==:dc4+2YzE3mJaY01o7OyLtw==:QvEGhYbu7lpp4lc2/N0a8IXumQ=="}},
  "ai": {"defaultProfile": "legacy", "profiles": {"legacy": {"baseURL": "https://legacy.provider/", "model": "legacy-model", "apiKey": "MDEyMzQ1Njc4OWFiY2RlZg==:dc4+2YzE3mJaY01o7OyLtw==:QvEGhYbu7lpp4lc2/N0a8IXumQ=="}}}
}`)

		fork, found, err := store.ForkInstance("legacy")
		if err != nil || !found || fork.Host != "legacy.example" || fork.Scheme != "https" || fork.Token != legacySecret {
			t.Fatalf("legacy ForkInstance() = (%#v, %t, %v)", fork, found, err)
		}
		resolved, err := store.ResolveCMProfile(CMResolveOptions{})
		if err != nil || resolved.Name != "legacy" || resolved.APIKey != legacySecret {
			t.Fatalf("legacy ResolveCMProfile() = (%#v, %v)", resolved, err)
		}
	})
}

func TestSemanticForkAndCMOperationsEncryptAndPreserveOrder(t *testing.T) {
	store := semanticStore(t, nil)
	if err := store.SaveForkInstance("first", ForkInput{Host: "first.example", Type: "github", Token: "first-token"}); err != nil {
		t.Fatalf("SaveForkInstance(first) returned an error: %v", err)
	}
	if err := store.SaveForkInstance("second", ForkInput{Host: "second.example", Scheme: "http", Type: "gitlab", Token: "second-token"}); err != nil {
		t.Fatalf("SaveForkInstance(second) returned an error: %v", err)
	}
	if err := store.SaveForkInstance("first", ForkInput{Host: "replacement.example", Type: "github", Token: "replacement-token"}); err != nil {
		t.Fatalf("SaveForkInstance(replacement) returned an error: %v", err)
	}
	forks, err := store.ListForkInstances()
	if err != nil || !sameStrings(forkNames(forks), []string{"first", "second"}) || forks[0].Host != "replacement.example" {
		t.Fatalf("ListForkInstances() = (%#v, %v)", forks, err)
	}
	removed, err := store.RemoveForkInstance("missing")
	if err != nil || removed {
		t.Fatalf("RemoveForkInstance(missing) = (%t, %v)", removed, err)
	}
	removed, err = store.RemoveForkInstance("second")
	if err != nil || !removed {
		t.Fatalf("RemoveForkInstance(second) = (%t, %v)", removed, err)
	}

	if err := store.AddCMProfile("work", " https://provider.example/v1/// ", " model ", "work-key"); err != nil {
		t.Fatalf("AddCMProfile(work) returned an error: %v", err)
	}
	if err := store.AddCMProfile("personal", "https://personal.example/", "personal-model", "personal-key"); err != nil {
		t.Fatalf("AddCMProfile(personal) returned an error: %v", err)
	}
	if err := store.SetDefaultCMProfile("personal"); err != nil {
		t.Fatalf("SetDefaultCMProfile() returned an error: %v", err)
	}
	if err := store.SetCMProfileValue("personal", "temperature", "1.5"); err != nil {
		t.Fatalf("SetCMProfileValue(temperature) returned an error: %v", err)
	}
	if err := store.SetCMProfileValue("personal", "timeoutMs", "1500suffix"); err != nil {
		t.Fatalf("SetCMProfileValue(timeoutMs) returned an error: %v", err)
	}
	if err := store.SetCMProfileValue("personal", "maxOutputTokens", "64suffix"); err != nil {
		t.Fatalf("SetCMProfileValue(maxOutputTokens) returned an error: %v", err)
	}
	profiles, err := store.ListCMProfiles()
	if err != nil || profiles.DefaultProfile != "personal" || !sameStrings(profileNames(profiles.Profiles), []string{"work", "personal"}) {
		t.Fatalf("ListCMProfiles() = (%#v, %v)", profiles, err)
	}
	resolved, err := store.ResolveCMProfile(CMResolveOptions{ProfileName: "personal"})
	if err != nil || resolved.BaseURL != "https://personal.example" || resolved.Model != "personal-model" || resolved.APIKey != "personal-key" || resolved.Temperature != 1.5 || resolved.TimeoutMS != 1500 || resolved.MaxOutputTokens != 64 {
		t.Fatalf("ResolveCMProfile() = (%#v, %v)", resolved, err)
	}

	contents, err := os.ReadFile(store.configPath())
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	for _, plaintext := range []string{"replacement-token", "work-key", "personal-key"} {
		if strings.Contains(string(contents), plaintext) {
			t.Fatalf("persisted config contains plaintext %q", plaintext)
		}
	}
}

func TestSetDefaultCMProfilePersistsSelectionWithoutChangingSharedConfiguration(t *testing.T) {
	store := semanticStore(t, nil)
	if err := store.SaveForkInstance("github", ForkInput{Host: "github.com", Type: "github", Token: "fork-token"}); err != nil {
		t.Fatalf("SaveForkInstance() returned an error: %v", err)
	}
	if err := store.AddCMProfile("primary", "https://primary.example/v1", "primary-model", "primary-key"); err != nil {
		t.Fatalf("AddCMProfile(primary) returned an error: %v", err)
	}
	if err := store.AddCMProfile("work", "https://work.example/v1", "work-model", "work-key"); err != nil {
		t.Fatalf("AddCMProfile(work) returned an error: %v", err)
	}

	if err := store.SetDefaultCMProfile("work"); err != nil {
		t.Fatalf("SetDefaultCMProfile(work) returned an error: %v", err)
	}

	profiles, err := store.ListCMProfiles()
	if err != nil {
		t.Fatalf("ListCMProfiles() returned an error: %v", err)
	}
	if profiles.DefaultProfile != "work" || !sameStrings(profileNames(profiles.Profiles), []string{"primary", "work"}) {
		t.Fatalf("ListCMProfiles() = %#v", profiles)
	}
	resolved, err := store.ResolveCMProfile(CMResolveOptions{})
	if err != nil || resolved.Name != "work" || resolved.BaseURL != "https://work.example/v1" || resolved.Model != "work-model" || resolved.APIKey != "work-key" {
		t.Fatalf("ResolveCMProfile() = (%#v, %v)", resolved, err)
	}
	primary, err := store.ResolveCMProfile(CMResolveOptions{ProfileName: "primary"})
	if err != nil || primary.BaseURL != "https://primary.example/v1" || primary.Model != "primary-model" || primary.APIKey != "primary-key" {
		t.Fatalf("ResolveCMProfile(primary) = (%#v, %v)", primary, err)
	}
	instances, err := store.ListForkInstances()
	if err != nil || len(instances) != 1 || instances[0].Name != "github" || instances[0].Host != "github.com" {
		t.Fatalf("ListForkInstances() = (%#v, %v)", instances, err)
	}

	contents, err := os.ReadFile(store.configPath())
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	var persisted struct {
		CM struct {
			DefaultProfile string `json:"defaultProfile"`
			Profiles       map[string]struct {
				BaseURL string `json:"baseURL"`
				Model   string `json:"model"`
			} `json:"profiles"`
		} `json:"cm"`
	}
	if err := json.Unmarshal(contents, &persisted); err != nil {
		t.Fatalf("decode persisted config: %v", err)
	}
	if persisted.CM.DefaultProfile != "work" || persisted.CM.Profiles["primary"].BaseURL != "https://primary.example/v1" || persisted.CM.Profiles["primary"].Model != "primary-model" || persisted.CM.Profiles["work"].BaseURL != "https://work.example/v1" || persisted.CM.Profiles["work"].Model != "work-model" {
		t.Fatalf("persisted CM = %#v", persisted.CM)
	}
}

func TestSetCMProfileStringFieldsNormalizeAndPreserveSharedConfiguration(t *testing.T) {
	store := semanticStore(t, nil)
	if err := store.SaveForkInstance("github", ForkInput{Host: "github.com", Type: "github", Token: "fork-token"}); err != nil {
		t.Fatalf("SaveForkInstance() returned an error: %v", err)
	}
	if err := store.AddCMProfile("work", "https://old.example/v1", "old-model", "work-key"); err != nil {
		t.Fatalf("AddCMProfile() returned an error: %v", err)
	}
	if err := store.SetCMProfileValue("work", "baseURL", " not a URL/// "); err != nil {
		t.Fatalf("SetCMProfileValue(baseURL) returned an error: %v", err)
	}
	if err := store.SetCMProfileValue("work", "model", "  "); err != nil {
		t.Fatalf("SetCMProfileValue(model) returned an error: %v", err)
	}

	contents, err := os.ReadFile(store.configPath())
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	var persisted struct {
		Fork struct {
			Instances map[string]struct {
				Host string `json:"host"`
			} `json:"instances"`
		} `json:"fork"`
		CM struct {
			DefaultProfile string `json:"defaultProfile"`
			Profiles       map[string]struct {
				BaseURL string `json:"baseURL"`
				Model   string `json:"model"`
				APIKey  string `json:"apiKey"`
			} `json:"profiles"`
		} `json:"cm"`
	}
	if err := json.Unmarshal(contents, &persisted); err != nil {
		t.Fatalf("decode persisted config: %v", err)
	}
	profile := persisted.CM.Profiles["work"]
	if profile.BaseURL != "not a URL" || profile.Model != "" || profile.APIKey == "work-key" {
		t.Fatalf("persisted work profile = %#v", profile)
	}
	if persisted.CM.DefaultProfile != "work" || persisted.Fork.Instances["github"].Host != "github.com" {
		t.Fatalf("persisted shared configuration = %#v", persisted)
	}
}

func TestSetCMProfileAPIKeyEncryptsAndPreservesExistingFields(t *testing.T) {
	store := semanticStore(t, nil)
	if err := store.SaveForkInstance("github", ForkInput{Host: "github.com", Type: "github", Token: "fork-token"}); err != nil {
		t.Fatalf("SaveForkInstance() returned an error: %v", err)
	}
	if err := store.AddCMProfile("work", "https://provider.example/v1", "work-model", "initial-api-key"); err != nil {
		t.Fatalf("AddCMProfile() returned an error: %v", err)
	}
	if err := store.SetCMProfileValue("work", "apiKey", "rotated-api-key"); err != nil {
		t.Fatalf("SetCMProfileValue(apiKey) returned an error: %v", err)
	}

	resolved, err := store.ResolveCMProfile(CMResolveOptions{ProfileName: "work"})
	if err != nil || resolved.BaseURL != "https://provider.example/v1" || resolved.Model != "work-model" || resolved.APIKey != "rotated-api-key" {
		t.Fatalf("ResolveCMProfile() = (%#v, %v)", resolved, err)
	}
	contents, err := os.ReadFile(store.configPath())
	if err != nil {
		t.Fatalf("read persisted config: %v", err)
	}
	for _, secret := range []string{"initial-api-key", "rotated-api-key", "fork-token"} {
		if strings.Contains(string(contents), secret) {
			t.Fatalf("persisted config contains plaintext %q", secret)
		}
	}
	instances, err := store.ListForkInstances()
	if err != nil || len(instances) != 1 || instances[0].Name != "github" || instances[0].Host != "github.com" {
		t.Fatalf("ListForkInstances() = (%#v, %v)", instances, err)
	}
}

func TestSetCMProfileNumericFieldsMatchLegacyParserBoundaries(t *testing.T) {
	for _, update := range []struct {
		name        string
		key         string
		value       string
		temperature float64
		timeout     float64
		maximum     float64
	}{
		{name: "temperature accepts empty Number input", key: "temperature", value: "", temperature: 0, timeout: 300000, maximum: 1000},
		{name: "temperature accepts hexadecimal Number input", key: "temperature", value: "0x2", temperature: 2, timeout: 300000, maximum: 1000},
		{name: "temperature accepts binary Number input", key: "temperature", value: "0b1", temperature: 1, timeout: 300000, maximum: 1000},
		{name: "temperature accepts octal Number input", key: "temperature", value: "0o2", temperature: 2, timeout: 300000, maximum: 1000},
		{name: "temperature accepts BOM whitespace", key: "temperature", value: "\uFEFF1.25", temperature: 1.25, timeout: 300000, maximum: 1000},
		{name: "timeout accepts decimal prefix", key: "timeoutMs", value: "+1000suffix", temperature: 0.2, timeout: 1000, maximum: 1000},
		{name: "timeout accepts fractional prefix", key: "timeoutMs", value: "1001.9", temperature: 0.2, timeout: 1001, maximum: 1000},
		{name: "timeout accepts BOM whitespace", key: "timeoutMs", value: "\uFEFF1002tail", temperature: 0.2, timeout: 1002, maximum: 1000},
		{name: "maximum accepts decimal prefix", key: "maxOutputTokens", value: "32suffix", temperature: 0.2, timeout: 300000, maximum: 32},
		{name: "maximum accepts fractional prefix", key: "maxOutputTokens", value: "33.9", temperature: 0.2, timeout: 300000, maximum: 33},
	} {
		update := update
		t.Run(update.name, func(t *testing.T) {
			store := semanticStore(t, nil)
			if err := store.AddCMProfile("work", "https://provider.example/v1", "work-model", "work-key"); err != nil {
				t.Fatalf("AddCMProfile() returned an error: %v", err)
			}
			if err := store.SetCMProfileValue("work", update.key, update.value); err != nil {
				t.Fatalf("SetCMProfileValue(%q, %q) returned an error: %v", update.key, update.value, err)
			}
			resolved, err := store.ResolveCMProfile(CMResolveOptions{ProfileName: "work"})
			if err != nil || resolved.Temperature != update.temperature || resolved.TimeoutMS != update.timeout || resolved.MaxOutputTokens != update.maximum {
				t.Fatalf("ResolveCMProfile() = (%#v, %v)", resolved, err)
			}
		})
	}

	store := semanticStore(t, nil)
	if err := store.AddCMProfile("work", "https://provider.example/v1", "work-model", "work-key"); err != nil {
		t.Fatalf("AddCMProfile() returned an error: %v", err)
	}
	for _, update := range []struct {
		key   string
		value string
		want  string
	}{
		{key: "temperature", value: "-0.1", want: "temperature must be a number between 0 and 2"},
		{key: "temperature", value: "2.0001", want: "temperature must be a number between 0 and 2"},
		{key: "temperature", value: "1suffix", want: "temperature must be a number between 0 and 2"},
		{key: "temperature", value: "Infinity", want: "temperature must be a number between 0 and 2"},
		{key: "timeoutMs", value: "999suffix", want: "timeoutMs must be an integer greater than or equal to 1000"},
		{key: "timeoutMs", value: "no-number", want: "timeoutMs must be an integer greater than or equal to 1000"},
		{key: "maxOutputTokens", value: "31.9", want: "maxOutputTokens must be an integer greater than or equal to 32"},
		{key: "maxOutputTokens", value: "no-number", want: "maxOutputTokens must be an integer greater than or equal to 32"},
	} {
		err := store.SetCMProfileValue("work", update.key, update.value)
		if err == nil || err.Error() != update.want {
			t.Fatalf("SetCMProfileValue(%q, %q) error = %v, want %q", update.key, update.value, err, update.want)
		}
	}
}

func TestSetCMProfileFailuresDoNotPublishOrMutateConfiguration(t *testing.T) {
	t.Run("missing profile does not publish configuration", func(t *testing.T) {
		store := semanticStore(t, nil)

		err := store.SetCMProfileValue("missing", "model", "next")
		if err == nil || err.Error() != "CM profile not found: missing" {
			t.Fatalf("SetCMProfileValue() error = %v", err)
		}
		if _, err := os.Stat(store.configPath()); !os.IsNotExist(err) {
			t.Fatalf("missing profile update published configuration: %v", err)
		}
	})

	t.Run("invalid updates leave the persisted document unchanged", func(t *testing.T) {
		store := semanticStore(t, nil)
		if err := store.AddCMProfile("work", "https://provider.example/v1", "work-model", "work-key"); err != nil {
			t.Fatalf("AddCMProfile() returned an error: %v", err)
		}
		before, err := os.ReadFile(store.configPath())
		if err != nil {
			t.Fatalf("read config before invalid updates: %v", err)
		}
		for _, update := range []struct {
			key   string
			value string
			want  string
		}{
			{key: "unsupported", value: "value", want: "Unsupported key. Use baseURL, model, apiKey, temperature, timeoutMs, or maxOutputTokens."},
			{key: "temperature", value: "2.1", want: "temperature must be a number between 0 and 2"},
			{key: "timeoutMs", value: "999", want: "timeoutMs must be an integer greater than or equal to 1000"},
			{key: "maxOutputTokens", value: "31", want: "maxOutputTokens must be an integer greater than or equal to 32"},
		} {
			err := store.SetCMProfileValue("work", update.key, update.value)
			if err == nil || err.Error() != update.want {
				t.Fatalf("SetCMProfileValue(%q, %q) error = %v, want %q", update.key, update.value, err, update.want)
			}
		}
		after, err := os.ReadFile(store.configPath())
		if err != nil {
			t.Fatalf("read config after invalid updates: %v", err)
		}
		if string(after) != string(before) {
			t.Fatalf("invalid updates changed config\nbefore: %s\nafter: %s", before, after)
		}
	})
}

func TestSetDefaultCMProfileMissingDoesNotPublishConfiguration(t *testing.T) {
	store := semanticStore(t, nil)

	err := store.SetDefaultCMProfile("missing")
	if err == nil || err.Error() != "CM profile not found: missing" {
		t.Fatalf("SetDefaultCMProfile(missing) error = %v", err)
	}
	if _, err := os.Stat(store.configPath()); !os.IsNotExist(err) {
		t.Fatalf("missing selection published configuration: %v", err)
	}
}

func TestSemanticOperationsSerializeConcurrentIndependentUpdates(t *testing.T) {
	store := semanticStore(t, nil)
	var group sync.WaitGroup
	errors := make(chan error, 12)
	for index := 0; index < 6; index++ {
		index := index
		group.Add(2)
		go func() {
			defer group.Done()
			errors <- store.SaveForkInstance(fmt.Sprintf("fork-%d", index), ForkInput{Host: fmt.Sprintf("fork-%d.example", index), Type: "github", Token: fmt.Sprintf("fork-token-%d", index)})
		}()
		go func() {
			defer group.Done()
			errors <- store.AddCMProfile(fmt.Sprintf("profile-%d", index), fmt.Sprintf("https://profile-%d.example", index), "model", fmt.Sprintf("api-key-%d", index))
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent semantic update returned an error: %v", err)
		}
	}
	forks, err := store.ListForkInstances()
	if err != nil || len(forks) != 6 {
		t.Fatalf("ListForkInstances() = (%#v, %v)", forks, err)
	}
	profiles, err := store.ListCMProfiles()
	if err != nil || len(profiles.Profiles) != 6 {
		t.Fatalf("ListCMProfiles() = (%#v, %v)", profiles, err)
	}
}

func TestCMResolutionEnvironmentPrecedenceAndErrors(t *testing.T) {
	environment := map[string]string{}
	store := semanticStore(t, environment)
	if err := store.AddCMProfile("stored", "https://stored.example/", "stored-model", "stored-key"); err != nil {
		t.Fatalf("AddCMProfile() returned an error: %v", err)
	}
	environment["YCY_CM_PROFILE"] = "stored"
	environment["YCY_CM_BASE_URL"] = "https://environment.example///"
	environment["YCY_CM_MODEL"] = "environment-model"
	environment["YCY_CM_API_KEY"] = "environment-key"
	environment["YCY_CM_TEMPERATURE"] = "1.25"
	environment["YCY_CM_TIMEOUT_MS"] = "5000.5"
	environment["YCY_CM_MAX_OUTPUT_TOKENS"] = "99.5"
	override := 7000.25
	resolved, err := store.ResolveCMProfile(CMResolveOptions{TimeoutOverrideMS: &override})
	if err != nil || resolved.Name != "stored" || resolved.BaseURL != "https://environment.example" || resolved.Model != "environment-model" || resolved.APIKey != "environment-key" || resolved.Temperature != 1.25 || resolved.TimeoutMS != 7000.25 || resolved.MaxOutputTokens != 99.5 {
		t.Fatalf("ResolveCMProfile() = (%#v, %v)", resolved, err)
	}
	for _, update := range []struct {
		key   string
		value string
	}{
		{"temperature", "2.1"},
		{"timeoutMs", "999"},
		{"maxOutputTokens", "31"},
		{"unsupported", "value"},
	} {
		if err := store.SetCMProfileValue("stored", update.key, update.value); err == nil {
			t.Fatalf("SetCMProfileValue(%q, %q) returned nil error", update.key, update.value)
		}
	}
	if err := store.SetDefaultCMProfile("missing"); err == nil || err.Error() != "CM profile not found: missing" {
		t.Fatalf("SetDefaultCMProfile(missing) error = %v", err)
	}
}

func TestTunnelCatalogEncryptsSortsCapsAndSkipsCorruptEntries(t *testing.T) {
	store := semanticStore(t, nil)
	connections, err := store.ReadTunnelConnections()
	if err != nil || len(connections) != 0 {
		t.Fatalf("initial ReadTunnelConnections() = (%#v, %v)", connections, err)
	}
	if _, err := os.Stat(store.configPath()); err != nil {
		t.Fatalf("ReadTunnelConnections() did not ensure config: %v", err)
	}

	for index := 0; index < 33; index++ {
		server, err := url.Parse(fmt.Sprintf("https://host-%d.example", index))
		if err != nil {
			t.Fatalf("parse tunnel server: %v", err)
		}
		if err := store.RememberTunnelConnection(server, fmt.Sprintf("token-%d", index), time.Date(2026, time.January, index+1, 0, 0, 0, 0, time.UTC)); err != nil {
			t.Fatalf("RememberTunnelConnection(%d) returned an error: %v", index, err)
		}
	}
	connections, err = store.ReadTunnelConnections()
	if err != nil || len(connections) != 32 || connections[0].Token != "token-32" {
		t.Fatalf("ReadTunnelConnections() = (%#v, %v)", connections, err)
	}
	server, err := url.Parse("https://host-32.example")
	if err != nil {
		t.Fatalf("parse tunnel server: %v", err)
	}
	id, err := store.TunnelInstanceID(server, "token-32")
	if err != nil || id != connections[0].ID || !strings.HasPrefix(id, "v1_") || len(id) != 46 {
		t.Fatalf("TunnelInstanceID() = (%q, %v)", id, err)
	}

	document, _, err := store.readDocument()
	if err != nil {
		t.Fatalf("readDocument() returned an error: %v", err)
	}
	document.Tunnel.Connections[connections[0].ID] = tunnelDocumentConnection{Server: connections[0].Server, Token: "invalid-ciphertext", LastAuthenticatedAt: connections[0].LastAuthenticatedAt}
	if err := store.writeDocument(document); err != nil {
		t.Fatalf("writeDocument() returned an error: %v", err)
	}
	connections, err = store.ReadTunnelConnections()
	if err != nil || len(connections) != 31 {
		t.Fatalf("ReadTunnelConnections() after corruption = (%#v, %v)", connections, err)
	}
}

func semanticStore(t *testing.T, environment map[string]string) *Store {
	t.Helper()
	home := t.TempDir()
	if environment == nil {
		environment = map[string]string{}
	}
	store, err := New(Dependencies{
		Environment: func(key string) string {
			if key == "HOME" {
				return home
			}
			return environment[key]
		},
		UserHomeDir: func() (string, error) { return home, nil },
		MachineID:   func() (string, error) { return "machine-id", nil },
		Username:    func() (string, error) { return "alice", nil },
	})
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}
	return store
}

func forkNames(instances []ForkInstance) []string {
	names := make([]string, 0, len(instances))
	for _, instance := range instances {
		names = append(names, instance.Name)
	}
	return names
}

func profileNames(profiles []CMProfile) []string {
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	return names
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
