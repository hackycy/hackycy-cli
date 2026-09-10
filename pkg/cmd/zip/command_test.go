package zip

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdZIPParsesLegacyFlagsAndDefaults(t *testing.T) {
	testCases := []struct {
		arguments []string
		want      Options
	}{
		{arguments: []string{"project", "-w", "-d", "../unsafe"}, want: Options{Directory: "project", Open: false, WithDir: "../unsafe"}},
		{arguments: []string{}, want: Options{Open: true}},
	}
	for _, testCase := range testCases {
		t.Run(strings.Join(testCase.arguments, " "), func(t *testing.T) {
			var options *Options
			command := NewCmdZIP(newZIPTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
				options = input
				return nil
			})
			command.SetArgs(testCase.arguments)
			if err := command.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("ExecuteContext() error = %v", err)
			}
			if options == nil || options.Directory != testCase.want.Directory || options.Open != testCase.want.Open || options.WithDir != testCase.want.WithDir {
				t.Fatalf("options = %#v, want %#v", options, testCase.want)
			}
			if options.Context == nil || options.Terminal == nil {
				t.Fatalf("Options has incomplete leaf dependencies: %#v", options)
			}
		})
	}
}

func TestNewCmdZIPRejectsExtraOperandsAndPreservesHelp(t *testing.T) {
	output, diagnostics := &bytes.Buffer{}, &bytes.Buffer{}
	called := 0
	command := NewCmdZIP(newZIPTestFactory(output, diagnostics), func(*Options) error {
		called++
		return nil
	})
	command.SetArgs([]string{"one", "two"})
	err := command.ExecuteContext(context.Background())
	if err == nil || !strings.Contains(err.Error(), "accepts at most 1 arg(s)") || called != 0 {
		t.Fatalf("extra operands execution = (%v, calls=%d)", err, called)
	}

	output.Reset()
	command = NewCmdZIP(newZIPTestFactory(output, diagnostics), nil)
	command.SetOut(output)
	command.SetArgs([]string{"--help"})
	if err := command.ExecuteContext(context.Background()); err != nil || !strings.Contains(output.String(), "Zip a directory into a zip file") || !strings.Contains(output.String(), "--without-open") || !strings.Contains(output.String(), "--with-dir") {
		t.Fatalf("help execution = (%v, %q)", err, output.String())
	}
}

func newZIPTestFactory(output, diagnostics *bytes.Buffer) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}
