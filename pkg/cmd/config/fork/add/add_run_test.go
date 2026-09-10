package add

import (
	"context"
	"errors"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestAddModulePromptsSavesAndPresentsSuccess(t *testing.T) {
	store := &outcomeAddStore{}
	presenter := &outcomeAddPresenter{}
	module, err := NewAdd(AddDependencies{
		Prompter: &scriptedAddPrompter{
			texts:      []promptResponse{{value: "work"}, {value: "gitlab.example"}},
			selections: []promptResponse{{value: "gitlab"}, {value: "https"}},
			passwords:  []promptResponse{{value: "token"}},
		},
		Writer:    store,
		Presenter: presenter,
	})
	if err != nil {
		t.Fatalf("NewAdd() returned an error: %v", err)
	}

	result, err := module.Run(context.Background(), AddRequest{})
	if err != nil {
		t.Fatalf("Run() returned an error: %v", err)
	}
	if result.Cancelled {
		t.Fatal("Run() reported cancellation")
	}
	if len(store.names) != 1 || store.names[0] != "work" || len(store.inputs) != 1 || store.inputs[0] != (appconfig.ForkInput{Host: "gitlab.example", Scheme: "https", Type: "gitlab", Token: "token"}) {
		t.Fatalf("store calls = %#v %#v", store.names, store.inputs)
	}
	if presenter.success != "Instance work (gitlab.example) added successfully" || presenter.cancellation != "" {
		t.Fatalf("presenter = %#v", presenter)
	}
}

func TestAddModuleCancellationDoesNotWrite(t *testing.T) {
	store := &outcomeAddStore{}
	presenter := &outcomeAddPresenter{}
	module, err := NewAdd(AddDependencies{
		Prompter:  &scriptedAddPrompter{texts: []promptResponse{{cancelled: true}}},
		Writer:    store,
		Presenter: presenter,
	})
	if err != nil {
		t.Fatalf("NewAdd() returned an error: %v", err)
	}

	result, err := module.Run(context.Background(), AddRequest{})
	if err != nil {
		t.Fatalf("Run() returned an error: %v", err)
	}
	if !result.Cancelled || len(store.names) != 0 || presenter.cancellation != "Cancelled" || presenter.success != "" {
		t.Fatalf("Run() = (%#v, store=%#v, presenter=%#v)", result, store, presenter)
	}
}

func TestAddModuleReturnsPromptValidationAndSaveFailures(t *testing.T) {
	tests := []struct {
		name      string
		prompter  *scriptedAddPrompter
		store     *outcomeAddStore
		wantError string
	}{
		{
			name:      "validation",
			prompter:  &scriptedAddPrompter{texts: []promptResponse{{value: ""}}},
			store:     &outcomeAddStore{},
			wantError: "Name is required",
		},
		{
			name: "save",
			prompter: &scriptedAddPrompter{
				texts:      []promptResponse{{value: "work"}, {value: "gitlab.example"}},
				selections: []promptResponse{{value: "gitlab"}, {value: "https"}},
				passwords:  []promptResponse{{value: "token"}},
			},
			store:     &outcomeAddStore{err: errors.New("save configuration")},
			wantError: "save configuration",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			presenter := &outcomeAddPresenter{}
			module, err := NewAdd(AddDependencies{Prompter: test.prompter, Writer: test.store, Presenter: presenter})
			if err != nil {
				t.Fatalf("NewAdd() returned an error: %v", err)
			}

			result, err := module.Run(context.Background(), AddRequest{})
			if err == nil || err.Error() != test.wantError || result != (AddResult{}) || presenter.cancellation != "" || presenter.success != "" {
				t.Fatalf("Run() = (%#v, %v, %#v), want error %q", result, err, presenter, test.wantError)
			}
		})
	}
}

func TestNewAddRequiresEachCommandOwnedAdapter(t *testing.T) {
	for _, dependencies := range []AddDependencies{
		{Writer: &outcomeAddStore{}, Presenter: &outcomeAddPresenter{}},
		{Prompter: &scriptedAddPrompter{}, Presenter: &outcomeAddPresenter{}},
		{Prompter: &scriptedAddPrompter{}, Writer: &outcomeAddStore{}},
	} {
		if _, err := NewAdd(dependencies); err == nil {
			t.Fatalf("NewAdd(%#v) returned nil error", dependencies)
		}
	}
}

type outcomeAddStore struct {
	names  []string
	inputs []appconfig.ForkInput
	err    error
}

func (store *outcomeAddStore) SaveForkInstance(name string, input appconfig.ForkInput) error {
	store.names = append(store.names, name)
	store.inputs = append(store.inputs, input)
	return store.err
}

type outcomeAddPresenter struct {
	cancellation string
	success      string
}

func (presenter *outcomeAddPresenter) Cancel(message string) {
	presenter.cancellation = message
}

func (presenter *outcomeAddPresenter) Success(message string) {
	presenter.success = message
}
