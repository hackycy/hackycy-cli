//go:build windows

package appconfig

import (
	"errors"
	"syscall"
	"unsafe"
)

const moveFileReplaceExisting = 0x1

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	moveFileExW    = kernel32.NewProc("MoveFileExW")
	errMoveFileExW = errors.New("MoveFileExW failed without a Windows error")
)

func replaceConfigFile(candidate, target string) error {
	candidatePath, err := syscall.UTF16PtrFromString(candidate)
	if err != nil {
		return err
	}
	targetPath, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := moveFileExW.Call(
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
	return errMoveFileExW
}
