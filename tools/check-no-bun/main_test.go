package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRunAllowsFrozenReferenceAndMigrationMaterial(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{filepath.Join("legacy", "b"+"un"), filepath.Join(".scratch", "go-migration")} {
		if err := os.MkdirAll(filepath.Join(root, path), 0o755); err != nil {
			t.Fatalf("create %s: %v", path, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "legacy", "b"+"un", "package.json"), []byte(`{"runtime":"b`+`un"}`), 0o644); err != nil {
		t.Fatalf("write frozen reference: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".scratch", "go-migration", "notes.md"), []byte("b"+"un"), 0o644); err != nil {
		t.Fatalf("write migration note: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.invalid/test\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := run(root, &bytes.Buffer{}); err != nil {
		t.Fatalf("run rejected allowed reference material: %v", err)
	}
}

func TestRunRejectsActiveReference(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("b"+"un"+" test\n"), 0o644); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	if err := run(root, &bytes.Buffer{}); err == nil {
		t.Fatal("run accepted an active legacy runtime reference")
	}
}
