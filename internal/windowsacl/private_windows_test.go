//go:build windows

package windowsacl

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRestrictPrivatePathRejectsBroadDACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.txt")
	if err := os.WriteFile(path, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RestrictPrivatePath(path); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPrivatePath(path); err != nil {
		t.Fatal(err)
	}

	descriptor, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPrivatePath(path); err == nil {
		t.Fatal("VerifyPrivatePath accepted a broad DACL")
	}
}
