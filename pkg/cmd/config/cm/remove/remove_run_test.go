package remove

import (
	"context"
	"errors"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestRemoveModuleRetainsNonMutatingOutcomes(t *testing.T) {
	tests := []struct {
		name          string
		confirmed     bool
		confirmCancel bool
		want          RemoveResult
	}{
		{name: "confirmation cancellation", confirmCancel: true, want: RemoveResult{Cancelled: true}},
		{name: "negative confirmation", want: RemoveResult{Declined: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := configuredCMRemoveReader("work")
			prompter := &scriptedCMRemoveRunPrompter{confirmed: test.confirmed, cancelled: test.confirmCancel}
			writer := &recordingCMRemoveStore{removed: true}
			presenter := &recordingCMRemovePresenter{}
			module := newCMRemoveModule(t, reader, prompter, writer, presenter)

			result, err := module.Run(context.Background(), RemoveRequest{Profile: "work"})

			if err != nil || result != test.want {
				t.Fatalf("Run() = (%#v, %v), want (%#v, nil)", result, err, test.want)
			}
			if len(writer.names) != 0 {
				t.Fatalf("Run() wrote on a non-mutating branch: %#v", writer.names)
			}
			if presenter.cancellation != "Cancelled" || presenter.success != "" {
				t.Fatalf("presenter = %#v", presenter)
			}
		})
	}
}

func TestRemoveModuleRemovesAndPresentsTheConfirmedProfile(t *testing.T) {
	reader := configuredCMRemoveReader("work")
	writer := &recordingCMRemoveStore{removed: true}
	presenter := &recordingCMRemovePresenter{}
	module := newCMRemoveModule(t, reader, &scriptedCMRemoveRunPrompter{confirmed: true}, writer, presenter)

	result, err := module.Run(context.Background(), RemoveRequest{Profile: "work"})

	if err != nil || result != (RemoveResult{}) {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
	if got, want := writer.names, []string{"work"}; !sameCMRemoveStrings(got, want) {
		t.Fatalf("remove names = %#v, want %#v", got, want)
	}
	if presenter.success != "Profile work removed" || presenter.cancellation != "" {
		t.Fatalf("presenter = %#v", presenter)
	}
}

func TestRemoveModuleValidatesProfileBeforeConfirmationOrDeletion(t *testing.T) {
	reader := configuredCMRemoveReader()
	prompter := &scriptedCMRemoveRunPrompter{confirmed: true}
	writer := &recordingCMRemoveStore{removed: true}
	presenter := &recordingCMRemovePresenter{}
	module := newCMRemoveModule(t, reader, prompter, writer, presenter)

	result, err := module.Run(context.Background(), RemoveRequest{Profile: "missing"})

	if err == nil || err.Error() != "CM profile not found: missing" || result != (RemoveResult{}) {
		t.Fatalf("Run() = (%#v, %v), want missing-profile error", result, err)
	}
	if reader.calls != 1 || prompter.calls != 0 || len(writer.names) != 0 || presenter.cancellation != "" || presenter.success != "" {
		t.Fatalf("validation order = reader:%d prompt:%d writes:%#v presenter:%#v", reader.calls, prompter.calls, writer.names, presenter)
	}
}

func TestRemoveModuleReturnsReadInteractionAndWriteFailuresWithoutPresentation(t *testing.T) {
	readFailure := errors.New("read configuration")
	interactionFailure := errors.New("interactive terminal unavailable")
	writeFailure := errors.New("publish configuration")
	tests := []struct {
		name      string
		reader    *recordingCMRemoveReader
		prompter  *scriptedCMRemoveRunPrompter
		writer    *recordingCMRemoveStore
		wantError error
	}{
		{name: "read", reader: &recordingCMRemoveReader{err: readFailure}, prompter: &scriptedCMRemoveRunPrompter{}, writer: &recordingCMRemoveStore{}, wantError: readFailure},
		{name: "interaction", reader: configuredCMRemoveReader("work"), prompter: &scriptedCMRemoveRunPrompter{err: interactionFailure}, writer: &recordingCMRemoveStore{}, wantError: interactionFailure},
		{name: "write", reader: configuredCMRemoveReader("work"), prompter: &scriptedCMRemoveRunPrompter{confirmed: true}, writer: &recordingCMRemoveStore{err: writeFailure}, wantError: writeFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			presenter := &recordingCMRemovePresenter{}
			module := newCMRemoveModule(t, test.reader, test.prompter, test.writer, presenter)

			result, err := module.Run(context.Background(), RemoveRequest{Profile: "work"})

			if result != (RemoveResult{}) || !errors.Is(err, test.wantError) {
				t.Fatalf("Run() = (%#v, %v), want error %v", result, err, test.wantError)
			}
			if presenter.cancellation != "" || presenter.success != "" {
				t.Fatalf("failure presented an outcome: %#v", presenter)
			}
		})
	}
}

func TestNewRemoveRequiresEveryCommandOwnedAdapter(t *testing.T) {
	reader := configuredCMRemoveReader("work")
	prompter := &scriptedCMRemoveRunPrompter{}
	writer := &recordingCMRemoveStore{}
	presenter := &recordingCMRemovePresenter{}

	for _, dependencies := range []RemoveDependencies{
		{Prompter: prompter, Writer: writer, Presenter: presenter},
		{Reader: reader, Writer: writer, Presenter: presenter},
		{Reader: reader, Prompter: prompter, Presenter: presenter},
		{Reader: reader, Prompter: prompter, Writer: writer},
	} {
		if _, err := NewRemove(dependencies); err == nil {
			t.Fatalf("NewRemove(%#v) returned nil error", dependencies)
		}
	}
}

func newCMRemoveModule(t *testing.T, reader Reader, prompter RemoveConfirmationPrompter, writer RemoveWriter, presenter RemovePresenter) *RemoveModule {
	t.Helper()
	module, err := NewRemove(RemoveDependencies{Reader: reader, Prompter: prompter, Writer: writer, Presenter: presenter})
	if err != nil {
		t.Fatalf("NewRemove() returned an error: %v", err)
	}
	return module
}

type recordingCMRemoveReader struct {
	profiles appconfig.CMProfileList
	err      error
	calls    int
}

func configuredCMRemoveReader(names ...string) *recordingCMRemoveReader {
	profiles := make([]appconfig.CMProfile, len(names))
	for index, name := range names {
		profiles[index] = appconfig.CMProfile{Name: name}
	}
	return &recordingCMRemoveReader{profiles: appconfig.CMProfileList{Profiles: profiles}}
}

func (reader *recordingCMRemoveReader) ListCMProfiles() (appconfig.CMProfileList, error) {
	reader.calls++
	return reader.profiles, reader.err
}

type scriptedCMRemoveRunPrompter struct {
	confirmed bool
	cancelled bool
	err       error
	calls     int
}

func (prompter *scriptedCMRemoveRunPrompter) Confirm(RemoveConfirmPrompt) (bool, bool, error) {
	prompter.calls++
	return prompter.confirmed, prompter.cancelled, prompter.err
}
