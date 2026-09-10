package root

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	commandSurfaceVersion = "0.0.0-dev"
	commandSurfaceDir     = "acceptance/testdata/command-surface"
)

// TestCommandSurface is the single frozen-surface comparison. Updating the
// files requires YCY_UPDATE_COMMAND_SURFACE=1; ordinary runs are read-only.
func TestCommandSurface(t *testing.T) {
	rootDir := commandSurfaceRepositoryRoot(t)
	artifacts, err := captureCommandSurface(t)
	if err != nil {
		t.Fatalf("capture command surface: %v", err)
	}
	if os.Getenv("YCY_UPDATE_COMMAND_SURFACE") == "1" {
		if err := writeCommandSurface(rootDir, artifacts); err != nil {
			t.Fatalf("write command surface: %v", err)
		}
		return
	}
	if err := compareCommandSurface(rootDir, artifacts); err != nil {
		t.Fatal(err)
	}
}

type commandSurfaceArtifacts struct {
	Manifest    []byte
	Help        map[string][]byte
	Completions map[string][]byte
}

type commandSurfaceManifest struct {
	SchemaVersion int                     `json:"schemaVersion"`
	Binary        string                  `json:"binary"`
	Version       string                  `json:"version"`
	Commands      []commandSurfaceCommand `json:"commands"`
}

type commandSurfaceCommand struct {
	Path       string               `json:"path"`
	Use        string               `json:"use"`
	Aliases    []string             `json:"aliases"`
	Hidden     bool                 `json:"hidden"`
	Deprecated string               `json:"deprecated"`
	Flags      []commandSurfaceFlag `json:"flags"`
}

type commandSurfaceFlag struct {
	Name         string `json:"name"`
	Shorthand    string `json:"shorthand,omitempty"`
	Type         string `json:"type"`
	Default      string `json:"default"`
	NoOptDefault string `json:"noOptDefault,omitempty"`
	Scope        string `json:"scope"`
}

func captureCommandSurface(t *testing.T) (commandSurfaceArtifacts, error) {
	app, err := newSurfaceApp()
	if err != nil {
		return commandSurfaceArtifacts{}, err
	}
	root := app.rootCommand()
	// Execute initializes these defaults lazily. Initialize them here so the
	// manifest describes the same command tree exposed by the real binary.
	root.InitDefaultHelpFlag()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	initializeSurfaceCommands(root)

	commands := make([]commandSurfaceCommand, 0)
	collectSurfaceCommands(root, nil, &commands)
	sort.Slice(commands, func(i, j int) bool { return commands[i].Path < commands[j].Path })
	manifest, err := json.MarshalIndent(commandSurfaceManifest{
		SchemaVersion: 1,
		Binary:        "ycy",
		Version:       commandSurfaceVersion,
		Commands:      commands,
	}, "", "  ")
	if err != nil {
		return commandSurfaceArtifacts{}, err
	}
	manifest = append(manifest, '\n')

	help := make(map[string][]byte, len(commands))
	for _, command := range commands {
		path := strings.Fields(command.Path)
		output, errOutput, outcome := executeSurfaceHelp(path[1:])
		if outcome.Code != 0 {
			return commandSurfaceArtifacts{}, fmt.Errorf("help for %q returned code %d: %v; stderr=%q", command.Path, outcome.Code, outcome.Err, errOutput)
		}
		if errOutput != "" {
			return commandSurfaceArtifacts{}, fmt.Errorf("help for %q wrote stderr: %q", command.Path, errOutput)
		}
		help[surfaceFileName(path)] = []byte(normalizeSurfaceText(output))
	}

	completions, err := captureSurfaceCompletions()
	if err != nil {
		return commandSurfaceArtifacts{}, err
	}
	return commandSurfaceArtifacts{Manifest: manifest, Help: help, Completions: completions}, nil
}

func initializeSurfaceCommands(command *cobra.Command) {
	command.InitDefaultHelpFlag()
	for _, child := range command.Commands() {
		initializeSurfaceCommands(child)
	}
}

func collectSurfaceCommands(command *cobra.Command, parent []string, result *[]commandSurfaceCommand) {
	path := append(append([]string(nil), parent...), command.Name())
	flags := make([]commandSurfaceFlag, 0)
	collectSurfaceFlags(&flags, command.LocalFlags(), "local")
	collectSurfaceFlags(&flags, command.InheritedFlags(), "inherited")
	sort.Slice(flags, func(i, j int) bool {
		if flags[i].Name != flags[j].Name {
			return flags[i].Name < flags[j].Name
		}
		return flags[i].Scope < flags[j].Scope
	})
	aliases := append([]string{}, command.Aliases...)
	*result = append(*result, commandSurfaceCommand{
		Path:       strings.Join(path, " "),
		Use:        command.Use,
		Aliases:    aliases,
		Hidden:     command.Hidden,
		Deprecated: command.Deprecated,
		Flags:      flags,
	})
	for _, child := range command.Commands() {
		collectSurfaceCommands(child, path, result)
	}
}

func collectSurfaceFlags(result *[]commandSurfaceFlag, flags *pflag.FlagSet, scope string) {
	flags.VisitAll(func(flag *pflag.Flag) {
		entry := commandSurfaceFlag{
			Name:      flag.Name,
			Shorthand: flag.Shorthand,
			Type:      flag.Value.Type(),
			Default:   flag.DefValue,
			Scope:     scope,
		}
		if flag.NoOptDefVal != "" {
			entry.NoOptDefault = flag.NoOptDefVal
		}
		*result = append(*result, entry)
	})
}

func executeSurfaceHelp(arguments []string) (stdout, stderr string, outcome Outcome) {
	output := &bytes.Buffer{}
	errOutput := &bytes.Buffer{}
	app, err := newSurfaceAppWithOutput(output, errOutput)
	if err != nil {
		return "", "", Outcome{Code: 1, Err: err}
	}
	outcome = app.Execute(context.Background(), append(arguments, "--help"))
	return output.String(), errOutput.String(), outcome
}

func captureSurfaceCompletions() (map[string][]byte, error) {
	app, err := newSurfaceApp()
	if err != nil {
		return nil, err
	}
	root := app.rootCommand()
	root.InitDefaultHelpFlag()
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	generators := map[string]func(io.Writer) error{
		"bash":       func(writer io.Writer) error { return root.GenBashCompletionV2(writer, true) },
		"zsh":        func(writer io.Writer) error { return root.GenZshCompletion(writer) },
		"fish":       func(writer io.Writer) error { return root.GenFishCompletion(writer, true) },
		"powershell": func(writer io.Writer) error { return root.GenPowerShellCompletionWithDesc(writer) },
	}
	result := make(map[string][]byte, len(generators))
	for name, generate := range generators {
		var output bytes.Buffer
		if err := generate(&output); err != nil {
			return nil, fmt.Errorf("generate %s completion: %w", name, err)
		}
		result[name] = []byte(normalizeSurfaceText(output.String()))
	}
	return result, nil
}

func normalizeSurfaceText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	for index := range lines {
		lines[index] = strings.TrimRight(lines[index], " \t")
	}
	return strings.Join(lines, "\n")
}

func writeCommandSurface(root string, artifacts commandSurfaceArtifacts) error {
	directory := filepath.Join(root, commandSurfaceDir)
	if err := os.MkdirAll(filepath.Join(directory, "help"), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(directory, "completions"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), artifacts.Manifest, 0o644); err != nil {
		return err
	}
	for name, content := range artifacts.Help {
		if err := os.WriteFile(filepath.Join(directory, "help", name+".txt"), content, 0o644); err != nil {
			return err
		}
	}
	for name, content := range artifacts.Completions {
		if err := os.WriteFile(filepath.Join(directory, "completions", name+".txt"), content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func compareCommandSurface(root string, artifacts commandSurfaceArtifacts) error {
	directory := filepath.Join(root, commandSurfaceDir)
	manifest, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return fmt.Errorf("read frozen manifest: %w", err)
	}
	if !bytes.Equal(manifest, artifacts.Manifest) {
		return errors.New("frozen command manifest differs; use YCY_UPDATE_COMMAND_SURFACE=1 only for an intentional initial capture")
	}
	if err := compareSurfaceFiles(filepath.Join(directory, "help"), artifacts.Help, ".txt"); err != nil {
		return fmt.Errorf("frozen help differs: %w", err)
	}
	if err := compareSurfaceFiles(filepath.Join(directory, "completions"), artifacts.Completions, ".txt"); err != nil {
		return fmt.Errorf("frozen completions differ: %w", err)
	}
	return nil
}

func compareSurfaceFiles(directory string, expected map[string][]byte, suffix string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	actualNames := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), suffix)
		actualNames[name] = struct{}{}
	}
	if len(actualNames) != len(expected) {
		return fmt.Errorf("file count = %d, want %d", len(actualNames), len(expected))
	}
	for name, content := range expected {
		if _, ok := actualNames[name]; !ok {
			return fmt.Errorf("missing %s", name+suffix)
		}
		actual, err := os.ReadFile(filepath.Join(directory, name+suffix))
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, content) {
			return fmt.Errorf("%s differs", name+suffix)
		}
	}
	return nil
}

func surfaceFileName(path []string) string {
	return strings.Join(path, "__")
}

func commandSurfaceRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("determine command-surface test location")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func newSurfaceApp() (*App, error) {
	return newSurfaceAppWithOutput(io.Discard, io.Discard)
}

func newSurfaceAppWithOutput(output, errOutput io.Writer) (*App, error) {
	return newTestApp(BuildInfo{Version: commandSurfaceVersion}, testDependencies{
		Out:               output,
		Err:               errOutput,
		Environment:       func(string) string { return "" },
		EnvironmentLookup: func(string) (string, bool) { return "", false },
		Logging:           logging.NewRuntime(logging.Options{Writer: errOutput}),
	})
}
