package add

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalForkAddAdapterTranslatesTheOrderedForm(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "work"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "gitlab.example"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "gitlab"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "https"}},
		terminaltest.SemanticAnswer{Value: terminalexperience.InteractionAnswer{Value: "secret-token"}},
	)
	run := experience.Open(context.Background())
	adapter := newTerminalForkAddAdapter(run)

	input, cancelled, err := PromptAdd(adapter)
	if err != nil || cancelled {
		t.Fatalf("PromptAdd() = (%#v, %t, %v)", input, cancelled, err)
	}
	if got, want := input, (AddInput{Alias: "work", Host: "gitlab.example", Type: "gitlab", Scheme: "https", Token: "secret-token"}); got != want {
		t.Fatalf("PromptAdd() = %#v, want %#v", got, want)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	operations := experience.Run.Operations()
	if len(operations) != 6 {
		t.Fatalf("operations = %#v", operations)
	}
	wantKinds := []terminalexperience.InteractionKind{
		terminalexperience.InteractionText,
		terminalexperience.InteractionText,
		terminalexperience.InteractionSelect,
		terminalexperience.InteractionSelect,
		terminalexperience.InteractionSecret,
	}
	wantMessages := []string{"Instance name (alias)", "Host", "Provider type", "Protocol", "Access token"}
	wantConsoleStepIDs := []string{
		forkAddIdentityFormID,
		forkAddHostFormID,
		forkAddProviderFormID,
		forkAddProtocolFormID,
		forkAddCredentialFormID,
	}
	for index := range wantKinds {
		if operations[index].Kind != terminaltest.AskOperation {
			t.Fatalf("operation %d kind = %q, want ask", index, operations[index].Kind)
		}
		request, ok := operations[index].Value.(terminalexperience.InteractionRequest)
		if !ok {
			t.Fatalf("operation %d request = %#v", index, operations[index].Value)
		}
		if request.Kind != wantKinds[index] || request.Message != wantMessages[index] || request.ConsoleStepID != wantConsoleStepIDs[index] {
			t.Fatalf("request %d = %#v", index, request)
		}
	}
	first := operations[0].Value.(terminalexperience.InteractionRequest)
	second := operations[1].Value.(terminalexperience.InteractionRequest)
	if first.TranscriptProject == nil || second.TranscriptProject == nil {
		t.Fatal("text requests must have safe transcript projections")
	}
	if got := first.TranscriptProject(terminalexperience.InteractionAnswer{Value: "safe"}); got != "safe" {
		t.Fatalf("alias transcript = %q", got)
	}
	if got := second.TranscriptProject(terminalexperience.InteractionAnswer{Value: "https://user:pass@example.test/path?secret=hidden#fragment"}); got != "example.test/path" {
		t.Fatalf("host transcript = %q", got)
	}
	if first.Placeholder != "e.g. work, github, company-gl" || second.Placeholder != "e.g. gitlab.company.com, github.com" {
		t.Fatalf("text placeholders = (%q, %q)", first.Placeholder, second.Placeholder)
	}
	provider := operations[2].Value.(terminalexperience.InteractionRequest)
	if !provider.HasDefault || provider.Default.Value != "gitlab" || !reflect.DeepEqual(provider.Options, []terminalexperience.InteractionOption{{Label: "GitLab", Value: "gitlab"}, {Label: "GitHub", Value: "github"}}) {
		t.Fatalf("provider request = %#v", provider)
	}
	protocol := operations[3].Value.(terminalexperience.InteractionRequest)
	if !protocol.HasDefault || protocol.Default.Value != "https" || !reflect.DeepEqual(protocol.Options, []terminalexperience.InteractionOption{{Label: "HTTPS", Value: "https"}, {Label: "HTTP (self-hosted / no TLS)", Value: "http"}}) {
		t.Fatalf("protocol request = %#v", protocol)
	}
	if err := first.Validate(terminalexperience.InteractionAnswer{}); err == nil || err.Error() != "Name is required" {
		t.Fatalf("alias validation = %v", err)
	}
	if err := operations[4].Value.(terminalexperience.InteractionRequest).Validate(terminalexperience.InteractionAnswer{}); err == nil || err.Error() != "Token is required" {
		t.Fatalf("token validation = %v", err)
	}
	credential := operations[4].Value.(terminalexperience.InteractionRequest)
	if !credential.Sensitive {
		t.Fatalf("credential request = %#v, want Sensitive", credential)
	}
	if operations[5].Kind != terminaltest.CloseOperation {
		t.Fatalf("last operation = %#v, want close", operations[5])
	}
}

func TestConfigForkAddConsoleDescriptorProvidesSafeBoundedContext(t *testing.T) {
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / config fork add",
		Target:  "Add fork provider instance - Store a provider connection for git fork operations",
		Status:  "READY",
		FormCatalog: []terminalexperience.ConsoleFormStep{
			{ID: forkAddIdentityFormID, Name: "Identity", Detail: "alias"},
			{ID: forkAddHostFormID, Name: "Host", Detail: "hostname"},
			{ID: forkAddProviderFormID, Name: "Provider", Detail: "type"},
			{ID: forkAddProtocolFormID, Name: "Protocol", Detail: "scheme"},
			{ID: forkAddCredentialFormID, Name: "Credential", Detail: "token", Sensitive: true},
		},
	}
	if got := terminalForkAddConsoleDescriptor(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
	for _, field := range []string{want.Command, want.Target, want.Status} {
		if strings.ContainsAny(field, "\r\n\t\x1b") {
			t.Fatalf("descriptor field contains terminal control: %q", field)
		}
	}
}

func TestTerminalForkAddAdapterMapsTerminalCancellation(t *testing.T) {
	experience := terminaltest.NewRecordingExperience(terminaltest.SemanticAnswer{Err: terminalexperience.ErrInteractionCancelled})
	adapter := newTerminalForkAddAdapter(experience.Open(context.Background()))

	input, cancelled, err := PromptAdd(adapter)
	if err != nil || !cancelled || input != (AddInput{}) {
		t.Fatalf("PromptAdd() = (%#v, %t, %v)", input, cancelled, err)
	}
}

func TestTerminalForkAddAdapterRoutesPlainPromptAndValidationToDiagnostics(t *testing.T) {
	stdout, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        strings.NewReader("\nwork\n"),
		Output:       stdout,
		Diagnostics:  diagnostics,
	})
	run := experience.Open(context.Background())
	adapter := newTerminalForkAddAdapter(run)

	value, cancelled, err := adapter.Text(TextPrompt{
		Message:     "Instance name (alias)",
		Placeholder: "e.g. work, github, company-gl",
		Validate: func(value string) error {
			if value == "" {
				return errors.New("Name is required")
			}
			return nil
		},
	})
	if err != nil || cancelled || value != "work" {
		t.Fatalf("Text() = (%q, %t, %v)", value, cancelled, err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want no prompt output", stdout.String())
	}
	for _, expected := range []string{"Instance name (alias)", "e.g. work, github, company-gl", "Name is required"} {
		if !strings.Contains(diagnostics.String(), expected) {
			t.Fatalf("diagnostics = %q, missing %q", diagnostics.String(), expected)
		}
	}
	if terminaltest.ContainsTerminalControl(diagnostics.Bytes()) {
		t.Fatalf("Plain prompt diagnostics contain terminal control: %q", diagnostics.String())
	}
}

func TestTerminalForkAddPresentationUsesTheSharedOutputBoundary(t *testing.T) {
	var output bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Output:       &output,
	})
	run := experience.Open(context.Background())
	adapter := newTerminalForkAddAdapter(run)
	adapter.Success("Instance work (gitlab.example) added successfully")
	adapter.Cancel("Cancelled")
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got, want := output.String(), "Instance work (gitlab.example) added successfully\nCancelled\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if terminaltest.ContainsTerminalControl(output.Bytes()) {
		t.Fatalf("plain result contains terminal control: %q", output.String())
	}

	for _, testCase := range []struct {
		cancelled bool
		role      terminalexperience.VisualRole
	}{
		{role: terminalexperience.VisualRoleSuccess},
		{cancelled: true, role: terminalexperience.VisualRoleWarning},
	} {
		document := terminalForkAddDocument("result", testCase.cancelled)
		if got := document.Blocks[0].Role; got != testCase.role {
			t.Fatalf("Rich role = %v, want %v", got, testCase.role)
		}
	}
}

func TestConfigForkAddAutomationFailsBeforeReadOrWrite(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", "")
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Input:        panicForkAddReader{},
		Output:       stdout,
		Diagnostics:  stderr,
	})
	err := runAdd(&Options{
		Context:  context.Background(),
		Terminal: experience,
		Store: func() (AddWriter, error) {
			panic("config fork add attempted to construct the store")
		},
	})
	if !errors.Is(err, errConfigForkAddRequiresInteractive) {
		t.Fatalf("runAdd() error = %v", err)
	}
	if got, want := stdout.String(), ""; got != want {
		t.Fatalf("stdout = %q, want empty", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("direct leaf execution wrote diagnostics: %q", stderr.String())
	}
	if terminaltest.ContainsTerminalControl(stderr.Bytes()) {
		t.Fatalf("automation stderr contains terminal control: %q", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".ycy-cli", "config.json")); !os.IsNotExist(err) {
		t.Fatalf("Automation failure wrote configuration: %v", err)
	}
}

func TestRunForkAddCancellationAfterFormDoesNotWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &cancelAfterForkAddLines{reader: strings.NewReader("work\ngitlab.example\n1\n1\n"), cancel: cancel, cancelAt: 4}
	var output, diagnostics bytes.Buffer
	var storeCalls, writes int
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.PlainInteractive},
		Input:        reader,
		Output:       &output,
		Diagnostics:  &diagnostics,
	})
	err := runAdd(&Options{
		Context:  ctx,
		Terminal: experience,
		Store: func() (AddWriter, error) {
			storeCalls++
			return forkAddWriterFunc(func(string, appconfig.ForkInput) error {
				writes++
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("runAdd() error = %v, want interactive cancellation", err)
	}
	if writes != 0 {
		t.Fatalf("writes = %d, want 0", writes)
	}
	if storeCalls != 0 {
		t.Fatalf("Store calls = %d, want 0 before the form completes", storeCalls)
	}
	if got, want := output.String(), "Cancelled\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if terminaltest.ContainsTerminalControl(append(output.Bytes(), diagnostics.Bytes()...)) {
		t.Fatalf("cancellation streams contain terminal controls: stdout=%q diagnostics=%q", output.String(), diagnostics.String())
	}
}

func TestForkAddPhaseSinkUsesOneWorkCatalogForCollectAndSave(t *testing.T) {
	experience := terminaltest.NewRecordingExperience()
	run := experience.Open(context.Background())
	sink := newForkAddPhaseSink(run, terminalexperience.Capabilities{Interaction: terminalexperience.RichInteractive})
	if err := sink.beginCollect(); err != nil {
		t.Fatalf("beginCollect() error = %v", err)
	}
	if err := sink.endCollect(terminalexperience.PhaseCompleted, "safe summary"); err != nil {
		t.Fatalf("endCollect() error = %v", err)
	}
	if err := sink.beginSave(); err != nil {
		t.Fatalf("beginSave() error = %v", err)
	}
	if err := sink.endSave(terminalexperience.PhaseCompleted, "Provider instance saved"); err != nil {
		t.Fatalf("endSave() error = %v", err)
	}
	if err := sink.close(); err != nil {
		t.Fatalf("close() error = %v", err)
	}

	operations := experience.Run.Operations()
	var startWork, workClose int
	var updates []terminalexperience.OperationPhase
	for _, operation := range operations {
		switch operation.Kind {
		case terminaltest.StartWorkOperation:
			startWork++
			if got, want := operation.Value.(terminalexperience.WorkCatalog), terminalForkAddWorkCatalog(); !reflect.DeepEqual(got, want) {
				t.Fatalf("Work catalog = %#v, want %#v", got, want)
			}
		case terminaltest.WorkUpdateOperation:
			updates = append(updates, operation.Value.(terminalexperience.OperationPhase))
		case terminaltest.WorkCloseOperation:
			workClose++
		case terminaltest.TrackOperation:
			t.Fatalf("legacy Track operation = %#v, want one controlled Work Catalog", operations)
		}
	}
	if startWork != 1 || workClose != 1 {
		t.Fatalf("operations = %#v, startWork=%d workClose=%d", operations, startWork, workClose)
	}
	want := []terminalexperience.OperationPhase{
		{ID: forkAddCollectPhaseID, State: terminalexperience.PhaseActive, Detail: "Answer the five provider fields"},
		{ID: forkAddCollectPhaseID, State: terminalexperience.PhaseCompleted, Detail: "safe summary"},
		{ID: forkAddSavePhaseID, State: terminalexperience.PhaseActive, Detail: "Writing encrypted provider configuration"},
		{ID: forkAddSavePhaseID, State: terminalexperience.PhaseCompleted, Detail: "Provider instance saved"},
	}
	if !reflect.DeepEqual(updates, want) {
		t.Fatalf("phase updates = %#v, want %#v", updates, want)
	}
}

func TestForkAddFinishRequestUsesSafeOutcomeSummaries(t *testing.T) {
	if got, want := terminalForkAddFinishRequest(terminalexperience.Succeeded, "ignored"), (terminalexperience.FinishRequest{
		Outcome: terminalexperience.Succeeded,
		Summary: terminalForkAddOutcomeDocument("Provider instance saved", terminalexperience.VisualRoleSuccess),
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("success Finish request = %#v, want %#v", got, want)
	}
	if got, want := terminalForkAddFinishRequest(terminalexperience.Cancelled, forkAddCollectPhaseName), (terminalexperience.FinishRequest{
		Outcome:  terminalexperience.Cancelled,
		Location: forkAddCollectPhaseName,
		Summary:  terminalForkAddOutcomeDocument("Provider setup cancelled", terminalexperience.VisualRoleWarning),
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("cancelled Finish request = %#v, want %#v", got, want)
	}
	if got, want := terminalForkAddFinishRequest(terminalexperience.Failed, forkAddSavePhaseName), (terminalexperience.FinishRequest{
		Outcome:  terminalexperience.Failed,
		Location: forkAddSavePhaseName,
		Summary:  terminalForkAddOutcomeDocument("Unable to save provider instance", terminalexperience.VisualRoleError),
	}); !reflect.DeepEqual(got, want) {
		t.Fatalf("save failure Finish request = %#v, want %#v", got, want)
	}
	if got := terminalForkAddFinishRequest(terminalexperience.Failed, "unsafe\nlocation"); got.Location != "" || terminalexperience.RenderPlain(got.Summary) != "Unable to collect provider details\n" {
		t.Fatalf("invalid failure location request = %#v", got)
	}
}

type forkAddWriterFunc func(string, appconfig.ForkInput) error

func (function forkAddWriterFunc) SaveForkInstance(name string, input appconfig.ForkInput) error {
	return function(name, input)
}

type cancelAfterForkAddLines struct {
	reader   *strings.Reader
	cancel   context.CancelFunc
	lines    int
	cancelAt int
}

func (reader *cancelAfterForkAddLines) Read(value []byte) (int, error) {
	if len(value) == 0 {
		return 0, nil
	}
	n, err := reader.reader.Read(value[:1])
	if n == 1 && value[0] == '\n' {
		reader.lines++
		if reader.lines == reader.cancelAt {
			reader.cancel()
		}
	}
	return n, err
}

type panicForkAddReader struct{}

func (panicForkAddReader) Read([]byte) (int, error) {
	panic("config fork add attempted to read Automation input")
}
