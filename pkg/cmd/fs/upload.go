package fs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

type UploadResult struct {
	Filename string `json:"filename"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
}

func (workspace *Workspace) Upload(directory WorkspacePath, filename string, body io.Reader) (UploadResult, error) {
	return workspace.UploadWithProgress(directory, filename, body, nil)
}

// Download streams remote content without applying the direct-upload size cap.
func (workspace *Workspace) Download(directory WorkspacePath, filename string, body io.Reader, progress func(int64)) (UploadResult, error) {
	filename, err := uploadFilename(filename)
	if err != nil {
		return UploadResult{}, err
	}
	workspace.writes.Lock()
	defer workspace.writes.Unlock()
	if err := workspace.requireDirectory(directory); err != nil {
		return UploadResult{}, err
	}
	temporary := directory.child(temporaryDownloadName())
	file, err := workspace.root.OpenFile(temporary.rootName(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return UploadResult{}, workspaceUnavailable("create download staging file", err)
	}
	published := false
	defer func() {
		if !published {
			_ = workspace.root.Remove(temporary.rootName())
		}
	}()
	written, err := io.Copy(file, &progressUploadReader{reader: body, progress: progress})
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return UploadResult{}, workspaceUnavailable("write download staging file", err)
	}
	for index := 0; index <= 9999; index++ {
		name := filename
		if index > 0 {
			name = copyFilename(filename, index)
		}
		destination := directory.child(name)
		if err := workspace.root.Link(temporary.rootName(), destination.rootName()); err != nil {
			if os.IsExist(err) {
				continue
			}
			return UploadResult{}, workspaceUnavailable("publish downloaded file", err)
		}
		if err := workspace.root.Remove(temporary.rootName()); err != nil {
			return UploadResult{}, workspaceUnavailable("remove download staging file", err)
		}
		published = true
		return UploadResult{Filename: name, Path: destination.String(), Size: written}, nil
	}
	return UploadResult{}, &ServiceError{Code: "NAME_EXHAUSTED", Message: "Too many files have the same name"}
}

func (workspace *Workspace) UploadWithProgress(directory WorkspacePath, filename string, body io.Reader, progress func(int64)) (UploadResult, error) {
	filename, err := uploadFilename(filename)
	if err != nil {
		return UploadResult{}, err
	}
	workspace.writes.Lock()
	defer workspace.writes.Unlock()
	if err := workspace.requireDirectory(directory); err != nil {
		return UploadResult{}, err
	}
	temporary := directory.child(temporaryUploadName())
	file, err := workspace.root.OpenFile(temporary.rootName(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return UploadResult{}, workspaceUnavailable("create upload staging file", err)
	}
	published := false
	defer func() {
		if !published {
			_ = workspace.root.Remove(temporary.rootName())
		}
	}()
	written, err := io.Copy(file, io.LimitReader(&progressUploadReader{reader: body, progress: progress}, MaxUploadBytes+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return UploadResult{}, workspaceUnavailable("write upload staging file", err)
	}
	if written > MaxUploadBytes {
		return UploadResult{}, &ServiceError{Code: "TOO_LARGE", Message: "Upload exceeds the 1 GiB file limit"}
	}
	for index := 0; index <= 9999; index++ {
		name := filename
		if index > 0 {
			name = copyFilename(filename, index)
		}
		destination := directory.child(name)
		if err := workspace.root.Link(temporary.rootName(), destination.rootName()); err != nil {
			if os.IsExist(err) {
				continue
			}
			return UploadResult{}, workspaceUnavailable("publish uploaded file", err)
		}
		if err := workspace.root.Remove(temporary.rootName()); err != nil {
			return UploadResult{}, workspaceUnavailable("remove upload staging file", err)
		}
		published = true
		return UploadResult{Filename: name, Path: destination.String(), Size: written}, nil
	}
	return UploadResult{}, &ServiceError{Code: "NAME_EXHAUSTED", Message: "Too many files have the same name"}
}

type progressUploadReader struct {
	reader   io.Reader
	progress func(int64)
	read     int64
}

func (reader *progressUploadReader) Read(bytes []byte) (int, error) {
	count, err := reader.reader.Read(bytes)
	reader.read += int64(count)
	if reader.progress != nil && count > 0 {
		reader.progress(reader.read)
	}
	return count, err
}

func uploadFilename(value string) (string, error) {
	filename := strings.TrimSpace(value)
	if filename == "" || filename == "." || filename == ".." || strings.Contains(filename, "/") || strings.Contains(filename, "\\") || strings.ContainsRune(filename, 0) {
		return "", &ServiceError{Code: "INVALID_UPLOAD", Message: "Upload filename is invalid"}
	}
	return filename, nil
}

func temporaryUploadName() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(fmt.Sprintf("generate upload staging name: %v", err))
	}
	return ".upload-" + hex.EncodeToString(bytes[:4]) + "-" + hex.EncodeToString(bytes[4:6]) + "-" + hex.EncodeToString(bytes[6:8]) + "-" + hex.EncodeToString(bytes[8:10]) + "-" + hex.EncodeToString(bytes[10:]) + ".tmp"
}

func temporaryDownloadName() string { return ".download-" + temporaryUploadName()[8:] }
