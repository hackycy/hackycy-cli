package packagesettings

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"

	"golang.org/x/sys/windows"
)

func openURL(ctx context.Context, address string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(address)
	if err != nil {
		return err
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const sFalse = syscall.Errno(1) // COM already initialized on this thread.
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && !errors.Is(err, sFalse) {
		return fmt.Errorf("initialize Windows shell: %w", err)
	}
	defer windows.CoUninitialize()
	return windows.ShellExecute(0, nil, target, nil, nil, windows.SW_SHOWNORMAL)
}
