package connect

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdConnectPreservesExplicitFlagPresence(t *testing.T) {
	var options []*Options
	for _, arguments := range [][]string{
		{"--server", "http://control.example.test", "--token", "cli-token"},
		{"--server="},
		nil,
	} {
		command := NewCmdConnect(newConnectTestFactory(), func(option *Options) error {
			options = append(options, option)
			return nil
		})
		command.SetArgs(arguments)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%q ExecuteContext() error = %v", arguments, err)
		}
	}
	if len(options) != 3 {
		t.Fatalf("options = %#v", options)
	}
	if options[0] == nil || options[0].Context == nil || options[0].Input.Server == nil || *options[0].Input.Server != "http://control.example.test" || options[0].Input.Token == nil || *options[0].Input.Token != "cli-token" || options[0].ConfigStore == nil || options[0].Environment == nil || options[0].Terminal == nil || options[0].Now == nil {
		t.Fatalf("explicit options = %#v", options[0])
	}
	if options[1] == nil || options[1].Input.Server == nil || *options[1].Input.Server != "" || options[1].Input.Token != nil {
		t.Fatalf("explicit empty server options = %#v", options[1])
	}
	if options[2] == nil || options[2].Input.Server != nil || options[2].Input.Token != nil {
		t.Fatalf("absent options = %#v", options[2])
	}
}

func TestNewCmdConnectRejectsArgumentsBeforeRunning(t *testing.T) {
	calls := 0
	command := NewCmdConnect(newConnectTestFactory(), func(*Options) error {
		calls++
		return nil
	})
	command.SetArgs([]string{"unexpected"})
	err := command.ExecuteContext(context.Background())
	if err == nil || calls != 0 || !strings.Contains(err.Error(), "unknown command \"unexpected\" for \"connect\"") {
		t.Fatalf("execution = (%v, calls=%d)", err, calls)
	}
}

func TestNewCmdConnectPreservesLeafHelpWithoutRunning(t *testing.T) {
	output := &bytes.Buffer{}
	calls := 0
	command := NewCmdConnect(newConnectTestFactory(), func(*Options) error {
		calls++
		return nil
	})
	command.SetOut(output)
	command.SetArgs([]string{"--help"})
	if err := command.ExecuteContext(context.Background()); err != nil || calls != 0 || !strings.Contains(output.String(), "--server") || !strings.Contains(output.String(), "--token") || strings.Contains(output.String(), "--control-port") || strings.Contains(output.String(), "--log-level") {
		t.Fatalf("help execution = (%v, calls=%d, output=%q)", err, calls, output.String())
	}
}

func newConnectTestFactory() *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    &bytes.Buffer{},
			ErrOut: &bytes.Buffer{},
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}
