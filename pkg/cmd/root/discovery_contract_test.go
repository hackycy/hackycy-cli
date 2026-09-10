package root

import (
	"context"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestDiscoveryAndParserContractsKeepAutomationStreamsSeparated(t *testing.T) {
	testCases := []struct {
		name       string
		arguments  []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "help",
			arguments:  []string{"--help"},
			wantCode:   0,
			wantStdout: "Usage:",
		},
		{
			name:       "version",
			arguments:  []string{"--version"},
			wantCode:   0,
			wantStdout: "0.0.0-dev\n",
		},
		{
			name:       "bash completion",
			arguments:  []string{"completion", "bash"},
			wantCode:   0,
			wantStdout: "__start_ycy",
		},
		{
			name:       "unknown command",
			arguments:  []string{"missing"},
			wantCode:   1,
			wantStderr: "error: unknown command 'missing'; Run 'ycy --help' for usage.\n",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			streams := terminaltest.NewRedirectedStreams("")
			app, err := newTestApp(BuildInfo{Version: "0.0.0-dev"}, testDependencies{
				Out:         streams.Stdout,
				Err:         streams.Stderr,
				Environment: func(string) string { return "" },
				Logging:     logging.NewRuntime(logging.Options{Writer: streams.Stderr}),
			})
			if err != nil {
				t.Fatalf("New returned an error: %v", err)
			}

			outcome := app.Execute(context.Background(), testCase.arguments)
			if outcome.Code != testCase.wantCode {
				t.Fatalf("outcome = %#v, want code %d", outcome, testCase.wantCode)
			}
			if testCase.wantStdout != "" && !strings.Contains(streams.Stdout.String(), testCase.wantStdout) {
				t.Fatalf("stdout = %q, want %q", streams.Stdout.String(), testCase.wantStdout)
			}
			if streams.Stderr.String() != testCase.wantStderr {
				t.Fatalf("stderr = %q, want %q", streams.Stderr.String(), testCase.wantStderr)
			}
			if terminaltest.ContainsTerminalControl(streams.Stdout.Bytes()) || terminaltest.ContainsTerminalControl(streams.Stderr.Bytes()) {
				t.Fatalf("Automation streams contain terminal control: stdout = %q stderr = %q", streams.Stdout.String(), streams.Stderr.String())
			}
		})
	}
}
