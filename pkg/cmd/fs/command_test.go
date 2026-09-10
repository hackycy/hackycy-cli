package fs

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	commandfactory "github.com/hackycy/hackycy-cli/pkg/cmd/factory"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestNewCmdFSPassesTypedInputAndEnvironmentPrecedence(t *testing.T) {
	environment := map[string]string{
		"YCY_FS_SESSION_DIR":           "environment-sessions",
		"YCY_FS_SESSION_IDLE_DAYS":     "3",
		"YCY_FS_CHUNKED_UPLOAD":        "true",
		"YCY_FS_UPLOAD_CHUNK_SIZE_MIB": "4",
	}
	var options []*Options
	for _, arguments := range [][]string{
		{
			"--port", "00000", "--address", "127.0.0.1", "--manage", "--safe-html",
			"--account", "Alice:password", "--account=Bob:password", "--session-dir", "selected-sessions",
			"--session-idle-days", "2", "--chunked-upload=false", "--upload-chunk-size", "16", "chosen-directory",
		},
		{"environment-directory"},
	} {
		command := NewCmdFS(newFSTestFactory(environment), func(option *Options) error {
			options = append(options, option)
			return nil
		})
		command.SetArgs(arguments)
		if err := command.ExecuteContext(context.Background()); err != nil {
			t.Fatalf("%q ExecuteContext() error = %v", arguments, err)
		}
	}
	want := []Input{
		{
			Directory:          "chosen-directory",
			Address:            "127.0.0.1",
			ManagementEnabled:  true,
			SafeHTML:           true,
			Accounts:           []string{"Alice:password", "Bob:password"},
			SessionDirectory:   "selected-sessions",
			SessionIdleTimeout: 2 * 24 * time.Hour,
			UploadChunkSize:    16 * 1024 * 1024,
		},
		{
			Directory:          "environment-directory",
			Port:               1204,
			Address:            "0.0.0.0",
			SessionDirectory:   "environment-sessions",
			SessionIdleTimeout: 3 * 24 * time.Hour,
			ChunkedUploads:     true,
			UploadChunkSize:    4 * 1024 * 1024,
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

func TestNewCmdFSRejectsInvalidConfigurationBeforeInvokingTheLeaf(t *testing.T) {
	for _, testCase := range []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"--port=-1"}, want: "'-1' is not a valid port"},
		{arguments: []string{"--port=65536"}, want: "Port must be between 0 and 65535"},
		{arguments: []string{"--session-idle-days=0"}, want: "'0' is not a valid positive session idle day count"},
		{arguments: []string{"--session-idle-days=999999999999"}, want: "'999999999999' is not a valid positive session idle day count"},
		{arguments: []string{"--upload-chunk-size=3"}, want: "'3' is not a valid upload chunk size; use 4-16 MiB"},
		{arguments: []string{"first", "second"}, want: "accepts at most 1 arg(s)"},
	} {
		t.Run(strings.Join(testCase.arguments, " "), func(t *testing.T) {
			calls := 0
			command := NewCmdFS(newFSTestFactory(nil), func(*Options) error {
				calls++
				return nil
			})
			command.SetArgs(testCase.arguments)
			err := command.ExecuteContext(context.Background())
			if err == nil || calls != 0 || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("execution = (%v, calls=%d), want %q", err, calls, testCase.want)
			}
		})
	}

	calls := 0
	command := NewCmdFS(newFSTestFactory(map[string]string{"YCY_FS_CHUNKED_UPLOAD": "yes"}), func(*Options) error {
		calls++
		return nil
	})
	command.SetArgs(nil)
	err := command.ExecuteContext(context.Background())
	if err == nil || calls != 0 || !strings.Contains(err.Error(), "'yes' is not a valid chunked-upload value") {
		t.Fatalf("environment execution = (%v, calls=%d)", err, calls)
	}
}

func TestNewCmdFSPreservesHelpWithoutStartingTheLeaf(t *testing.T) {
	output := &bytes.Buffer{}
	calls := 0
	command := NewCmdFS(newFSTestFactory(nil), func(*Options) error {
		calls++
		return nil
	})
	command.SetOut(output)
	command.SetArgs([]string{"--help"})
	if err := command.ExecuteContext(context.Background()); err != nil || calls != 0 || !strings.Contains(output.String(), "Browse a directory in a browser") || !strings.Contains(output.String(), "--account") || !strings.Contains(output.String(), "--upload-chunk-size") || strings.Contains(output.String(), "--public") {
		t.Fatalf("help execution = (%v, calls=%d, output=%q)", err, calls, output.String())
	}
}

func newFSTestFactory(environment map[string]string) *cmdutil.Factory {
	return commandfactory.New(commandfactory.Options{
		Version: "0.0.0-dev",
		IOStreams: cmdutil.IOStreams{
			Out:    &bytes.Buffer{},
			ErrOut: &bytes.Buffer{},
		},
		Environment: func(key string) string {
			return environment[key]
		},
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
	})
}
