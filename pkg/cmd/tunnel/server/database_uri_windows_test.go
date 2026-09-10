//go:build windows

package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseFileURIUsesWindowsDriverCompatibleDrivePath(t *testing.T) {
	path := `C:\Users\Operator Name\数据\tunnel.sqlite`
	got := databaseFileURI(path)
	if !strings.HasPrefix(got, "file:C:/") {
		t.Fatalf("databaseFileURI(%q) = %q, want file:C:/ prefix", path, got)
	}
	if strings.HasPrefix(got, "file://") || strings.Contains(got, "\\") {
		t.Fatalf("databaseFileURI(%q) = %q, want no host or backslashes", path, got)
	}
	if !strings.Contains(got, "%20") || !strings.Contains(got, "%E6%95%B0%") {
		t.Fatalf("databaseFileURI(%q) = %q, want URI escaping", path, got)
	}
}

func TestOpenDatabaseSupportsSpacedUnicodeWindowsPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Tunnel Data 数据")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatalf("create test directory: %v", err)
	}
	path := filepath.Join(root, "tunnel.sqlite")
	database, err := openDatabase(path)
	if err != nil {
		t.Fatalf("openDatabase(%q): %v", path, err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat database: %v", err)
	}
}
