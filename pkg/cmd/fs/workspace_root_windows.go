//go:build windows

package fs

import (
	"errors"
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

const workspaceRootOpenAttempts = 3

var errWorkspaceRootChanged = errors.New("workspace root changed while opening")

type windowsWorkspaceRoot struct {
	mu       sync.Mutex
	handle   windows.Handle
	identity windowsFileIdentity
}

type windowsFileIdentity struct {
	volumeSerialNumber uint32
	fileIndexHigh      uint32
	fileIndexLow       uint32
}

func openWorkspaceRoot(directory string) (workspaceRoot, error) {
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(directory),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return nil, err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, err
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("workspace root is not a directory")
	}
	return &windowsWorkspaceRoot{
		handle:   handle,
		identity: windowsIdentity(information),
	}, nil
}

func (root *windowsWorkspaceRoot) Close() error {
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.handle == windows.InvalidHandle {
		return nil
	}
	handle := root.handle
	root.handle = windows.InvalidHandle
	return windows.CloseHandle(handle)
}

func (root *windowsWorkspaceRoot) Open(name string) (*os.File, error) {
	return withTemporaryWorkspaceRoot(root, func(temporary *os.Root) (*os.File, error) {
		return temporary.Open(name)
	})
}

func (root *windowsWorkspaceRoot) OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	return withTemporaryWorkspaceRoot(root, func(temporary *os.Root) (*os.File, error) {
		return temporary.OpenFile(name, flag, perm)
	})
}

func (root *windowsWorkspaceRoot) Lstat(name string) (os.FileInfo, error) {
	return withTemporaryWorkspaceRoot(root, func(temporary *os.Root) (os.FileInfo, error) {
		return temporary.Lstat(name)
	})
}

func (root *windowsWorkspaceRoot) Stat(name string) (os.FileInfo, error) {
	return withTemporaryWorkspaceRoot(root, func(temporary *os.Root) (os.FileInfo, error) {
		return temporary.Stat(name)
	})
}

func (root *windowsWorkspaceRoot) Readlink(name string) (string, error) {
	return withTemporaryWorkspaceRoot(root, func(temporary *os.Root) (string, error) {
		return temporary.Readlink(name)
	})
}

func (root *windowsWorkspaceRoot) Mkdir(name string, perm os.FileMode) error {
	return root.use(func(temporary *os.Root) error { return temporary.Mkdir(name, perm) })
}

func (root *windowsWorkspaceRoot) Remove(name string) error {
	return root.use(func(temporary *os.Root) error { return temporary.Remove(name) })
}

func (root *windowsWorkspaceRoot) RemoveAll(name string) error {
	return root.use(func(temporary *os.Root) error { return temporary.RemoveAll(name) })
}

func (root *windowsWorkspaceRoot) Rename(oldname, newname string) error {
	return root.use(func(temporary *os.Root) error { return temporary.Rename(oldname, newname) })
}

func (root *windowsWorkspaceRoot) Link(oldname, newname string) error {
	return root.use(func(temporary *os.Root) error { return temporary.Link(oldname, newname) })
}

func (root *windowsWorkspaceRoot) Symlink(oldname, newname string) error {
	return root.use(func(temporary *os.Root) error { return temporary.Symlink(oldname, newname) })
}

// OpenWorkspaceFile keeps the existing os.Root containment check, then swaps
// only the reader handle for one that permits a Windows-native replacement.
func (root *windowsWorkspaceRoot) OpenWorkspaceFile(name string) (*os.File, error) {
	for attempt := 0; attempt < workspaceRootOpenAttempts; attempt++ {
		contained, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		path, expected, err := windowsPathAndIdentity(contained)
		if err != nil {
			_ = contained.Close()
			return nil, err
		}
		shared, err := windows.CreateFile(
			windows.StringToUTF16Ptr(path),
			windows.GENERIC_READ,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_BACKUP_SEMANTICS,
			0,
		)
		closeErr := contained.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			_ = windows.CloseHandle(shared)
			return nil, closeErr
		}
		file := os.NewFile(uintptr(shared), path)
		if file == nil {
			_ = windows.CloseHandle(shared)
			return nil, errors.New("open workspace file: unable to wrap Windows handle")
		}
		_, actual, err := windowsPathAndIdentity(file)
		if err == nil && actual == expected {
			return file, nil
		}
		_ = file.Close()
		if err != nil {
			return nil, err
		}
	}
	return nil, errWorkspaceRootChanged
}

func (root *windowsWorkspaceRoot) use(operation func(*os.Root) error) error {
	_, err := withTemporaryWorkspaceRoot(root, func(temporary *os.Root) (struct{}, error) {
		return struct{}{}, operation(temporary)
	})
	return err
}

func withTemporaryWorkspaceRoot[T any](root *windowsWorkspaceRoot, operation func(*os.Root) (T, error)) (T, error) {
	var zero T
	for attempt := 0; attempt < workspaceRootOpenAttempts; attempt++ {
		directory, expected, err := root.currentPathAndIdentity()
		if err != nil {
			return zero, err
		}
		temporary, err := os.OpenRoot(directory)
		if err != nil {
			continue
		}
		proof, err := temporary.Open(".")
		if err != nil {
			_ = temporary.Close()
			return zero, err
		}
		_, actual, identityErr := windowsPathAndIdentity(proof)
		closeProofErr := proof.Close()
		if identityErr != nil {
			_ = temporary.Close()
			return zero, identityErr
		}
		if closeProofErr != nil {
			_ = temporary.Close()
			return zero, closeProofErr
		}
		if actual != expected {
			_ = temporary.Close()
			continue
		}
		result, operationErr := operation(temporary)
		closeErr := temporary.Close()
		if operationErr != nil {
			return zero, operationErr
		}
		if closeErr != nil {
			return zero, closeErr
		}
		return result, nil
	}
	return zero, errWorkspaceRootChanged
}

func (root *windowsWorkspaceRoot) currentPathAndIdentity() (string, windowsFileIdentity, error) {
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.handle == windows.InvalidHandle {
		return "", windowsFileIdentity{}, os.ErrClosed
	}
	buffer := make([]uint16, 1024)
	for {
		length, err := windows.GetFinalPathNameByHandle(root.handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", windowsFileIdentity{}, err
		}
		if int(length) < len(buffer) {
			return windows.UTF16ToString(buffer[:length]), root.identity, nil
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func windowsPathAndIdentity(file *os.File) (string, windowsFileIdentity, error) {
	handle := windows.Handle(file.Fd())
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		return "", windowsFileIdentity{}, err
	}
	buffer := make([]uint16, 1024)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", windowsFileIdentity{}, err
		}
		if int(length) < len(buffer) {
			return windows.UTF16ToString(buffer[:length]), windowsIdentity(information), nil
		}
		buffer = make([]uint16, int(length)+1)
	}
}

func windowsIdentity(information windows.ByHandleFileInformation) windowsFileIdentity {
	return windowsFileIdentity{
		volumeSerialNumber: information.VolumeSerialNumber,
		fileIndexHigh:      information.FileIndexHigh,
		fileIndexLow:       information.FileIndexLow,
	}
}
