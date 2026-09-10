package env

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

func TestNewCmdEnvParsesOptionsAndExposesTheRealLeaf(t *testing.T) {
	var options []*Options
	command := NewCmdEnv(newEnvTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
		options = append(options, input)
		return nil
	})
	command.SetArgs([]string{"project", "-e", "production", "--merge", "-o", "output.json"})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("ExecuteContext() error = %v", err)
	}

	want := []*Options{{
		Directory:   "project",
		Environment: "production",
		Merge:       true,
		Output:      "output.json",
	}}
	if len(options) != len(want) || !reflect.DeepEqual(options[0].Directory, want[0].Directory) || options[0].Environment != want[0].Environment || options[0].Merge != want[0].Merge || options[0].Output != want[0].Output {
		t.Fatalf("options = %#v, want %#v", options, want)
	}
	if options[0].Context == nil || options[0].WorkingDirectory == nil || options[0].Terminal == nil || options[0].Reader == nil || options[0].Writer == nil {
		t.Fatalf("Options has incomplete leaf dependencies: %#v", options[0])
	}

	output := &bytes.Buffer{}
	errors := &bytes.Buffer{}
	command = NewCmdEnv(newEnvTestFactory(output, errors), nil)
	command.SetOut(output)
	command.SetErr(errors)
	command.SetArgs([]string{"--help"})
	if err := command.ExecuteContext(context.Background()); err != nil || !strings.Contains(output.String(), "--env") || !strings.Contains(output.String(), "--merge") || !strings.Contains(output.String(), "--out") {
		t.Fatalf("help execution = (%v, %q)", err, output.String())
	}
}

func newEnvTestFactory(output, diagnostics *bytes.Buffer) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}
