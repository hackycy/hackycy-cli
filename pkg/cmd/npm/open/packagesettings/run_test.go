package packagesettings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
)

func TestPackageSettingsArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"--batch"}, {"directory"}} {
		var options *Options
		command := NewCmdPackageSettings(&cmdutil.Factory{
			WorkingDirectory: func() (string, error) { return ".", nil },
			IOStreams:        cmdutil.IOStreams{Out: io.Discard},
			Terminal:         terminal.NewExperience(terminal.ExperienceOptions{}),
		}, func(actual *Options) error {
			options = actual
			return nil
		})
		command.SetArgs(args)
		if _, err := command.ExecuteC(); slices.Equal(args, []string{"directory"}) {
			if err == nil || options != nil {
				t.Fatalf("Execute(%q) = %v, options %v; expected an argument error", args, err, options)
			}
		} else if err != nil || options == nil || options.Batch != (len(args) != 0) {
			t.Fatalf("Execute(%q) = %v, options %v", args, err, options)
		}
	}
}

func TestPackageSettingsOpeningSequence(t *testing.T) {
	workspace := t.TempDir()
	writeFixture(t, workspace, "package.json", `{"private":true,"workspaces":["packages/*"]}`)
	writeFixture(t, workspace, "packages/a/package.json", `{"name":"@example/a"}`)
	writeFixture(t, workspace, "packages/b/package.json", `{"name":"beta"}`)
	writeFixture(t, workspace, "packages/c/package.json", `{"name":"gamma"}`)

	addresses := []string{
		"https://www.npmjs.com/package/@example/a/access",
		"https://www.npmjs.com/package/beta/access",
		"https://www.npmjs.com/package/gamma/access",
	}
	for _, test := range []struct {
		name      string
		input     string
		mode      terminal.InteractionMode
		batch     bool
		fail      bool
		wantOpen  int
		wantAsk   int
		wantError string
	}{
		{name: "continue by default", input: "\n\n", mode: terminal.PlainInteractive, wantOpen: 3, wantAsk: 2},
		{name: "stop", input: "n\n", mode: terminal.PlainInteractive, wantOpen: 1, wantAsk: 1},
		{name: "dismiss", mode: terminal.PlainInteractive, wantOpen: 1, wantAsk: 1},
		{name: "interactive failure", input: "y\n\n", mode: terminal.PlainInteractive, fail: true, wantOpen: 2, wantAsk: 1, wantError: addresses[1]},
		{name: "noninteractive", mode: terminal.Automation, wantError: "use --batch"},
		{name: "batch", mode: terminal.Automation, batch: true, wantOpen: 3},
		{name: "batch failures", mode: terminal.Automation, batch: true, fail: true, wantOpen: 3, wantError: "failed to open 2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			options := testOptions(t, workspace, test.mode, test.input, &out, &diagnostics)
			options.Batch = test.batch
			var requests []string
			options.OpenURL = func(_ context.Context, address string) error {
				if got, want := out.String(), "Found 3 npm packages:\n  @example/a\n  beta\n  gamma\n"; got != want {
					t.Fatalf("stdout at open = %q, want %q", got, want)
				}
				requests = append(requests, address)
				if test.fail && address != addresses[0] {
					return errors.New("launcher failed")
				}
				return nil
			}
			err := runPackageSettings(options)
			if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("runPackageSettings() error = %v, want %q", err, test.wantError)
			}
			if !slices.Equal(requests, addresses[:test.wantOpen]) {
				t.Errorf("opener requests = %v, want %v", requests, addresses[:test.wantOpen])
			}
			if got := strings.Count(diagnostics.String(), "Enter to continue"); got != test.wantAsk {
				t.Errorf("got %d prompts, want %d: %q", got, test.wantAsk, diagnostics.String())
			}
			if test.batch && test.fail {
				for _, address := range addresses[1:] {
					if !strings.Contains(diagnostics.String(), address) {
						t.Errorf("batch failures are missing %q: %s", address, diagnostics.String())
					}
				}
			}
		})
	}
}

func TestPackageSettingsSingleEmptyAndInvalid(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		error   bool
		opens   int
	}{
		{name: "single", content: `{"name":"example"}`, opens: 1},
		{name: "empty", content: `{"name":"example","private":true}`},
		{name: "invalid", content: `{`, error: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "package.json", test.content)
			var out, diagnostics bytes.Buffer
			options := testOptions(t, root, terminal.PlainInteractive, "", &out, &diagnostics)
			var opens int
			options.OpenURL = func(context.Context, string) error { opens++; return nil }
			err := runPackageSettings(options)
			if (err != nil) != test.error || opens != test.opens || diagnostics.Len() != 0 {
				t.Fatalf("runPackageSettings() = %v, %d opens, diagnostics %q", err, opens, diagnostics.String())
			}
			if test.name == "empty" && out.String() != "No npm packages to open.\n" {
				t.Fatalf("empty result = %q", out.String())
			}
		})
	}
}

func TestPackageSettingsStopsBatchAfterCancellation(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"first","workspaces":["second"]}`)
	writeFixture(t, root, "second/package.json", `{"name":"second"}`)
	options := testOptions(t, root, terminal.Automation, "", io.Discard, io.Discard)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	options.Context = ctx
	options.Batch = true
	var opens int
	options.OpenURL = func(context.Context, string) error {
		opens++
		cancel()
		return nil
	}
	if err := runPackageSettings(options); !errors.Is(err, context.Canceled) || opens != 1 {
		t.Fatalf("canceled batch = %v, opened %d package(s)", err, opens)
	}
}

func TestPackageSettingsWritesListBeforeOpening(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"example"}`)
	options := testOptions(t, root, terminal.Automation, "", errorOutput{}, io.Discard)
	options.Batch = true
	options.OpenURL = func(context.Context, string) error {
		t.Fatal("opened browser before writing list successfully")
		return nil
	}
	if err := runPackageSettings(options); err == nil || !strings.Contains(err.Error(), "write package list") {
		t.Fatalf("runPackageSettings() = %v, want output failure", err)
	}
}

type errorOutput struct{}

func (errorOutput) Write([]byte) (int, error) { return 0, fmt.Errorf("output unavailable") }

func testOptions(t *testing.T, directory string, mode terminal.InteractionMode, input string, output, diagnostics io.Writer) *Options {
	return &Options{
		Context:          t.Context(),
		WorkingDirectory: func() (string, error) { return directory, nil },
		Output:           output,
		Terminal: terminal.NewExperience(terminal.ExperienceOptions{
			Capabilities: terminal.Capabilities{Interaction: mode},
			Input:        strings.NewReader(input), Output: output, Diagnostics: diagnostics,
		}),
	}
}
