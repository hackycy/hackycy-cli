package rm

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlanExplicitResolvesExistingFilesDirectoriesSymlinksAndExternalPaths(t *testing.T) {
	root := t.TempDir()
	workingDirectory := filepath.Join(root, "workspace")
	if err := os.Mkdir(workingDirectory, 0o700); err != nil {
		t.Fatalf("create working directory: %v", err)
	}
	file := filepath.Join(workingDirectory, "file.txt")
	if err := os.WriteFile(file, []byte("contents"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	directory := filepath.Join(workingDirectory, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatalf("create directory: %v", err)
	}
	symlink := filepath.Join(workingDirectory, "link")
	if err := os.Symlink(file, symlink); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	external := filepath.Join(root, "external.txt")
	if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
		t.Fatalf("write external file: %v", err)
	}

	plan, err := planExplicit(workingDirectory, []string{
		"file.txt",
		"directory",
		"link",
		external,
		filepath.Join("nested", "..", "file.txt"),
	})

	if err != nil {
		t.Fatalf("plan explicit paths: %v", err)
	}
	want := []string{file, directory, symlink, external, file}
	if !reflect.DeepEqual(plan.existing, want) {
		t.Fatalf("existing paths = %#v, want %#v", plan.existing, want)
	}
	if len(plan.missing) != 0 {
		t.Fatalf("missing paths = %#v, want none", plan.missing)
	}
}

func TestPlanExplicitClassifiesMissingAndDanglingSymlinkPathsWithoutFailing(t *testing.T) {
	workingDirectory := t.TempDir()
	dangling := filepath.Join(workingDirectory, "dangling")
	if err := os.Symlink(filepath.Join(workingDirectory, "missing-target"), dangling); err != nil {
		t.Fatalf("create dangling symlink: %v", err)
	}

	plan, err := planExplicit(workingDirectory, []string{"missing", "dangling"})

	if err != nil {
		t.Fatalf("plan explicit paths: %v", err)
	}
	if len(plan.existing) != 0 {
		t.Fatalf("existing paths = %#v, want none", plan.existing)
	}
	want := []string{filepath.Join(workingDirectory, "missing"), dangling}
	if !reflect.DeepEqual(plan.missing, want) {
		t.Fatalf("missing paths = %#v, want %#v", plan.missing, want)
	}
}

func TestPlanExplicitPreservesLegacyDuplicateAndUnsafeOperandsWithoutMutation(t *testing.T) {
	workingDirectory := t.TempDir()
	victim := filepath.Join(workingDirectory, "victim.txt")
	if err := os.WriteFile(victim, []byte("retain"), 0o600); err != nil {
		t.Fatalf("write victim: %v", err)
	}

	plan, err := planExplicit(workingDirectory, []string{".", "victim.txt", "victim.txt"})

	if err != nil {
		t.Fatalf("plan explicit paths: %v", err)
	}
	want := []string{workingDirectory, victim, victim}
	if !reflect.DeepEqual(plan.existing, want) {
		t.Fatalf("existing paths = %#v, want %#v", plan.existing, want)
	}
	contents, err := os.ReadFile(victim)
	if err != nil || string(contents) != "retain" {
		t.Fatalf("planning mutated victim = (%v, %q)", err, contents)
	}
}
