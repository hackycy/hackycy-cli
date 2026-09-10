package heat

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdHeatParsesCompatibilityInput(t *testing.T) {
	var options *Options
	command := NewCmdHeat(newHeatTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
		options = input
		return nil
	})
	command.SetArgs([]string{
		"--limit", "3oops",
		"--type", "dirs",
		"--sort", "count",
		"--relative-time",
		"--query", "  Api  ",
	})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if options == nil || options.Limit == nil || *options.Limit != 3 || options.Days != nil {
		t.Fatalf("range options = %#v", options)
	}
	if options.Target != TargetDirectories || options.Sort != SortCount || !options.RelativeTime || options.Query != "  Api  " {
		t.Fatalf("options = %#v", options)
	}
	if options.Context == nil || options.Terminal == nil || options.Git == nil || options.Now == nil {
		t.Fatalf("Options has incomplete leaf dependencies: %#v", options)
	}
}

func TestNewCmdHeatAppliesDefaultsAndSupportsDays(t *testing.T) {
	var options []*Options
	for _, arguments := range [][]string{
		{},
		{"-d", "+2tail", "-t", "files", "-s", "path"},
	} {
		command := NewCmdHeat(newHeatTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
			options = append(options, input)
			return nil
		})
		command.SetArgs(arguments)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%v ExecuteContext() error = %v", arguments, err)
		}
	}
	if len(options) != 2 {
		t.Fatalf("options = %#v", options)
	}
	if options[0].Limit != nil || options[0].Days != nil || options[0].Target != TargetDirectories || options[0].Sort != SortPath {
		t.Fatalf("defaults = %#v", options[0])
	}
	if options[1].Days == nil || *options[1].Days != 2 || options[1].Target != TargetFiles || options[1].Sort != SortPath {
		t.Fatalf("days options = %#v", options[1])
	}
}

func TestNewCmdHeatRejectsInvalidFlagsBeforeRunner(t *testing.T) {
	calls := 0
	for _, testCase := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"-n", "oops"}, want: "'oops' is not a valid integer"},
		{arguments: []string{"-t", "directory"}, want: "'directory' is not a valid report type. Use files or directories."},
		{arguments: []string{"-s", "date"}, want: "'date' is not a valid sort. Use count or path."},
		{arguments: []string{"unexpected"}, want: "unknown command \"unexpected\" for \"heat\""},
	} {
		command := NewCmdHeat(newHeatTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
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

func TestNewCmdHeatRetainsBothRangesForTheModuleToReject(t *testing.T) {
	var options *Options
	command := NewCmdHeat(newHeatTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
		options = input
		return nil
	})
	command.SetArgs([]string{"-n", "1", "-d", "2"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if options == nil || options.Limit == nil || options.Days == nil || *options.Limit != 1 || *options.Days != 2 {
		t.Fatalf("options = %#v", options)
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
	for _, value := range []string{"", " ", "+", "-", "Infinity", "999999999999999999999999999999"} {
		if _, err := parseInteger(value); err == nil {
			t.Fatalf("parseInteger(%q) error = nil", value)
		}
	}
}

func TestNewCmdHeatPreservesTypedSignalError(t *testing.T) {
	want := gitHeatExitError{code: 143}
	command := NewCmdHeat(newHeatTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
		return want
	})
	if err := command.ExecuteContext(context.Background()); err == nil || err.Error() != want.Error() {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func newHeatTestFactory(output, diagnostics *bytes.Buffer) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}

type gitHeatExitError struct {
	code int
}

func (err gitHeatExitError) Error() string {
	return "git heat signal outcome"
}

func (err gitHeatExitError) ExitCode() int {
	return err.code
}
