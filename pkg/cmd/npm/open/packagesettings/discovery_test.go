package packagesettings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDiscoverPackagesFromWorkspaceRootAndMember(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"root-package","workspaces":["other/*"]}`)
	writeFixture(t, root, "pnpm-workspace.yaml", `packages: ['packages/**', '!packages/skip/**', './packages/a/']`)
	writeFixture(t, root, "packages/a/package.json", `{"name":"@example/a"}`)
	writeFixture(t, root, "packages/b/package.json", `{"name":"example-b","version":"2.0.0"}`)
	writeFixture(t, root, "packages/duplicate/package.json", `{"name":"example-b"}`)
	writeFixture(t, root, "packages/private/package.json", `{"name":"private-package","private":true}`)
	writeFixture(t, root, "packages/nameless/package.json", `{"name":false}`)
	writeFixture(t, root, "packages/invalid/package.json", `{"name":"@../tools"}`)
	writeFixture(t, root, "packages/skip/package.json", `{"name":"skipped-package"}`)
	writeFixture(t, root, "packages/skip/reinclude/package.json", `{"name":"skipped-nested-package"}`)
	writeFixture(t, root, "packages/node_modules/broken/package.json", `{`)
	writeFixture(t, root, "other/not-a-member/package.json", `{"name":"independent-package"}`)
	memberSource := filepath.Join(root, "packages", "b", "src")
	if err := os.Mkdir(memberSource, 0o700); err != nil {
		t.Fatal(err)
	}

	want := []string{"@example/a", "example-b", "root-package"}
	assertPackages(t, root, want)
	assertPackages(t, memberSource, want)
	assertPackages(t, filepath.Join(root, "other", "not-a-member"), []string{"independent-package"})
}

func TestDiscoverPackagesFromJSONWorkspaces(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"workspace-root","private":true,"workspaces":["apps/{one,two}","apps/ignored","!apps/ignored","apps/one/"]}`)
	writeFixture(t, root, "apps/one/package.json", `{"name":"one"}`)
	writeFixture(t, root, "apps/two/package.json", `{"name":"two"}`)
	writeFixture(t, root, "apps/ignored/package.json", `{"name":"ignored"}`)
	writeFixture(t, root, "apps/unlisted/package.json", `{"name":"unlisted"}`)
	assertPackages(t, filepath.Join(root, "apps", "two"), []string{"one", "two"})
}

func TestDiscoverPackagesWithPNPMRootOnlyOrNoEligiblePackages(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"root-only","workspaces":["packages/*"]}`)
	writeFixture(t, root, "pnpm-workspace.yaml", "allowBuilds:\n  esbuild: true\n")
	writeFixture(t, root, "packages/a/package.json", `{"name":"a"}`)
	assertPackages(t, root, []string{"root-only"})

	writeFixture(t, root, "package.json", `{"name":"root-only","private":true}`)
	assertPackages(t, root, []string{})

	if err := os.Remove(filepath.Join(root, "package.json")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "pnpm-workspace.yaml", "packages:\n- packages/*\n")
	assertPackages(t, root, []string{"a"})
}

func TestDiscoverPackagesRespectsGitBoundaries(t *testing.T) {
	for _, worktree := range []bool{false, true} {
		root := t.TempDir()
		writeFixture(t, root, "package.json", `{"name":"outer","workspaces":["worktree"]}`)
		inner := filepath.Join(root, "worktree")
		if worktree {
			writeFixture(t, inner, ".git", "gitdir: ../.git/worktrees/worktree\n")
		} else if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := discoverPackages(t.Context(), inner)
		if !errors.Is(err, errNoProject) {
			t.Fatalf("project outside Git boundary: %v", err)
		}
		writeFixture(t, inner, "package.json", `{"name":"inner"}`)
		src := filepath.Join(inner, "src")
		if err := os.Mkdir(src, 0o700); err != nil {
			t.Fatal(err)
		}
		assertPackages(t, src, []string{"inner"})
	}
}

func TestDiscoverPackagesReportsInvalidProjectFiles(t *testing.T) {
	for _, test := range []struct{ file, contents string }{
		{"pnpm-workspace.yaml", "packages: [invalid"},
		{"pnpm-workspace.yaml", "packages: ['../outside']"},
		{"package.json", `{"workspaces":null}`},
		{"package.json", `{"workspaces":["apps/["]}`},
		{"apps/a/package.json", `{"name":`},
	} {
		t.Run(test.contents, func(t *testing.T) {
			root := t.TempDir()
			writeFixture(t, root, "package.json", `{"name":"root","workspaces":["apps/*"]}`)
			writeFixture(t, root, test.file, test.contents)
			names, err := discoverPackages(t.Context(), root)
			if names != nil || err == nil || !strings.Contains(err.Error(), filepath.FromSlash(test.file)) {
				t.Fatalf("discoverPackages() = %v, %v, want error identifying %s", names, err, test.file)
			}
		})
	}
}

func TestDiscoverPackagesRejectsManifestLinkOutsideWorkspace(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "package.json", `{"name":"root","workspaces":["apps/*"]}`)
	writeFixture(t, root, "apps/a/placeholder", "")
	outside := t.TempDir()
	writeFixture(t, outside, "package.json", `{"name":"outside"}`)
	if err := os.Symlink(filepath.Join(outside, "package.json"), filepath.Join(root, "apps", "a", "package.json")); err != nil {
		t.Skipf("creating symbolic links is unavailable: %v", err)
	}
	if _, err := discoverPackages(t.Context(), root); err == nil || !strings.Contains(err.Error(), filepath.FromSlash("apps/a/package.json")) {
		t.Fatalf("discoverPackages() error = %v, want contained manifest error", err)
	}
}

func assertPackages(t *testing.T, directory string, want []string) {
	t.Helper()
	got, err := discoverPackages(context.Background(), directory)
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("discoverPackages(%q) = %v, %v, want %v", directory, got, err, want)
	}
}

func writeFixture(t *testing.T, directory, relative, contents string) {
	t.Helper()
	file := filepath.Join(directory, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
