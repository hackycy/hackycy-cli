package tunnelruntime

import (
	"path/filepath"
	"testing"
)

func TestManagedFRPRuntimeDirectoryUsesStateRootAndDockerOverride(t *testing.T) {
	home := func() (string, error) { return "/Users/proof", nil }

	macOS, err := managedFRPRuntimeDirectory(func(string) string { return "" }, home, "darwin")
	if err != nil {
		t.Fatalf("managedFRPRuntimeDirectory() error = %v", err)
	}
	if want := filepath.Join("/Users/proof", "Library", "Application Support", "ycy", "frp", FRPVersion); macOS != want {
		t.Fatalf("macOS runtime directory = %q, want %q", macOS, want)
	}

	linux, err := managedFRPRuntimeDirectory(func(key string) string {
		if key == "XDG_STATE_HOME" {
			return "/state"
		}
		return ""
	}, home, "linux")
	if err != nil {
		t.Fatalf("managedFRPRuntimeDirectory() error = %v", err)
	}
	if want := filepath.Join("/state", "ycy", "frp", FRPVersion); linux != want {
		t.Fatalf("Linux runtime directory = %q, want %q", linux, want)
	}

	docker, err := managedFRPRuntimeDirectory(func(key string) string {
		if key == "YCY_TUNNEL_DOCKER" {
			return "1"
		}
		return ""
	}, home, "linux")
	if err != nil {
		t.Fatalf("managedFRPRuntimeDirectory() Docker error = %v", err)
	}
	if want := filepath.Join("/opt", "ycy", "frp", FRPVersion); docker != want {
		t.Fatalf("Docker runtime directory = %q, want %q", docker, want)
	}
}
