package pulse

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdPulseParsesCompatibilityInput(t *testing.T) {
	var options []*Options
	for _, arguments := range [][]string{
		{},
		{"workspace", "--days", "3oops"},
		{"--days=-1tail"},
		{"--days", "0"},
	} {
		command := NewCmdPulse(newPulseTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
			options = append(options, input)
			return nil
		})
		command.SetArgs(arguments)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v ExecuteContext() error = %v", arguments, err)
		}
	}

	want := []Input{
		{},
		{Directory: "workspace", Days: pulseInt(3)},
		{Days: pulseInt(-1)},
		{Days: pulseInt(0)},
	}
	if len(options) != len(want) {
		t.Fatalf("options = %#v", options)
	}
	for index, input := range want {
		if options[index].Directory != input.Directory || !samePulseInteger(options[index].Days, input.Days) {
			t.Fatalf("options[%d] = %#v, want %#v", index, options[index], input)
		}
		if options[index].Context == nil || options[index].WorkingDirectory == nil || options[index].Terminal == nil || options[index].Git == nil || options[index].Now == nil {
			t.Fatalf("options[%d] has incomplete leaf dependencies: %#v", index, options[index])
		}
	}
}

func TestNewCmdPulseRejectsInvalidArgumentsBeforeRunner(t *testing.T) {
	calls := 0
	for _, testCase := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"--days", "oops"}, want: "'oops' is not a valid integer"},
		{arguments: []string{"one", "two"}, want: "accepts at most 1 arg(s), received 2"},
		{arguments: []string{"--unknown"}, want: "unknown flag: --unknown"},
	} {
		command := NewCmdPulse(newPulseTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
			calls++
			return nil
		})
		command.SetArgs(testCase.arguments)
		err := command.ExecuteContext(context.Background())
		if err == nil || !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("%v ExecuteContext() error = %v, want %q", testCase.arguments, err, testCase.want)
		}
	}
	if calls != 0 {
		t.Fatalf("runner calls = %d", calls)
	}
}

func TestParseIntegerPreservesPermissiveDecimalPrefixes(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  int
	}{
		{value: "3oops", want: 3},
		{value: "  +12tail", want: 12},
		{value: "-0x1", want: 0},
	} {
		got, err := parseInteger(testCase.value)
		if err != nil || got != testCase.want {
			t.Fatalf("parseInteger(%q) = (%d, %v), want %d", testCase.value, got, err, testCase.want)
		}
	}
	for _, value := range []string{"", " ", "+", "-", "Infinity", "999999999999999999999999999999999"} {
		if _, err := parseInteger(value); err == nil {
			t.Fatalf("parseInteger(%q) error = nil", value)
		}
	}
}

func TestNewCmdPulsePreservesTypedSignalError(t *testing.T) {
	want := gitPulseExitError{code: 143}
	command := NewCmdPulse(newPulseTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
		return want
	})
	if err := command.ExecuteContext(context.Background()); err == nil || err.Error() != want.Error() {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func newPulseTestFactory(output, diagnostics *bytes.Buffer) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}

func pulseInt(value int) *int {
	return &value
}

func samePulseInteger(left, right *int) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

type gitPulseExitError struct {
	code int
}

func (err gitPulseExitError) Error() string {
	return "git pulse signal outcome"
}

func (err gitPulseExitError) ExitCode() int {
	return err.code
}
