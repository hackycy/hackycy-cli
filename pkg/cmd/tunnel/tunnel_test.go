package tunnel

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdTunnelRegistersServerAndConnect(t *testing.T) {
	command := NewCmdTunnel(newTunnelTestFactory())
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetArgs([]string{"--help"})
	if err := command.Execute(); err != nil || !strings.Contains(output.String(), "server") || !strings.Contains(output.String(), "connect") {
		t.Fatalf("help execution = (%v, %q)", err, output.String())
	}
}

func newTunnelTestFactory() *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    &bytes.Buffer{},
			ErrOut: &bytes.Buffer{},
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}
