package rm

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestSmartActionsPreserveLegacyOrderAndLabels(t *testing.T) {
	want := []SmartAction{
		{ID: smartActionNodeDist, Label: "Node project - delete ./dist"},
		{ID: smartActionNodeModules, Label: "Node project - delete ./node_modules"},
		{ID: smartActionMonorepoDist, Label: "Monorepo - delete all dist dirs (recursive)"},
		{ID: smartActionMonorepoModules, Label: "Monorepo - delete all node_modules dirs (recursive)"},
		{ID: smartActionNodeLockfile, Label: "Node project - delete lockfile(s)"},
		{ID: smartActionAIAgent, Label: "AI agent config dirs (.claude, .cursor, .copilot...)"},
	}
	if !reflect.DeepEqual(smartActions, want) {
		t.Fatalf("smart actions = %#v, want %#v", smartActions, want)
	}
}

func TestDiscoverSmartRootActionsUseExistenceWithoutPathKindRestriction(t *testing.T) {
	workingDirectory := t.TempDir()
	for _, name := range append(append([]string{"dist", "package-lock.json", "pnpm-lock.yaml", ".claude", ".agents", ".cursor"}, lockfileNames[1:]...), aiAgentDirectoryNames[2:]...) {
		path := filepath.Join(workingDirectory, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(workingDirectory, "node_modules"), 0o700); err != nil {
		t.Fatalf("create node_modules: %v", err)
	}

	testCases := []struct {
		action SmartAction
		want   []string
	}{
		{action: smartActions[0], want: []string{filepath.Join(workingDirectory, "dist")}},
		{action: smartActions[1], want: []string{filepath.Join(workingDirectory, "node_modules")}},
		{action: smartActions[4], want: joinPaths(workingDirectory, lockfileNames)},
		{action: smartActions[5], want: joinPaths(workingDirectory, aiAgentDirectoryNames)},
	}
	for _, testCase := range testCases {
		t.Run(testCase.action.ID, func(t *testing.T) {
			got := discoverSmart(workingDirectory, testCase.action, 5)
			if !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("discovered paths = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestDiscoverSmartRecursiveActionsPreserveDepthEdgesAndSkipRules(t *testing.T) {
	workingDirectory := t.TempDir()
	direct := makeDirectory(t, workingDirectory, "dist")
	depthTwo := makeDirectory(t, workingDirectory, "workspace", "dist")
	depthThree := makeDirectory(t, workingDirectory, "workspace", "nested", "dist")
	hidden := makeDirectory(t, workingDirectory, ".hidden", "dist")
	vcs := makeDirectory(t, workingDirectory, ".git", "dist")
	cache := makeDirectory(t, workingDirectory, "__pycache__", "dist")
	if err := os.WriteFile(filepath.Join(workingDirectory, "file-named-dist"), nil, 0o600); err != nil {
		t.Fatalf("write regular file: %v", err)
	}
	if err := os.Symlink(filepath.Join(workingDirectory, "workspace"), filepath.Join(workingDirectory, "linked-workspace")); err != nil {
		t.Fatalf("create directory symlink: %v", err)
	}

	if got := discoverSmart(workingDirectory, smartActions[2], -1); len(got) != 0 {
		t.Fatalf("negative depth paths = %#v, want none", got)
	}
	assertPathSet(t, discoverSmart(workingDirectory, smartActions[2], 0), []string{direct})
	assertPathSet(t, discoverSmart(workingDirectory, smartActions[2], 1), []string{direct, depthTwo})
	assertPathSet(t, discoverSmart(workingDirectory, smartActions[2], 2), []string{direct, depthTwo, depthThree})
	for _, skipped := range []string{hidden, vcs, cache} {
		for _, found := range discoverSmart(workingDirectory, smartActions[2], 5) {
			if found == skipped {
				t.Fatalf("recursive scan included skipped path %s", skipped)
			}
		}
	}
}

func TestDiscoverSmartRecursiveActionsIgnoreWrongTypesAndMissingRoots(t *testing.T) {
	workingDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(workingDirectory, "node_modules"), nil, 0o600); err != nil {
		t.Fatalf("write node_modules file: %v", err)
	}
	if got := discoverSmart(workingDirectory, smartActions[3], 5); len(got) != 0 {
		t.Fatalf("recursive paths = %#v, want none", got)
	}
	if got := discoverSmart(filepath.Join(workingDirectory, "missing"), smartActions[3], 5); len(got) != 0 {
		t.Fatalf("missing root paths = %#v, want none", got)
	}
}

func makeDirectory(t *testing.T, root string, elements ...string) string {
	t.Helper()
	directory := filepath.Join(append([]string{root}, elements...)...)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("create %s: %v", directory, err)
	}
	return directory
}

func joinPaths(root string, names []string) []string {
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(root, name))
	}
	return paths
}

func assertPathSet(t *testing.T, got, want []string) {
	t.Helper()
	got = append([]string(nil), got...)
	want = append([]string(nil), want...)
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("paths = %#v, want %#v", got, want)
	}
}
