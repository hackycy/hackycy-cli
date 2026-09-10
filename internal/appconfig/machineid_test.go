package appconfig

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestResolveHomeDirectoryUsesLegacyPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		fallback    string
		want        string
	}{
		{
			name:        "USERPROFILE wins on every platform",
			environment: map[string]string{"USERPROFILE": "/profile", "HOME": "/home"},
			fallback:    "/fallback",
			want:        "/profile",
		},
		{
			name:        "HOME is second",
			environment: map[string]string{"HOME": "/home"},
			fallback:    "/fallback",
			want:        "/home",
		},
		{
			name:     "user home fallback",
			fallback: "/fallback",
			want:     "/fallback",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveHomeDirectory(func(key string) string { return test.environment[key] }, func() (string, error) { return test.fallback, nil })
			if err != nil {
				t.Fatalf("resolveHomeDirectory() returned an error: %v", err)
			}
			if got != test.want {
				t.Fatalf("resolveHomeDirectory() = %q, want %q", got, test.want)
			}
		})
	}

	if _, err := resolveHomeDirectory(func(string) string { return "" }, func() (string, error) { return "", errors.New("unavailable") }); err == nil {
		t.Fatal("resolveHomeDirectory() returned nil error for an unavailable fallback")
	}

	store, err := New(Dependencies{
		Environment: func(key string) string {
			if key == "USERPROFILE" {
				return "/profile"
			}
			return ""
		},
		UserHomeDir: func() (string, error) { return "/fallback", nil },
	})
	if err != nil {
		t.Fatalf("New() returned an error: %v", err)
	}
	if got, want := store.configPath(), filepath.Join("/profile", ".ycy-cli", "config.json"); got != want {
		t.Fatalf("configPath() = %q, want %q", got, want)
	}
}

func TestMachineIDParsers(t *testing.T) {
	macOS, err := parseDarwinMachineID(`
    | |   "IOPlatformUUID" = "8B11C2E2-0000-4E24-9999-123456789ABC"
`)
	if err != nil || macOS != "8B11C2E2-0000-4E24-9999-123456789ABC" {
		t.Fatalf("parseDarwinMachineID() = (%q, %v)", macOS, err)
	}

	linux, err := parseLinuxMachineID(" machine-id\n")
	if err != nil || linux != "machine-id" {
		t.Fatalf("parseLinuxMachineID() = (%q, %v)", linux, err)
	}

	windows, err := parseWindowsMachineID("\r\nMachineGuid    REG_SZ    01234567-89ab-cdef-0123-456789abcdef\r\n")
	if err != nil || windows != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("parseWindowsMachineID() = (%q, %v)", windows, err)
	}

	if _, err := parseDarwinMachineID("missing"); err == nil {
		t.Fatal("parseDarwinMachineID() returned nil error for missing input")
	}
	if _, err := parseLinuxMachineID(" \n"); err == nil {
		t.Fatal("parseLinuxMachineID() returned nil error for missing input")
	}
	if _, err := parseWindowsMachineID("missing"); err == nil {
		t.Fatal("parseWindowsMachineID() returned nil error for missing input")
	}
}

func TestMachineIDFallsBackToHostnameAndUsername(t *testing.T) {
	got, err := machineIDWithFallback(
		func() (string, error) { return "", errors.New("not available") },
		func() (string, error) { return "builder", nil },
		func() (string, error) { return "alice", nil },
	)
	if err != nil {
		t.Fatalf("machineIDWithFallback() returned an error: %v", err)
	}
	if got != "builder-alice" {
		t.Fatalf("machineIDWithFallback() = %q, want %q", got, "builder-alice")
	}
}

func TestNativeMachineIDAdapterReturnsAnID(t *testing.T) {
	id, err := nativeMachineID()
	if err != nil {
		t.Fatalf("nativeMachineID() returned an error: %v", err)
	}
	if id == "" {
		t.Fatal("nativeMachineID() returned an empty ID")
	}
}
