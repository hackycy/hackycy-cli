package upgrade

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdUpgradePassesFactoryFactsAndNoArguments(t *testing.T) {
	var options *Options
	command := NewCmdUpgrade(newUpgradeTestFactory("1.0.0"), func(input *Options) error {
		options = input
		return nil
	})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if options == nil || options.Context == nil || options.Terminal == nil || options.CurrentVersion != "1.0.0" {
		t.Fatalf("options = %#v", options)
	}

	called := false
	command = NewCmdUpgrade(newUpgradeTestFactory("1.0.0"), func(*Options) error {
		called = true
		return nil
	})
	command.SetArgs([]string{"extra"})
	if err := command.ExecuteContext(context.Background()); err == nil || called {
		t.Fatalf("argument execution = (%v, called=%t)", err, called)
	}
}

func TestNewCmdUpgradePreservesTypedErrorAndHelp(t *testing.T) {
	command := NewCmdUpgrade(newUpgradeTestFactory("1.0.0"), func(*Options) error {
		return &testExitError{code: 0, err: errors.New("fixture abort")}
	})
	if err := command.ExecuteContext(context.Background()); err == nil || err.Error() != "fixture abort" {
		t.Fatalf("typed error = %v", err)
	}

	output := &bytes.Buffer{}
	called := false
	command = NewCmdUpgrade(newUpgradeTestFactory("1.0.0"), func(*Options) error {
		called = true
		return nil
	})
	command.SetOut(output)
	command.SetArgs([]string{"--help"})
	if err := command.ExecuteContext(context.Background()); err != nil || called || !strings.Contains(output.String(), "Update ycy to the latest release") || strings.Contains(output.String(), "--log-level") {
		t.Fatalf("help execution = (%v, called=%t, output=%q)", err, called, output.String())
	}

	command = NewCmdUpgrade(nil, nil)
	if err := command.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "upgrade Factory is incomplete") {
		t.Fatalf("nil factory execution = %v", err)
	}
}

type testExitError struct {
	code int
	err  error
}

func (err *testExitError) Error() string { return err.err.Error() }
func (err *testExitError) ExitCode() int { return err.code }

func newUpgradeTestFactory(version string) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: version,
		IOStreams: cmdutil.IOStreams{
			Out:    &bytes.Buffer{},
			ErrOut: &bytes.Buffer{},
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}
