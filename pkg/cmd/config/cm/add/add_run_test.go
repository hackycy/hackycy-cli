package add

import (
	"context"
	"errors"
	"testing"
)

func TestAddModulePromptsSavesAndPresentsSuccess(t *testing.T) {
	store := &recordingCMAddStore{}
	presenter := &recordingCMAddPresenter{}
	module, err := NewAdd(AddDependencies{
		Prompter: &scriptedCMAddPrompter{responses: []cmAddPromptResponse{
			{value: "work"},
			{value: "https://provider.example"},
			{value: "model"},
			{value: "api-key"},
		}},
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
	if len(store.inputs) != 1 || store.inputs[0] != (AddInput{Name: "work", BaseURL: "https://provider.example", Model: "model", APIKey: "api-key"}) {
		t.Fatalf("store calls = %#v", store.inputs)
	}
	if presenter.success != "Profile work added" || presenter.cancellation != "" {
		t.Fatalf("presenter = %#v", presenter)
	}
}

func TestAddModuleCancellationDoesNotWrite(t *testing.T) {
	store := &recordingCMAddStore{}
	presenter := &recordingCMAddPresenter{}
	module, err := NewAdd(AddDependencies{
		Prompter:  &scriptedCMAddPrompter{responses: []cmAddPromptResponse{{cancelled: true}}},
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
	if !result.Cancelled || len(store.inputs) != 0 || presenter.cancellation != "Cancelled" || presenter.success != "" {
		t.Fatalf("Run() = (%#v, store=%#v, presenter=%#v)", result, store, presenter)
	}
}

func TestAddModuleReturnsPromptValidationAndSaveFailures(t *testing.T) {
	tests := []struct {
		name      string
		prompter  *scriptedCMAddPrompter
		store     *recordingCMAddStore
		wantError string
	}{
		{
			name:      "validation",
			prompter:  &scriptedCMAddPrompter{responses: []cmAddPromptResponse{{value: ""}}},
			store:     &recordingCMAddStore{},
			wantError: "Name is required",
		},
		{
			name: "save",
			prompter: &scriptedCMAddPrompter{responses: []cmAddPromptResponse{
				{value: "work"},
				{value: "https://provider.example"},
				{value: "model"},
				{value: "api-key"},
			}},
			store:     &recordingCMAddStore{err: errors.New("save configuration")},
			wantError: "save configuration",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			presenter := &recordingCMAddPresenter{}
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
		{Writer: &recordingCMAddStore{}, Presenter: &recordingCMAddPresenter{}},
		{Prompter: &scriptedCMAddPrompter{}, Presenter: &recordingCMAddPresenter{}},
		{Prompter: &scriptedCMAddPrompter{}, Writer: &recordingCMAddStore{}},
	} {
		if _, err := NewAdd(dependencies); err == nil {
			t.Fatalf("NewAdd(%#v) returned nil error", dependencies)
		}
	}
}
