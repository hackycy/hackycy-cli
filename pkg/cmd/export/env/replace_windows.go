//go:build windows

package env

import (
	"errors"
	"syscall"
	"unsafe"
)

const exportEnvMoveFileReplaceExisting = 0x1

var (
	exportEnvKernel32       = syscall.NewLazyDLL("kernel32.dll")
	exportEnvMoveFileExW    = exportEnvKernel32.NewProc("MoveFileExW")
	exportEnvErrMoveFileExW = errors.New("MoveFileExW failed without a Windows error")
)

func replaceExportEnvFile(candidate, target string) error {
	candidatePath, err := syscall.UTF16PtrFromString(candidate)
	if err != nil {
		return err
	}
	targetPath, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	result, _, callErr := exportEnvMoveFileExW.Call(
		uintptr(unsafe.Pointer(candidatePath)),
		uintptr(unsafe.Pointer(targetPath)),
		uintptr(exportEnvMoveFileReplaceExisting),
	)
	if result != 0 {
		return nil
	}
	if callErr != syscall.Errno(0) {
		return callErr
	}
	return exportEnvErrMoveFileExW
}
