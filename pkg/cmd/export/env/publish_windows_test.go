//go:build windows

package env

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestExportEnvPublicationLockedDestinationRemainsIntact(t *testing.T) {
	directory := t.TempDir()
	output := filepath.Join(directory, "output.json")
	original := []byte("original output")
	if err := os.WriteFile(output, original, 0o600); err != nil {
		t.Fatal(err)
	}
	// Permit reads/writes so a direct O_TRUNC write would succeed, but deny
	// delete sharing so atomic replacement must fail without touching it.
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(output), windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil,
		windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(handle)

	if err := (osExportEnvWriter{}).WriteFile(output, []byte("new output")); err == nil {
		t.Fatal("publication unexpectedly replaced a locked destination")
	}
	contents, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(contents, original) {
		t.Fatalf("original output = %q, %v; want %q", contents, err, original)
	}
	assertExportEnvPublicationDirectory(t, directory, "output.json")
}
