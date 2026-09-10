//go:build windows

package updater

import (
	"errors"
	"syscall"
	"unsafe"
)

const moveFileReplaceExisting = 0x1

var (
	updateKernel32       = syscall.NewLazyDLL("kernel32.dll")
	updateMoveFileExW    = updateKernel32.NewProc("MoveFileExW")
	errUpdateMoveFileExW = errors.New("MoveFileExW failed without a Windows error")
)

func replaceStateFile(candidate, target string) error {
	return retryFileOperation(fileRetryCount, defaultFileSleep, func() error {
		candidatePath, err := syscall.UTF16PtrFromString(candidate)
		if err != nil {
			return err
		}
		targetPath, err := syscall.UTF16PtrFromString(target)
		if err != nil {
			return err
		}
		result, _, callErr := updateMoveFileExW.Call(
			uintptr(unsafe.Pointer(candidatePath)),
			uintptr(unsafe.Pointer(targetPath)),
			uintptr(moveFileReplaceExisting),
		)
		if result != 0 {
			return nil
		}
		if callErr != syscall.Errno(0) {
			return callErr
		}
		return errUpdateMoveFileExW
	})
}
