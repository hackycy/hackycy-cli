package root

import (
	"reflect"
	"testing"
)

func TestNormalizeDiagnosticAliasesPreservesGitHeatQueryShorthand(t *testing.T) {
	tests := []struct {
		arguments []string
		want      []string
	}{
		{arguments: []string{"-q", "export", "env"}, want: []string{"--quiet", "export", "env"}},
		{arguments: []string{"-vvq", "export", "env"}, want: []string{"-v", "-v", "--quiet", "export", "env"}},
		{arguments: []string{"git", "heat", "-q", "needle"}, want: []string{"git", "heat", "-q", "needle"}},
		{arguments: []string{"--log-level", "warn", "git", "heat", "-qneedle"}, want: []string{"--log-level", "warn", "git", "heat", "-qneedle"}},
	}
	for _, test := range tests {
		if got := normalizeDiagnosticAliases(test.arguments); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("normalizeDiagnosticAliases(%q) = %q, want %q", test.arguments, got, test.want)
		}
	}
}

func TestCollectDiagnosticControlsCountsOnlyDiagnosticGrammar(t *testing.T) {
	controls := collectDiagnosticControls([]string{
		"--log-level", "warn", "--log-format=json", "-v", "--quiet", "--", "--log-level", "error",
	})
	if !reflect.DeepEqual(controls, diagnosticControls{
		logLevels:    []string{"warn"},
		logFormats:   []string{"json"},
		verboseCount: 1,
		quietCount:   1,
	}) {
		t.Fatalf("controls = %#v", controls)
	}
}
