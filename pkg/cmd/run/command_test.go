package run

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdRunParsesLegacyArguments(t *testing.T) {
	testCases := []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{}},
		{arguments: []string{"project"}, want: "project"},
		{arguments: []string{"--flag"}, want: "--flag"},
		{arguments: []string{"project", "--log-level", "warn"}, want: "project"},
		{arguments: []string{"--log-level=warn", "other"}, want: "other"},
	}
	for _, testCase := range testCases {
		t.Run(strings.Join(testCase.arguments, " "), func(t *testing.T) {
			var options *Options
			command := NewCmdRun(newRunTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
				options = input
				return nil
			})
			command.SetArgs(testCase.arguments)
			if err := command.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("ExecuteContext() error = %v", err)
			}
			if options == nil || options.Directory != testCase.want {
				t.Fatalf("options = %#v, want directory %q", options, testCase.want)
			}
			if options.Context == nil || options.WorkingDirectory == nil || options.Terminal == nil || options.Reader == nil || options.Exists == nil || options.Runner == nil {
				t.Fatalf("Options has incomplete leaf dependencies: %#v", options)
			}
		})
	}
}

func TestNewCmdRunReservesLegacyHelpOptions(t *testing.T) {
	for _, arguments := range [][]string{{"--help"}, {"-h"}} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			output := &bytes.Buffer{}
			called := 0
			command := NewCmdRun(newRunTestFactory(output, &bytes.Buffer{}), func(*Options) error {
				called++
				return nil
			})
			command.SetOut(output)
			command.SetArgs(arguments)
			if err := command.ExecuteContext(context.Background()); err != nil || called != 0 || !strings.Contains(output.String(), "Run package.json scripts") {
				t.Fatalf("help execution = (%v, calls=%d, output=%q)", err, called, output.String())
			}
		})
	}
}

func TestNewCmdRunRetainsTheFrozenPassthroughRejectionMatrix(t *testing.T) {
	testCases := [][]string{
		{".", "--flag"},
		{"--flag", "value"},
		{"arg1", "arg2"},
		{"--", "arg1", "arg2"},
	}
	for _, arguments := range testCases {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			called := 0
			command := NewCmdRun(newRunTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
				called++
				return nil
			})
			command.SetArgs(arguments)
			err := command.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "accepts at most 1 arg(s)") || called != 0 {
				t.Fatalf("arguments %q execution = (%v, calls=%d)", arguments, err, called)
			}
		})
	}
}

func newRunTestFactory(output, diagnostics *bytes.Buffer) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}
