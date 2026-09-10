package rm

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdRMParsesOptionsAndLegacyDepthPrefixes(t *testing.T) {
	var options []*Options
	command := NewCmdRM(newRMTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
		options = append(options, input)
		return nil
	})
	command.SetArgs([]string{"--force", "--depth", "3ignored", "one", "two"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	depth := 3
	want := []*Options{{
		Paths: []string{"one", "two"},
		Force: true,
		Depth: &depth,
	}}
	if len(options) != len(want) || !reflect.DeepEqual(options[0].Paths, want[0].Paths) || options[0].Force != want[0].Force || options[0].Depth == nil || *options[0].Depth != *want[0].Depth {
		t.Fatalf("options = %#v, want %#v", options, want)
	}
	if options[0].Context == nil || options[0].WorkingDirectory == nil || options[0].Terminal == nil || options[0].Remover == nil {
		t.Fatalf("Options has incomplete leaf dependencies: %#v", options[0])
	}

	command = NewCmdRM(newRMTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
		options = append(options, input)
		return nil
	})
	command.SetArgs([]string{"--depth=-2"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}
	if len(options) != 2 || options[1].Depth == nil || *options[1].Depth != -2 {
		t.Fatalf("negative-depth options = %#v", options)
	}
}

func TestNewCmdRMRejectsInvalidDepthAndExposesTheRealLeaf(t *testing.T) {
	output := &bytes.Buffer{}
	errors := &bytes.Buffer{}
	called := 0
	command := NewCmdRM(newRMTestFactory(output, errors), func(*Options) error {
		called++
		return nil
	})
	command.SetOut(output)
	command.SetErr(errors)
	command.SetArgs([]string{"--depth", "not-a-number"})
	if err := command.ExecuteContext(context.Background()); err == nil || err.Error() != "'not-a-number' is not a valid integer" || called != 0 {
		t.Fatalf("invalid-depth execution = (%v, calls=%d)", err, called)
	}

	output.Reset()
	errors.Reset()
	command = NewCmdRM(newRMTestFactory(output, errors), nil)
	command.SetOut(output)
	command.SetErr(errors)
	command.SetArgs([]string{"--help"})
	if err := command.ExecuteContext(context.Background()); err != nil || !strings.Contains(output.String(), "--force") || !strings.Contains(output.String(), "--depth") {
		t.Fatalf("help execution = (%v, %q)", err, output.String())
	}
}

func newRMTestFactory(output, diagnostics *bytes.Buffer) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}
