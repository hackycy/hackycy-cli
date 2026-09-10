package remove

import (
	"errors"
	"sync"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

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

func TestRemoveProfileDelegatesToAppconfigAndKeepsMissingState(t *testing.T) {
	store := &recordingCMRemoveStore{}

	removed, err := RemoveProfile(store, "work")

	if err != nil || removed {
		t.Fatalf("RemoveProfile() = (%t, %v), want missing result", removed, err)
	}
	if got, want := store.names, []string{"work"}; !sameCMRemoveStrings(got, want) {
		t.Fatalf("remove names = %#v, want %#v", got, want)
	}
}

func TestRemoveProfilePropagatesAppconfigFailure(t *testing.T) {
	failure := errors.New("publish configuration")

	removed, err := RemoveProfile(&recordingCMRemoveStore{removed: true, err: failure}, "work")

	if !removed || !errors.Is(err, failure) {
		t.Fatalf("RemoveProfile() = (%t, %v), want appconfig result with failure", removed, err)
	}
}

func TestRemoveProfilePersistsDefaultTransitions(t *testing.T) {
	t.Run("nondefault removal retains the default", func(t *testing.T) {
		store, _ := newCMAddStore(t)
		addCMRemoveProfile(t, store, "primary", "primary-key")
		addCMRemoveProfile(t, store, "work", "work-key")

		removed, err := RemoveProfile(store, "work")
		if err != nil || !removed {
			t.Fatalf("RemoveProfile(work) = (%t, %v)", removed, err)
		}
		assertCMRemoveProfiles(t, store, "primary", []string{"primary"})
	})

	t.Run("default removal selects the first remaining profile", func(t *testing.T) {
		store, _ := newCMAddStore(t)
		addCMRemoveProfile(t, store, "primary", "primary-key")
		addCMRemoveProfile(t, store, "work", "work-key")
		if err := store.SetDefaultCMProfile("work"); err != nil {
			t.Fatalf("SetDefaultCMProfile(work) returned an error: %v", err)
		}

		removed, err := RemoveProfile(store, "work")
		if err != nil || !removed {
			t.Fatalf("RemoveProfile(work) = (%t, %v)", removed, err)
		}
		assertCMRemoveProfiles(t, store, "primary", []string{"primary"})
		resolved, err := store.ResolveCMProfile(appconfig.CMResolveOptions{})
		if err != nil || resolved.Name != "primary" || resolved.APIKey != "primary-key" {
			t.Fatalf("ResolveCMProfile() = (%#v, %v)", resolved, err)
		}
	})

	t.Run("last removal leaves no default", func(t *testing.T) {
		store, _ := newCMAddStore(t)
		addCMRemoveProfile(t, store, "only", "only-key")

		removed, err := RemoveProfile(store, "only")
		if err != nil || !removed {
			t.Fatalf("RemoveProfile(only) = (%t, %v)", removed, err)
		}
		assertCMRemoveProfiles(t, store, "", nil)
	})
}

func TestRemoveProfilePreservesConcurrentForkUpdates(t *testing.T) {
	store, _ := newCMAddStore(t)
	addCMRemoveProfile(t, store, "primary", "primary-key")
	addCMRemoveProfile(t, store, "remove", "remove-key")
	if err := store.SetDefaultCMProfile("remove"); err != nil {
		t.Fatalf("SetDefaultCMProfile(remove) returned an error: %v", err)
	}
	if err := store.SaveForkInstance("github", appconfig.ForkInput{Host: "github.com", Scheme: "https", Type: "github", Token: "github-token"}); err != nil {
		t.Fatalf("SaveForkInstance(github) returned an error: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		removed, err := RemoveProfile(store, "remove")
		if err == nil && !removed {
			results <- errors.New("selected CM profile was not removed")
			return
		}
		results <- err
	}()
	go func() {
		defer group.Done()
		<-start
		results <- store.SaveForkInstance("gitlab", appconfig.ForkInput{Host: "gitlab.com", Scheme: "https", Type: "gitlab", Token: "gitlab-token"})
	}()
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent update returned an error: %v", err)
		}
	}

	assertCMRemoveProfiles(t, store, "primary", []string{"primary"})
	instances, err := store.ListForkInstances()
	if err != nil || len(instances) != 2 || instances[0].Name != "github" || instances[1].Name != "gitlab" {
		t.Fatalf("ListForkInstances() = (%#v, %v)", instances, err)
	}
}

func addCMRemoveProfile(t *testing.T, store *appconfig.Store, name, apiKey string) {
	t.Helper()
	if err := store.AddCMProfile(name, "https://"+name+".example/v1", name+"-model", apiKey); err != nil {
		t.Fatalf("AddCMProfile(%s) returned an error: %v", name, err)
	}
}

func assertCMRemoveProfiles(t *testing.T, store *appconfig.Store, defaultName string, names []string) {
	t.Helper()
	profiles, err := store.ListCMProfiles()
	if err != nil {
		t.Fatalf("ListCMProfiles() returned an error: %v", err)
	}
	if profiles.DefaultProfile != defaultName || len(profiles.Profiles) != len(names) {
		t.Fatalf("ListCMProfiles() = %#v, want default %q and names %#v", profiles, defaultName, names)
	}
	for index, name := range names {
		if profiles.Profiles[index].Name != name {
			t.Fatalf("profile names = %#v, want %#v", profiles.Profiles, names)
		}
	}
}

type recordingCMRemoveStore struct {
	names   []string
	removed bool
	err     error
}

func (store *recordingCMRemoveStore) RemoveCMProfile(name string) (bool, error) {
	store.names = append(store.names, name)
	return store.removed, store.err
}

func sameCMRemoveStrings(got, want []string) bool {
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
