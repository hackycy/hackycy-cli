package fork

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdForkParsesItsTwoPositionalArguments(t *testing.T) {
	var options []*Options
	for _, arguments := range [][]string{
		{"owner/project"},
		{"fixture:group/project", "chosen"},
	} {
		command := NewCmdFork(newForkTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
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
	if options[0].Repository != "owner/project" || options[0].Destination != "" {
		t.Fatalf("default options = %#v", options[0])
	}
	if options[1].Repository != "fixture:group/project" || options[1].Destination != "chosen" {
		t.Fatalf("destination options = %#v", options[1])
	}
	for index, option := range options {
		if option.Context == nil || option.Config == nil || option.WorkingDirectory == nil || option.HTTP == nil || option.Terminal == nil || option.Git == nil {
			t.Fatalf("options[%d] has incomplete leaf dependencies: %#v", index, option)
		}
	}
}

func TestNewCmdForkRejectsInvalidArgumentsBeforeRunner(t *testing.T) {
	calls := 0
	for _, testCase := range []struct {
		arguments []string
		want      string
	}{
		{arguments: nil, want: "accepts between 1 and 2 arg(s), received 0"},
		{arguments: []string{"one", "two", "three"}, want: "accepts between 1 and 2 arg(s), received 3"},
		{arguments: []string{"owner/project", "--unknown"}, want: "unknown flag: --unknown"},
	} {
		command := NewCmdFork(newForkTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
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

func TestNewCmdForkPreservesTypedSignalError(t *testing.T) {
	want := gitForkExitError{code: 143}
	command := NewCmdFork(newForkTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
		return want
	})
	command.SetArgs([]string{"owner/project"})
	if err := command.ExecuteContext(context.Background()); err == nil || err.Error() != want.Error() {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
}

func newForkTestFactory(output, diagnostics *bytes.Buffer) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}

type gitForkExitError struct {
	code int
}

func (err gitForkExitError) Error() string {
	return "git fork signal outcome"
}

func (err gitForkExitError) ExitCode() int {
	return err.code
}
