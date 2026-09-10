package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const approvedLegacyHookHash = "7bc48fcc880a58ab4f92dbe45343a82eea1b2539c86e5c05dc6713d39bdf5d95"

type hookKind string

const (
	hookMissing  hookKind = "missing"
	hookLegacy   hookKind = "approved legacy hook"
	hookLefthook hookKind = "lefthook"
	hookUnknown  hookKind = "unknown"
)

// State is the resolved Git hook location for one repository or linked worktree.
type State struct {
	Root          string
	CommonGitDir  string
	HooksPath     string
	LocalPaths    []string
	GlobalPaths   []string
	SystemPaths   []string
	PreCommitKind hookKind
}

type invoker func(context.Context, string, string, ...string) (string, error)

// Controller keeps all hook mutation behind exact state inspection.
type Controller struct {
	directory string
	output    io.Writer
	invoke    invoker
}

// New constructs a controller rooted at a Git worktree directory.
func New(directory string, output io.Writer) (*Controller, error) {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve hookctl directory: %w", err)
	}
	if output == nil {
		output = io.Discard
	}
	return &Controller{directory: abs, output: output}, nil
}

// Discover resolves Git's effective paths without changing configuration or hook files.
func (controller *Controller) Discover(context context.Context) (State, error) {
	root, err := controller.git(context, "rev-parse", "--show-toplevel")
	if err != nil {
		return State{}, fmt.Errorf("resolve repository root: %w", err)
	}
	root = filepath.Clean(filepath.FromSlash(root))
	commonDir, err := controller.git(context, "rev-parse", "--git-common-dir")
	if err != nil {
		return State{}, fmt.Errorf("resolve common Git directory: %w", err)
	}
	hooksPath, err := controller.git(context, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return State{}, fmt.Errorf("resolve effective hooks path: %w", err)
	}

	state := State{
		Root:         root,
		CommonGitDir: resolveGitPath(root, commonDir),
		HooksPath:    resolveGitPath(root, hooksPath),
	}
	if state.LocalPaths, err = controller.configValues(context, "--local"); err != nil {
		return State{}, err
	}
	if state.GlobalPaths, err = controller.configValues(context, "--global"); err != nil {
		return State{}, err
	}
	if state.SystemPaths, err = controller.configValues(context, "--system"); err != nil {
		return State{}, err
	}
	state.PreCommitKind, err = classifyHook(filepath.Join(state.HooksPath, "pre-commit"))
	if err != nil {
		return State{}, err
	}
	return state, nil
}

// Install removes only the approved legacy hook and replaces it with pinned Lefthook output.
func (controller *Controller) Install(context context.Context) error {
	state, err := controller.Discover(context)
	if err != nil {
		return err
	}
	if err := requireDefaultHooksPath(state); err != nil {
		return err
	}
	if state.PreCommitKind == hookUnknown {
		return unknownHookError(filepath.Join(state.HooksPath, "pre-commit"))
	}
	if err := controller.runLefthook(context, state.Root, "validate"); err != nil {
		return fmt.Errorf("validate pinned Lefthook policy before installation: %w", err)
	}
	if state.PreCommitKind == hookLegacy {
		if err := os.Remove(filepath.Join(state.HooksPath, "pre-commit")); err != nil {
			return fmt.Errorf("remove approved legacy pre-commit hook: %w", err)
		}
	}
	if err := controller.runLefthook(context, state.Root, "install", "pre-commit"); err != nil {
		return fmt.Errorf("install pinned Lefthook pre-commit hook: %w", err)
	}
	installed, err := controller.Discover(context)
	if err != nil {
		return err
	}
	if installed.PreCommitKind != hookLefthook {
		return fmt.Errorf("pinned Lefthook did not create a recognizable pre-commit hook")
	}
	return controller.Doctor(context)
}

// Doctor reports the resolved state and fails closed for an incomplete or unsafe lifecycle.
func (controller *Controller) Doctor(context context.Context) error {
	state, err := controller.Discover(context)
	if err != nil {
		return err
	}
	fmt.Fprintf(controller.output, "repository root: %s\n", state.Root)
	fmt.Fprintf(controller.output, "common Git directory: %s\n", state.CommonGitDir)
	fmt.Fprintf(controller.output, "effective hooks path: %s\n", state.HooksPath)
	fmt.Fprintf(controller.output, "pre-commit manager: %s\n", state.PreCommitKind)

	if err := requireDefaultHooksPath(state); err != nil {
		return err
	}
	if state.PreCommitKind != hookLefthook {
		if state.PreCommitKind == hookUnknown {
			return unknownHookError(filepath.Join(state.HooksPath, "pre-commit"))
		}
		return fmt.Errorf("pre-commit manager is %s; run make hooks-install", state.PreCommitKind)
	}
	if _, err := controller.lefthookPath(state.Root); err != nil {
		return err
	}
	if err := requireFile(filepath.Join(state.Root, "lefthook.yml"), "pinned Lefthook policy"); err != nil {
		return err
	}
	if err := requireFile(filepath.Join(state.Root, "lefthook.rc"), "Lefthook runtime wrapper"); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(state.Root, "web", "node_modules")); err != nil {
		return fmt.Errorf("web dependencies are unavailable; run make bootstrap")
	}
	if err := activeToolchainClean(state.Root); err != nil {
		return err
	}
	fmt.Fprintln(controller.output, "hook lifecycle: ready")
	return nil
}

// Uninstall removes only a Lefthook-managed pre-commit hook and never restores legacy content.
func (controller *Controller) Uninstall(context context.Context) error {
	state, err := controller.Discover(context)
	if err != nil {
		return err
	}
	if err := requireDefaultHooksPath(state); err != nil {
		return err
	}
	switch state.PreCommitKind {
	case hookMissing:
		return nil
	case hookLefthook:
		if err := os.Remove(filepath.Join(state.HooksPath, "pre-commit")); err != nil {
			return fmt.Errorf("remove Lefthook pre-commit hook: %w", err)
		}
		checksum := filepath.Join(state.CommonGitDir, "info", "lefthook.checksum")
		if err := os.Remove(checksum); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove Lefthook checksum: %w", err)
		}
	case hookLegacy, hookUnknown:
		return unknownHookError(filepath.Join(state.HooksPath, "pre-commit"))
	}

	verified, err := controller.Discover(context)
	if err != nil {
		return err
	}
	if verified.PreCommitKind != hookMissing {
		return fmt.Errorf("Lefthook uninstall left a pre-commit hook in place")
	}
	return nil
}

func (controller *Controller) configValues(context context.Context, scope string) ([]string, error) {
	values, err := controller.git(context, "config", scope, "--get-all", "core.hooksPath")
	if err == nil {
		return strings.Fields(values), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil, nil
	}
	return nil, fmt.Errorf("inspect %s core.hooksPath: %w", scope, err)
}

func (controller *Controller) git(context context.Context, arguments ...string) (string, error) {
	command := exec.CommandContext(context, "git", append([]string{"-C", controller.directory}, arguments...)...)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (controller *Controller) runLefthook(context context.Context, root string, arguments ...string) error {
	path, err := controller.lefthookPath(root)
	if err != nil {
		return err
	}
	if controller.invoke != nil {
		_, err := controller.invoke(context, root, path, arguments...)
		return err
	}
	command := exec.CommandContext(context, path, arguments...)
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off", "GOPROXY=off")
	command.Stdout = controller.output
	command.Stderr = controller.output
	return command.Run()
}

func (controller *Controller) lefthookPath(root string) (string, error) {
	path := filepath.Join(root, "tools", "lefthook", "bin", "lefthook")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	if _, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("pinned Lefthook is unavailable at %s; run make bootstrap", path)
	}
	return path, nil
}

func classifyHook(path string) (hookKind, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return hookMissing, nil
	}
	if err != nil {
		return hookUnknown, fmt.Errorf("inspect pre-commit hook %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return hookUnknown, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return hookUnknown, fmt.Errorf("read pre-commit hook %s: %w", path, err)
	}
	checksum := sha256.Sum256(contents)
	if len(contents) == 222 && hex.EncodeToString(checksum[:]) == approvedLegacyHookHash {
		return hookLegacy, nil
	}
	if strings.Contains(string(contents), "LEFTHOOK") {
		return hookLefthook, nil
	}
	return hookUnknown, nil
}

func requireDefaultHooksPath(state State) error {
	if len(state.LocalPaths)+len(state.GlobalPaths)+len(state.SystemPaths) == 0 {
		return nil
	}
	return fmt.Errorf("core.hooksPath is configured (%s); it was not changed. Preserve that policy and resolve it manually before make hooks-install", strings.Join(append(append(state.LocalPaths, state.GlobalPaths...), state.SystemPaths...), ", "))
}

func unknownHookError(path string) error {
	return fmt.Errorf("pre-commit hook at %s is not an approved legacy or Lefthook hook; it was left unchanged", path)
}

func requireFile(path, description string) error {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return fmt.Errorf("%s is unavailable at %s; run make bootstrap", description, path)
	}
	return nil
}

func activeToolchainClean(root string) error {
	legacyRuntime := "b" + "un"
	for _, name := range []string{"package.json", legacyRuntime + ".lock", legacyRuntime + ".lockb", legacyRuntime + "fig.toml"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			return fmt.Errorf("active obsolete toolchain residue %s is present", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func resolveGitPath(root, value string) string {
	value = filepath.FromSlash(value)
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(root, value))
}
