package cm

import "testing"

func TestResolveExecutionModePreservesTheLegacyFlagMatrix(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		input Input
		want  executionMode
	}{
		{
			name:  "default",
			input: Input{},
			want:  executionMode{Scope: ScopeAllUncommitted},
		},
		{
			name:  "dry run",
			input: Input{DryRun: true},
			want:  executionMode{Scope: ScopeAllUncommitted},
		},
		{
			name:  "staged commit",
			input: Input{Staged: true},
			want:  executionMode{Scope: ScopeStaged, CreateCommit: true, Interactive: true},
		},
		{
			name:  "staged dry run",
			input: Input{Staged: true, DryRun: true},
			want:  executionMode{Scope: ScopeStaged, Interactive: true},
		},
		{
			name:  "select files",
			input: Input{Stage: true},
			want:  executionMode{Scope: ScopeStaged, PromptStage: true, CreateCommit: true, Interactive: true},
		},
		{
			name:  "select files with staged is accepted",
			input: Input{Stage: true, Staged: true},
			want:  executionMode{Scope: ScopeStaged, PromptStage: true, CreateCommit: true, Interactive: true},
		},
		{
			name:  "stage all",
			input: Input{StageAll: true},
			want:  executionMode{Scope: ScopeStaged, StageAll: true, CreateCommit: true, Interactive: true},
		},
		{
			name:  "stage all dry run uses the frozen all uncommitted scope",
			input: Input{StageAll: true, DryRun: true},
			want:  executionMode{Scope: ScopeAllUncommitted, Interactive: true},
		},
		{
			name:  "stage push",
			input: Input{StagePush: modeString("publish")},
			want:  executionMode{Scope: ScopeStaged, PromptStage: true, CreateCommit: true, Push: true, PushRemote: "publish", Interactive: true},
		},
		{
			name:  "staged push",
			input: Input{Staged: true, Push: modeString("upstream")},
			want:  executionMode{Scope: ScopeStaged, CreateCommit: true, Push: true, PushRemote: "upstream", Interactive: true},
		},
		{
			name:  "stage all push",
			input: Input{StageAll: true, Push: modeString("origin")},
			want:  executionMode{Scope: ScopeStaged, StageAll: true, CreateCommit: true, Push: true, PushRemote: "origin", Interactive: true},
		},
		{
			name:  "stage push remote wins over push remote",
			input: Input{StagePush: modeString("stage-remote"), Push: modeString("push-remote")},
			want:  executionMode{Scope: ScopeStaged, PromptStage: true, CreateCommit: true, Push: true, PushRemote: "stage-remote", Interactive: true},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := resolveExecutionMode(testCase.input)
			if err != nil {
				t.Fatalf("resolveExecutionMode() error = %v", err)
			}
			if got != testCase.want {
				t.Fatalf("mode = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestResolveExecutionModeRejectsOnlyTheLegacyConflicts(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		input Input
		want  string
	}{
		{name: "stage and stage all", input: Input{Stage: true, StageAll: true}, want: "Use either --stage/--stage-push or --stage-all, not both."},
		{name: "stage push and stage all", input: Input{StagePush: modeString("origin"), StageAll: true}, want: "Use either --stage/--stage-push or --stage-all, not both."},
		{name: "stage and dry run", input: Input{Stage: true, DryRun: true}, want: "Use either --stage/--stage-push or --dry-run, not both."},
		{name: "stage push and dry run", input: Input{StagePush: modeString("origin"), DryRun: true}, want: "Use either --stage/--stage-push or --dry-run, not both."},
		{name: "push and dry run", input: Input{Staged: true, Push: modeString("origin"), DryRun: true}, want: "Use either --push/--stage-push or --dry-run, not both."},
		{name: "push alone", input: Input{Push: modeString("origin")}, want: "Use --push with --stage, --staged, or --stage-all."},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := resolveExecutionMode(testCase.input)
			if err == nil || err.Error() != testCase.want {
				t.Fatalf("resolveExecutionMode() error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestResolveExecutionModeTreatsEmptyOptionalValuesAsAbsent(t *testing.T) {
	mode, err := resolveExecutionMode(Input{StagePush: modeString(""), Push: modeString(""), DryRun: true})
	if err != nil {
		t.Fatalf("resolveExecutionMode() error = %v", err)
	}
	if mode != (executionMode{Scope: ScopeAllUncommitted}) {
		t.Fatalf("mode = %#v", mode)
	}
}

func TestRequiresInteractionMatchesTheEstablishedPromptPaths(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		input Input
		want  bool
	}{
		{name: "generation only", input: Input{}, want: false},
		{name: "dry run", input: Input{DryRun: true}, want: false},
		{name: "stage all dry run", input: Input{StageAll: true, DryRun: true}, want: false},
		{name: "file selection", input: Input{Stage: true}, want: true},
		{name: "commit confirmation", input: Input{Staged: true}, want: true},
		{name: "stage all confirmation", input: Input{StageAll: true}, want: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got, err := RequiresInteraction(testCase.input)
			if err != nil || got != testCase.want {
				t.Fatalf("RequiresInteraction(%#v) = (%t, %v), want (%t, nil)", testCase.input, got, err, testCase.want)
			}
		})
	}
	if _, err := RequiresInteraction(Input{Stage: true, StageAll: true}); err == nil {
		t.Fatal("RequiresInteraction() accepted an invalid flag combination")
	}
}

func modeString(value string) *string {
	return &value
}
