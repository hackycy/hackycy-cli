package cm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestGenerateCommitMessageCompilesEvidenceAndCleansValidatedOutput(t *testing.T) {
	snapshot := evidenceSnapshot([]SnapshotFile{evidenceFile("src/cm.go")})
	snapshot.ID = "snapshot-id"
	model := &scriptedCommitMessageModel{responses: []scriptedModelResponse{{output: ModelOutput{Content: "```markdown\n\"feat(cm): generate a message\"\n```", Usage: providerUsage(3, 2, 5)}}}}
	generated, err := GenerateCommitMessage(context.Background(), model, GenerationInput{Snapshot: snapshot, Language: "en"})
	if err != nil {
		t.Fatalf("GenerateCommitMessage() error = %v", err)
	}
	if generated.Message != "feat(cm): generate a message" || generated.SnapshotID != "snapshot-id" || generated.FileCount != 1 || generated.Usage == nil || generated.Evidence.EstimatedLocalPromptTokens == 0 {
		t.Fatalf("generated = %#v", generated)
	}
	if len(model.inputs) != 1 || !strings.Contains(model.inputs[0].System, "English only") || !strings.Contains(model.inputs[0].Evidence, "CHANGE_SUMMARY") {
		t.Fatalf("model inputs = %#v", model.inputs)
	}
}

func TestGenerateCommitMessageUsesDetailedChineseInstructionForLargerScope(t *testing.T) {
	snapshot := evidenceSnapshot([]SnapshotFile{evidenceFile("src/one.go"), evidenceFile("src/two.go"), evidenceFile("src/three.go")})
	model := &scriptedCommitMessageModel{responses: []scriptedModelResponse{{output: ModelOutput{Content: "feat(cm): message"}}}}
	_, err := GenerateCommitMessage(context.Background(), model, GenerationInput{Snapshot: snapshot, Language: "zh"})
	if err != nil {
		t.Fatalf("GenerateCommitMessage() error = %v", err)
	}
	if len(model.inputs) != 1 || !strings.Contains(model.inputs[0].System, "Chinese only") || !strings.Contains(model.inputs[0].System, "feat=new behavior") {
		t.Fatalf("system = %#v", model.inputs)
	}
}

func TestGenerateCommitMessageClassifiesNoChangesAndModelFailures(t *testing.T) {
	_, err := GenerateCommitMessage(context.Background(), &scriptedCommitMessageModel{}, GenerationInput{Snapshot: GitSnapshot{Scope: ScopeStaged}})
	assertCommandError(t, err, ErrorNoChanges, "No staged changes.")

	failure := errors.New("provider offline")
	_, err = GenerateCommitMessage(context.Background(), &scriptedCommitMessageModel{responses: []scriptedModelResponse{{err: failure}}}, GenerationInput{Snapshot: evidenceSnapshot([]SnapshotFile{evidenceFile("src/cm.go")})})
	if !errors.Is(err, failure) {
		t.Fatalf("GenerateCommitMessage() error = %v", err)
	}
	assertCommandError(t, err, ErrorModelUnavailable, "Unable to generate commit message: provider offline")
}

func TestGenerateCommitMessageRejectsInvalidSubjectsBodiesAndFences(t *testing.T) {
	snapshot := evidenceSnapshot([]SnapshotFile{evidenceFile("src/cm.go")})
	for _, testCase := range []struct {
		content     string
		includeBody bool
		want        string
	}{
		{content: "message without grammar", want: "Model output is not a valid Angular commit message."},
		{content: "feat(cm): subject\n\nbody", want: "Model output included a body when only a subject was requested."},
		{content: "feat(cm): subject\n```\nbody", includeBody: true, want: "Model output included a Markdown fence in the commit body."},
	} {
		model := &scriptedCommitMessageModel{responses: []scriptedModelResponse{{output: ModelOutput{Content: testCase.content}}}}
		_, err := GenerateCommitMessage(context.Background(), model, GenerationInput{Snapshot: snapshot, IncludeBody: testCase.includeBody})
		assertCommandError(t, err, ErrorInvalidModelOutput, testCase.want)
		if !strings.Contains(err.Error(), "Received model output") {
			t.Fatalf("error = %v", err)
		}
	}
}

type scriptedCommitMessageModel struct {
	inputs    []ModelInput
	responses []scriptedModelResponse
}

type scriptedModelResponse struct {
	output ModelOutput
	err    error
}

func (model *scriptedCommitMessageModel) Generate(_ context.Context, input ModelInput) (ModelOutput, error) {
	model.inputs = append(model.inputs, input)
	if len(model.responses) == 0 {
		return ModelOutput{}, errors.New("No scripted model response remaining.")
	}
	response := model.responses[0]
	model.responses = model.responses[1:]
	return response.output, response.err
}

func providerUsage(prompt, completion, total float64) *TokenUsage {
	return &TokenUsage{PromptTokens: &prompt, CompletionTokens: &completion, TotalTokens: &total}
}

func assertCommandError(t *testing.T, err error, code ErrorCode, contains string) {
	t.Helper()
	var commandError *CommandError
	if !errors.As(err, &commandError) || commandError.Code != code || !strings.Contains(commandError.Error(), contains) {
		t.Fatalf("error = %#v, want code %q containing %q", err, code, contains)
	}
}
