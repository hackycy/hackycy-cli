//go:build windows

package filesession

import (
	"errors"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	moveFileReplaceExisting = 0x1
	replaceRetryCount       = 100
	replaceRetryInterval    = 50 * time.Millisecond
)

var (
	kernel32       = syscall.NewLazyDLL("kernel32.dll")
	moveFileExW    = kernel32.NewProc("MoveFileExW")
	errMoveFileExW = errors.New("MoveFileExW failed without a Windows error")
)

func replaceSessionFile(candidate, target string) error {
	return replaceSessionFileWithRetry(candidate, target, replaceRetryCount, replaceRetryInterval)
}

func replaceSessionFileWithRetry(candidate, target string, attempts int, interval time.Duration) error {
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		err := moveSessionFile(candidate, target)
		if err == nil {
			return nil
		}
		last = err
		if !retryableSessionReplace(err) || attempt == attempts-1 {
			return err
		}
		time.Sleep(interval)
	}
	return last
}

func moveSessionFile(candidate, target string) error {
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

func retryableSessionReplace(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED)
}
