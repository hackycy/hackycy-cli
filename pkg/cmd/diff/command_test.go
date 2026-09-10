package diff

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

func TestNewCmdDiffPassesTypedInputAndLegacyDefaults(t *testing.T) {
	var options []*Options
	for _, arguments := range [][]string{
		{"baseline", "-x", "first", "--port", "00007", "target", "--exclude=second", "--public", "--no-gitignore"},
		{"before", "after"},
	} {
		command := NewCmdDiff(newDiffTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(input *Options) error {
			options = append(options, input)
			return nil
		})
		command.SetArgs(arguments)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%q ExecuteContext() error = %v", arguments, err)
		}
	}

	want := []Input{
		{
			BaselineDirectory: "baseline",
			TargetDirectory:   "target",
			Port:              7,
			Public:            true,
			Exclusions:        []string{"first", "second"},
			NoGitIgnore:       true,
		},
		{
			BaselineDirectory: "before",
			TargetDirectory:   "after",
			Port:              1205,
			Exclusions:        []string{},
		},
	}
	if len(options) != len(want) {
		t.Fatalf("options = %#v", options)
	}
	for index := range want {
		if options[index] == nil || options[index].Context == nil || options[index].Terminal == nil || options[index].NetworkInterfaces == nil || !reflect.DeepEqual(options[index].Input, want[index]) {
			t.Fatalf("option %d = %#v, want input %#v", index, options[index], want[index])
		}
	}
}

func TestNewCmdDiffRejectsInvalidPortsAndOperandCounts(t *testing.T) {
	invalidPorts := []struct {
		value string
		want  string
	}{
		{value: "-1", want: "'-1' is not a valid port"},
		{value: "+1", want: "'+1' is not a valid port"},
		{value: " 1", want: "' 1' is not a valid port"},
		{value: "1.0", want: "'1.0' is not a valid port"},
		{value: "0x50", want: "'0x50' is not a valid port"},
		{value: "\uff11", want: "'\uff11' is not a valid port"},
		{value: "65536", want: "Port must be between 0 and 65535"},
	}
	for _, testCase := range invalidPorts {
		t.Run(testCase.value, func(t *testing.T) {
			calls := 0
			command := NewCmdDiff(newDiffTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
				calls++
				return nil
			})
			command.SetArgs([]string{"--port=" + testCase.value, "baseline", "target"})
			err := command.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), testCase.want) || calls != 0 {
				t.Fatalf("port %q execution = (%v, calls=%d), want %q", testCase.value, err, calls, testCase.want)
			}
		})
	}

	for _, arguments := range [][]string{{"baseline"}, {"baseline", "target", "extra"}} {
		t.Run(strings.Join(arguments, " "), func(t *testing.T) {
			calls := 0
			command := NewCmdDiff(newDiffTestFactory(&bytes.Buffer{}, &bytes.Buffer{}), func(*Options) error {
				calls++
				return nil
			})
			command.SetArgs(arguments)
			err := command.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), "accepts 2 arg(s)") || calls != 0 {
				t.Fatalf("arguments %q execution = (%v, calls=%d)", arguments, err, calls)
			}
		})
	}
}

func TestNewCmdDiffPreservesHelpWithoutStartingTheLeaf(t *testing.T) {
	output := &bytes.Buffer{}
	calls := 0
	command := NewCmdDiff(newDiffTestFactory(output, &bytes.Buffer{}), func(*Options) error {
		calls++
		return nil
	})
	command.SetOut(output)
	command.SetArgs([]string{"--help"})
	if err := command.ExecuteContext(context.Background()); err != nil || calls != 0 || !strings.Contains(output.String(), "Compare two directories in a browser") || !strings.Contains(output.String(), "--port") || !strings.Contains(output.String(), "--public") || !strings.Contains(output.String(), "--exclude") || !strings.Contains(output.String(), "--no-gitignore") || strings.Contains(output.String(), "--address") {
		t.Fatalf("help execution = (%v, calls=%d, output=%q)", err, calls, output.String())
	}
}

func newDiffTestFactory(output, diagnostics *bytes.Buffer) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    output,
			ErrOut: diagnostics,
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}
