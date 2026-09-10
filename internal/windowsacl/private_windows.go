//go:build windows

package windowsacl

import (
	"fmt"
	"runtime"
	"strings"

	"golang.org/x/sys/windows"
)

func RestrictPrivatePath(path string) error {
	dacl, _, descriptor, err := privateDACL()
	if err != nil {
		return err
	}
	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
	runtime.KeepAlive(descriptor)
	if err != nil {
		return fmt.Errorf("restrict Windows DACL: %w", err)
	}
	return nil
}

func VerifyPrivatePath(path string) error {
	_, expected, _, err := privateDACL()
	if err != nil {
		return err
	}
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		return fmt.Errorf("read Windows DACL: %w", err)
	}
	if actual := daclSDDL(descriptor.String()); actual != expected {
		return fmt.Errorf("Windows DACL = %q, want %q", actual, expected)
	}
	return nil
}

func privateDACL() (*windows.ACL, string, *windows.SECURITY_DESCRIPTOR, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, "", nil, fmt.Errorf("open current process token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return nil, "", nil, fmt.Errorf("read current process user: %w", err)
	}
	// Windows canonicalizes generic-all rights to file-all rights and marks the
	// protected DACL as auto-inherited when it is written to a file object.
	sddl := "D:PAI(A;;FA;;;" + user.User.Sid.String() + ")(A;;FA;;;SY)(A;;FA;;;BA)"
	descriptor, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return nil, "", nil, fmt.Errorf("construct private Windows DACL: %w", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return nil, "", nil, fmt.Errorf("read private Windows DACL: %w", err)
	}
	return dacl, daclSDDL(descriptor.String()), descriptor, nil
}

func daclSDDL(descriptor string) string {
	start := strings.Index(descriptor, "D:")
	if start < 0 {
		return ""
	}
	return descriptor[start:]
}
