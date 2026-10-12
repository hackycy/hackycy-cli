//go:build windows

package zip

import (
	"errors"
	"syscall"
	"unsafe"
)

const zipMoveFileReplaceExisting = 0x1

var (
	zipKernel32       = syscall.NewLazyDLL("kernel32.dll")
	zipMoveFileExW    = zipKernel32.NewProc("MoveFileExW")
	zipErrMoveFileExW = errors.New("MoveFileExW failed without a Windows error")
)

func replaceZipFile(candidate, target string) error {
	candidatePath, err := syscall.UTF16PtrFromString(candidate)
	if err != nil {
		return err
	}
	targetPath, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := zipMoveFileExW.Call(
		uintptr(unsafe.Pointer(candidatePath)),
		uintptr(unsafe.Pointer(targetPath)),
		uintptr(zipMoveFileReplaceExisting),
	)
	if result != 0 {
		return nil
	}
	if callErr != syscall.Errno(0) {
		return callErr
	}
	return zipErrMoveFileExW
}
