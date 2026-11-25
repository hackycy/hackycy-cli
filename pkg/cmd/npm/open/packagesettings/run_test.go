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
		name, input string
		batch, fail bool
		wantOpen    []int
		wantResult  []string
		wantError   string
	}{
		{name: "confirm requires a second enter", input: "\n", wantResult: []string{"Pending 3"}},
		{name: "continue by default", input: "\n\n\n\n", wantOpen: []int{0, 1, 2}, wantResult: []string{"Opened 3"}},
		{name: "subset in catalog order", input: "3,1,3\n\n\n", wantOpen: []int{0, 2}, wantResult: []string{"Opened 2", "Not selected 1", "Not selected  beta"}},
		{name: "stop after one", input: "\n\nn\n", wantOpen: []int{0}, wantResult: []string{"Opened 1", "Pending 2"}},
		{name: "cancel selection", input: "q\n", wantResult: []string{"Pending 3"}},
		{name: "empty selection", input: "none\n\n", wantResult: []string{"Not selected 3"}},
		{name: "invalid input reprompts", input: "4\n2\n\n", wantOpen: []int{1}, wantResult: []string{"Opened 1", "Not selected 2"}},
		{name: "failure still permits next", input: "\n\n\n\n", fail: true, wantOpen: []int{0, 1, 2}, wantResult: []string{"Opened 2", "Failed 1"}, wantError: "failed to open 1"},
		{name: "batch", batch: true, wantOpen: []int{0, 1, 2}},
		{name: "batch failures", batch: true, fail: true, wantOpen: []int{0, 1, 2}, wantError: "failed to open 1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			mode := terminal.PlainInteractive
			if test.batch {
				mode = terminal.Automation
			}
			options := testOptions(t, workspace, mode, test.input, &out, &diagnostics)
			options.Batch = test.batch
			const batchList = "Found 3 npm packages:\n  @example/a\n  beta\n  gamma\n"
			var requests []string
			options.OpenURL = func(_ context.Context, address string) error {
				want := ""
				if test.batch {
					want = batchList
				}
				if got := out.String(); got != want {
					t.Fatalf("stdout at open = %q, want %q", got, want)
				}
				requests = append(requests, address)
				if test.fail && address == addresses[1] {
					return errors.New("launcher failed")
				}
				return nil
			}
			err := runPackageSettings(options)
			if test.wantError == "" && err != nil || test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("runPackageSettings() error = %v, want %q", err, test.wantError)
			}
			var want []string
			for _, index := range test.wantOpen {
				want = append(want, addresses[index])
			}
			if !slices.Equal(requests, want) {
				t.Errorf("opener requests = %v, want %v", requests, want)
			}
			if test.batch && out.String() != batchList {
				t.Errorf("batch output = %q", out.String())
			}
			for _, status := range test.wantResult {
				if !strings.Contains(out.String(), status) {
					t.Errorf("result missing %q: %s", status, out.String())
				}
			}
			if test.fail && !strings.Contains(diagnostics.String(), addresses[1]) {
				t.Errorf("failure reason missing from stderr: %s", diagnostics.String())
			}
		})
	}
}

func TestPackageSettingsSingleEmptyAndInvalid(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
		input   string
		error   bool
		opens   int
	}{
		{name: "single", content: `{"name":"example"}`, input: "\n\n", opens: 1},
		{name: "single confirmation only", content: `{"name":"example"}`, input: "\n"},
		{name: "empty", content: `{"name":"example","private":true}`},
		{name: "invalid", content: `{`, error: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "package.json", test.content)
			var out, diagnostics bytes.Buffer
			options := testOptions(t, root, terminal.PlainInteractive, test.input, &out, &diagnostics)
			var opens int
			options.OpenURL = func(context.Context, string) error { opens++; return nil }
			err := runPackageSettings(options)
			if (err != nil) != test.error || opens != test.opens || (test.input == "" && diagnostics.Len() != 0) {
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

func TestPackageSettingsKeepsPendingRowsWhenLaunchIsInterrupted(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"first","workspaces":["second"]}`)
	writeFixture(t, root, "second/package.json", `{"name":"second"}`)
	var out, diagnostics bytes.Buffer
	options := testOptions(t, root, terminal.PlainInteractive, "\n\n\n", &out, &diagnostics)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	options.Context = ctx
	var requests []string
	options.OpenURL = func(ctx context.Context, address string) error {
		requests = append(requests, address)
		cancel()
		return ctx.Err()
	}
	if err := runPackageSettings(options); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted launch = %v, want context cancellation", err)
	}
	if !slices.Equal(requests, []string{settingsURL("first")}) || !strings.Contains(out.String(), "Opened 0 · Pending 2 · Failed 0") {
		t.Fatalf("interrupted launch requested %v, result %q", requests, out.String())
	}
}
