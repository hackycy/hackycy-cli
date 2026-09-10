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
		instances     []appconfig.ForkInstance
		selection     string
		selectCancel  bool
		confirmed     bool
		confirmCancel bool
		want          RemoveResult
		wantInfos     []string
		wantOutcomes  []string
	}{
		{
			name:         "empty",
			want:         RemoveResult{Empty: true},
			wantInfos:    []string{"No instances configured"},
			wantOutcomes: []string{"Nothing to remove"},
		},
		{
			name:         "selection cancellation",
			instances:    []appconfig.ForkInstance{{Name: "work", Host: "gitlab.example"}},
			selectCancel: true,
			want:         RemoveResult{Cancelled: true},
			wantOutcomes: []string{"Cancelled"},
		},
		{
			name:          "confirmation cancellation",
			instances:     []appconfig.ForkInstance{{Name: "work", Host: "gitlab.example"}},
			selection:     "work",
			confirmCancel: true,
			want:          RemoveResult{Cancelled: true},
			wantOutcomes:  []string{"Cancelled"},
		},
		{
			name:         "negative confirmation",
			instances:    []appconfig.ForkInstance{{Name: "work", Host: "gitlab.example"}},
			selection:    "work",
			want:         RemoveResult{Declined: true},
			wantOutcomes: []string{"Cancelled"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prompter := &removeRunPrompter{
				selection:     test.selection,
				selectCancel:  test.selectCancel,
				confirmed:     test.confirmed,
				confirmCancel: test.confirmCancel,
			}
			writer := &removeRunWriter{}
			presenter := &removeRunPresenter{}
			module := newRemoveModule(t, removeRunReader{instances: test.instances}, prompter, writer, presenter)

			result, err := module.Run(context.Background(), RemoveRequest{})

			if err != nil || result != test.want {
				t.Fatalf("Run() = (%#v, %v), want (%#v, nil)", result, err, test.want)
			}
			if len(writer.names) != 0 {
				t.Fatalf("Run() wrote on non-mutating branch: %#v", writer.names)
			}
			if got := presenter.infos; !sameAddStrings(got, test.wantInfos) {
				t.Fatalf("info messages = %#v, want %#v", got, test.wantInfos)
			}
			if got := presenter.outcomes; !sameAddStrings(got, test.wantOutcomes) {
				t.Fatalf("outcome messages = %#v, want %#v", got, test.wantOutcomes)
			}
		})
	}
}

func TestRemoveModuleReportsSuccessWhenConcurrentStateIsAlreadyMissing(t *testing.T) {
	prompter := &removeRunPrompter{selection: "work", confirmed: true}
	writer := &removeRunWriter{removed: false}
	presenter := &removeRunPresenter{}
	module := newRemoveModule(t, removeRunReader{instances: []appconfig.ForkInstance{{Name: "work", Host: "gitlab.example"}}}, prompter, writer, presenter)

	result, err := module.Run(context.Background(), RemoveRequest{})

	if err != nil || result != (RemoveResult{}) {
		t.Fatalf("Run() = (%#v, %v), want success", result, err)
	}
	if got, want := writer.names, []string{"work"}; !sameAddStrings(got, want) {
		t.Fatalf("remove names = %#v, want %#v", got, want)
	}
	if got, want := presenter.outcomes, []string{"Instance work removed"}; !sameAddStrings(got, want) {
		t.Fatalf("outcome messages = %#v, want %#v", got, want)
	}
}

func TestRemoveModuleReturnsReadAndWriteFailuresWithoutSuccessPresentation(t *testing.T) {
	readFailure := errors.New("read configuration")
	presenter := &removeRunPresenter{}
	module := newRemoveModule(t, removeRunReader{err: readFailure}, &removeRunPrompter{}, &removeRunWriter{}, presenter)
	if _, err := module.Run(context.Background(), RemoveRequest{}); !errors.Is(err, readFailure) {
		t.Fatalf("Run() error = %v, want %v", err, readFailure)
	}
	if len(presenter.infos) != 0 || len(presenter.outcomes) != 0 {
		t.Fatalf("read failure presented an outcome: %#v %#v", presenter.infos, presenter.outcomes)
	}

	writeFailure := errors.New("publish configuration")
	presenter = &removeRunPresenter{}
	module = newRemoveModule(t, removeRunReader{instances: []appconfig.ForkInstance{{Name: "work", Host: "gitlab.example"}}}, &removeRunPrompter{selection: "work", confirmed: true}, &removeRunWriter{removed: true, err: writeFailure}, presenter)
	if _, err := module.Run(context.Background(), RemoveRequest{}); !errors.Is(err, writeFailure) {
		t.Fatalf("Run() error = %v, want %v", err, writeFailure)
	}
	if len(presenter.outcomes) != 0 {
		t.Fatalf("write failure presented false success: %#v", presenter.outcomes)
	}
}

func TestNewRemoveRequiresEveryAdapter(t *testing.T) {
	reader := removeRunReader{}
	prompter := &removeRunPrompter{}
	writer := &removeRunWriter{}
	presenter := &removeRunPresenter{}

	for _, dependencies := range []RemoveDependencies{
		{Prompter: prompter, Writer: writer, Presenter: presenter},
		{Reader: reader, Writer: writer, Presenter: presenter},
		{Reader: reader, Prompter: prompter, Presenter: presenter},
		{Reader: reader, Prompter: prompter, Writer: writer},
	} {
		if _, err := NewRemove(dependencies); err == nil {
			t.Fatal("NewRemove() accepted a missing adapter")
		}
	}
}

func newRemoveModule(t *testing.T, reader RemoveReader, prompter RemoveInteraction, writer RemoveWriter, presenter RemovePresenter) *RemoveModule {
	t.Helper()
	module, err := NewRemove(RemoveDependencies{Reader: reader, Prompter: prompter, Writer: writer, Presenter: presenter})
	if err != nil {
		t.Fatalf("NewRemove() returned an error: %v", err)
	}
	return module
}

type removeRunReader struct {
	instances []appconfig.ForkInstance
	err       error
}

func (reader removeRunReader) ListForkInstances() ([]appconfig.ForkInstance, error) {
	return reader.instances, reader.err
}

type removeRunPrompter struct {
	selection     string
	selectCancel  bool
	selectErr     error
	confirmed     bool
	confirmCancel bool
	confirmErr    error
}

func (prompter *removeRunPrompter) Select(SelectPrompt) (string, bool, error) {
	return prompter.selection, prompter.selectCancel, prompter.selectErr
}

func (prompter *removeRunPrompter) Confirm(ConfirmPrompt) (bool, bool, error) {
	return prompter.confirmed, prompter.confirmCancel, prompter.confirmErr
}

type removeRunWriter struct {
	names   []string
	removed bool
	err     error
}

func (writer *removeRunWriter) RemoveForkInstance(name string) (bool, error) {
	writer.names = append(writer.names, name)
	return writer.removed, writer.err
}

type removeRunPresenter struct {
	infos    []string
	outcomes []string
}

func (presenter *removeRunPresenter) Info(message string) {
	presenter.infos = append(presenter.infos, message)
}

func (presenter *removeRunPresenter) Outcome(message string) {
	presenter.outcomes = append(presenter.outcomes, message)
}
