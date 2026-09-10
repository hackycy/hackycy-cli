package cm

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdCMParsesLegacyFlagMatrix(t *testing.T) {
	var inputs []Input
	for _, arguments := range [][]string{
		{},
		{"--profile", "work", "--timeout-ms", "0x3e8", "-l", "zh", "-S", "-s", "-a", "-d", "-b"},
		{"--push"},
		{"--stage-push=upstream"},
	} {
		command := NewCmdCM(newCMTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(options *Options) error {
			inputs = append(inputs, options.Input)
			return nil
		})
		command.SetArgs(arguments)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v ExecuteContext() error = %v", arguments, err)
		}
	}

	if len(inputs) != 4 {
		t.Fatalf("inputs = %#v", inputs)
	}
	if got := inputs[0]; got != (Input{Language: "en"}) {
		t.Fatalf("default input = %#v", got)
	}
	if got := inputs[1]; got.Profile != "work" || got.TimeoutMS == nil || *got.TimeoutMS != 1000 || got.Language != "zh" || !got.Staged || !got.Stage || !got.StageAll || !got.DryRun || !got.Body {
		t.Fatalf("full input = %#v", got)
	}
	if got := inputs[2]; got.Push == nil || *got.Push != "origin" || got.StagePush != nil {
		t.Fatalf("bare push input = %#v", got)
	}
	if got := inputs[3]; got.Push != nil || got.StagePush == nil || *got.StagePush != "upstream" {
		t.Fatalf("stage push input = %#v", got)
	}
}

func TestNormalizeArgumentsPreservesLegacyOptionalRemoteForms(t *testing.T) {
	var inputs []Input
	for _, arguments := range [][]string{
		{"git", "cm", "--push", "upstream"},
		{"git", "cm", "-p", "upstream"},
		{"git", "cm", "-pupstream"},
		{"git", "cm", "--stage-push", "publish"},
		{"git", "cm", "-cpublish"},
		{"git", "cm", "-p=upstream"},
		{"git", "cm", "--push", "-d"},
	} {
		command := NewCmdCM(newCMTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(options *Options) error {
			inputs = append(inputs, options.Input)
			return nil
		})
		normalized := NormalizeArguments(arguments)
		command.SetArgs(normalized[2:])
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v normalized to %v, ExecuteContext() error = %v", arguments, normalized, err)
		}
	}

	if got, want := inputs, []Input{
		{Language: "en", Push: stringPointer("upstream")},
		{Language: "en", Push: stringPointer("upstream")},
		{Language: "en", Push: stringPointer("upstream")},
		{Language: "en", StagePush: stringPointer("publish")},
		{Language: "en", StagePush: stringPointer("publish")},
		{Language: "en", Push: stringPointer("=upstream")},
		{Language: "en", Push: stringPointer("origin"), DryRun: true},
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("inputs = %#v, want %#v", got, want)
	}
}

func TestNewCmdCMRejectsInvalidTimeoutBeforeRunner(t *testing.T) {
	calls := 0
	for _, value := range []string{"999", "1.5", "not-a-number", "9007199254740992"} {
		command := NewCmdCM(newCMTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
			calls++
			return nil
		})
		command.SetArgs([]string{"--timeout-ms", value})
		err := command.ExecuteContext(context.Background())
		want := "'" + value + "' is not a valid timeout in milliseconds. Use an integer greater than or equal to 1000."
		if err == nil || err.Error() != want {
			t.Fatalf("%q ExecuteContext() error = %v, want %q", value, err, want)
		}
	}
	if calls != 0 {
		t.Fatalf("runner calls = %d", calls)
	}
}

func TestParseTimeoutMSMatchesStrictJavaScriptNumberSemantics(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  float64
	}{
		{value: "1000", want: 1000},
		{value: "1e3", want: 1000},
		{value: "0b1111101000", want: 1000},
		{value: "0o1750", want: 1000},
		{value: "\uFEFF1001", want: 1001},
		{value: "9007199254740991", want: 9007199254740991},
	} {
		got, err := parseTimeoutMS(testCase.value)
		if err != nil || got != testCase.want {
			t.Fatalf("parseTimeoutMS(%q) = (%v, %v), want (%v, nil)", testCase.value, got, err, testCase.want)
		}
	}
	for _, value := range []string{"", "999", "1.5", "Infinity", "NaN", "1000suffix", "9007199254740992"} {
		if _, err := parseTimeoutMS(value); err == nil {
			t.Fatalf("parseTimeoutMS(%q) error = nil", value)
		}
	}
	if _, err := parseTimeoutMS("1000.0"); err != nil {
		t.Fatalf("parseTimeoutMS decimal integer error = %v", err)
	}
	if _, err := parseTimeoutMS("-1000"); err == nil {
		t.Fatalf("parseTimeoutMS negative result = %v", err)
	}
}

func TestNormalizeArgumentsLeavesOtherCommandArgumentsUntouched(t *testing.T) {
	arguments := []string{"git", "pulse", "--push", "upstream"}
	if got := NormalizeArguments(arguments); !reflect.DeepEqual(got, arguments) {
		t.Fatalf("normalized arguments = %#v, want %#v", got, arguments)
	}
	arguments = []string{"git", "cm", "--", "--push", "upstream"}
	if got := NormalizeArguments(arguments); !reflect.DeepEqual(got, arguments) {
		t.Fatalf("normalized arguments after delimiter = %#v, want %#v", got, arguments)
	}
}

func TestNewCmdCMPropagatesTypedSignalError(t *testing.T) {
	want := cmExitError{code: 143}
	command := NewCmdCM(newCMTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
		return want
	})
	if err := command.ExecuteContext(context.Background()); err == nil || err.Error() != want.Error() {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func newCMTestFactory(output, diagnostics *bytes.Buffer) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}

type cmExitError struct {
	code int
}

func (err cmExitError) Error() string {
	return "git cm signal outcome"
}

func (err cmExitError) ExitCode() int {
	return err.code
}
