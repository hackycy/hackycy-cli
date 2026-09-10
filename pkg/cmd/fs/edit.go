package fs

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type ServiceError struct {
	Code    string
	Message string
	Cause   error
}

func (err *ServiceError) Error() string {
	return err.Message
}

func (err *ServiceError) Unwrap() error {
	return err.Cause
}

type TextSaveResult struct {
	Revision string `json:"revision"`
	Size     int64  `json:"size"`
	Modified string `json:"modifiedAt"`
	Encoding string `json:"encoding"`
}

func (workspace *Workspace) SaveText(path WorkspacePath, draft, expectedRevision string) (TextSaveResult, error) {
	if !utf8.ValidString(draft) {
		return TextSaveResult{}, &ServiceError{Code: "UNSUPPORTED_TEXT", Message: "Edited text must be valid UTF-8"}
	}
	workspace.writes.Lock()
	defer workspace.writes.Unlock()
	linkInfo, err := workspace.root.Lstat(path.rootName())
	if err != nil {
		return TextSaveResult{}, fmt.Errorf("%w: inspect %s: %v", ErrWorkspaceUnavailable, path.String(), err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.Mode().IsRegular() {
		return TextSaveResult{}, ErrWorkspacePathNotFile
	}
	source, err := workspace.readTextSource(path)
	if err != nil {
		return TextSaveResult{}, err
	}
	if source.size > MaxTextPreviewBytes {
		return TextSaveResult{}, &ServiceError{Code: "TOO_LARGE", Message: "Text file exceeds the 10 MiB limit"}
	}
	decoded, encoding, bom, ok := decodeWorkspaceTextWithBOM(source.bytes)
	if !ok {
		return TextSaveResult{}, &ServiceError{Code: "UNSUPPORTED_TEXT", Message: "File contents are not supported text"}
	}
	actualRevision := sha256.Sum256(source.bytes)
	if hex.EncodeToString(actualRevision[:]) != expectedRevision {
		return TextSaveResult{}, &ServiceError{Code: "REVISION_MISMATCH", Message: "The file changed while it was being edited"}
	}
	output := encodeWorkspaceText(normalizeWorkspaceDraft(draft, decoded), encoding, bom)
	if int64(len(output)) > MaxTextPreviewBytes {
		return TextSaveResult{}, &ServiceError{Code: "TOO_LARGE", Message: "Edited text exceeds the 10 MiB limit"}
	}
	temporary := temporaryEditPath(path)
	file, err := workspace.root.OpenFile(temporary.rootName(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, linkInfo.Mode().Perm())
	if err != nil {
		return TextSaveResult{}, fmt.Errorf("%w: create edit staging file: %v", ErrWorkspaceUnavailable, err)
	}
	published := false
	defer func() {
		if !published {
			_ = workspace.root.Remove(temporary.rootName())
		}
	}()
	if _, err := file.Write(output); err != nil {
		_ = file.Close()
		return TextSaveResult{}, fmt.Errorf("%w: write edit staging file: %v", ErrWorkspaceUnavailable, err)
	}
	if err := file.Chmod(linkInfo.Mode().Perm()); err != nil {
		_ = file.Close()
		return TextSaveResult{}, fmt.Errorf("%w: protect edit staging file: %v", ErrWorkspaceUnavailable, err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return TextSaveResult{}, fmt.Errorf("%w: sync edit staging file: %v", ErrWorkspaceUnavailable, err)
	}
	if err := file.Close(); err != nil {
		return TextSaveResult{}, fmt.Errorf("%w: close edit staging file: %v", ErrWorkspaceUnavailable, err)
	}
	finalLink, err := workspace.root.Lstat(path.rootName())
	if err != nil || finalLink.Mode()&os.ModeSymlink != 0 || !finalLink.Mode().IsRegular() {
		return TextSaveResult{}, &ServiceError{Code: "REVISION_MISMATCH", Message: "The file changed while it was being edited"}
	}
	finalSource, err := workspace.readTextSource(path)
	if err != nil {
		return TextSaveResult{}, err
	}
	finalRevision := sha256.Sum256(finalSource.bytes)
	if finalSource.size > MaxTextPreviewBytes || hex.EncodeToString(finalRevision[:]) != expectedRevision {
		return TextSaveResult{}, &ServiceError{Code: "REVISION_MISMATCH", Message: "The file changed while it was being edited"}
	}
	if err := workspace.root.Rename(temporary.rootName(), path.rootName()); err != nil {
		return TextSaveResult{}, fmt.Errorf("%w: publish edited text: %v", ErrWorkspaceUnavailable, err)
	}
	published = true
	finalInfo, err := workspace.root.Stat(path.rootName())
	if err != nil {
		return TextSaveResult{}, fmt.Errorf("%w: inspect published text: %v", ErrWorkspaceUnavailable, err)
	}
	revision := sha256.Sum256(output)
	return TextSaveResult{
		Revision: hex.EncodeToString(revision[:]),
		Size:     int64(len(output)),
		Modified: finalInfo.ModTime().UTC().Format("2006-01-02T15:04:05.000Z"),
		Encoding: encoding,
	}, nil
}

type textSource struct {
	bytes []byte
	size  int64
}

func (workspace *Workspace) readTextSource(path WorkspacePath) (textSource, error) {
	file, err := workspace.OpenFile(path)
	if err != nil {
		return textSource{}, err
	}
	defer file.Close()
	identity := file.Identity()
	if identity.Size > MaxTextPreviewBytes {
		return textSource{size: identity.Size}, nil
	}
	contents, err := io.ReadAll(io.LimitReader(file, MaxTextPreviewBytes+1))
	if err != nil {
		return textSource{}, errors.Join(ErrWorkspaceUnavailable, err)
	}
	info, err := file.file.Stat()
	if err != nil {
		return textSource{}, errors.Join(ErrWorkspaceUnavailable, err)
	}
	return textSource{bytes: contents, size: info.Size()}, nil
}

func decodeWorkspaceTextWithBOM(bytes []byte) (string, string, bool, bool) {
	if len(bytes) >= 2 && bytes[0] == 0xff && bytes[1] == 0xfe {
		text, ok := decodeWorkspaceUTF16(bytes[2:], binary.LittleEndian)
		return text, "utf-16le", true, ok
	}
	if len(bytes) >= 2 && bytes[0] == 0xfe && bytes[1] == 0xff {
		text, ok := decodeWorkspaceUTF16(bytes[2:], binary.BigEndian)
		return text, "utf-16be", true, ok
	}
	if !utf8.Valid(bytes) {
		return "", "", false, false
	}
	bom := len(bytes) >= 3 && string(bytes[:3]) == "\xef\xbb\xbf"
	if bom {
		bytes = bytes[3:]
	}
	return string(bytes), "utf-8", bom, true
}

func encodeWorkspaceText(text, encoding string, bom bool) []byte {
	if encoding == "utf-8" {
		if !bom {
			return []byte(text)
		}
		return append([]byte{0xef, 0xbb, 0xbf}, []byte(text)...)
	}
	units := utf16.Encode([]rune(text))
	output := make([]byte, 2+len(units)*2)
	if encoding == "utf-16le" {
		output[0], output[1] = 0xff, 0xfe
		for index, unit := range units {
			output[2+index*2], output[3+index*2] = byte(unit), byte(unit>>8)
		}
		return output
	}
	output[0], output[1] = 0xfe, 0xff
	for index, unit := range units {
		output[2+index*2], output[3+index*2] = byte(unit>>8), byte(unit)
	}
	return output
}

func normalizeWorkspaceDraft(draft, source string) string {
	endedWithNewline := strings.HasSuffix(source, "\n") || strings.HasSuffix(source, "\r")
	normalized := strings.ReplaceAll(strings.ReplaceAll(draft, "\r\n", "\n"), "\r", "\n")
	normalized = strings.TrimRight(normalized, "\n")
	if endedWithNewline {
		normalized += "\n"
	}
	separator := dominantLineEnding(source)
	if separator != "\n" {
		normalized = strings.ReplaceAll(normalized, "\n", separator)
	}
	return normalized
}

func dominantLineEnding(value string) string {
	crlf, lf, cr := 0, 0, 0
	for index := 0; index < len(value); index++ {
		switch value[index] {
		case '\r':
			if index+1 < len(value) && value[index+1] == '\n' {
				crlf++
				index++
			} else {
				cr++
			}
		case '\n':
			lf++
		}
	}
	if crlf > lf && crlf > cr {
		return "\r\n"
	}
	if cr > lf && cr > crlf {
		return "\r"
	}
	return "\n"
}

func temporaryEditPath(path WorkspacePath) WorkspacePath {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	name := ".edit-" + hex.EncodeToString(bytes[:4]) + "-" + hex.EncodeToString(bytes[4:6]) + "-" + hex.EncodeToString(bytes[6:8]) + "-" + hex.EncodeToString(bytes[8:10]) + "-" + hex.EncodeToString(bytes[10:]) + ".tmp"
	parts := strings.Split(path.String(), "/")
	parts[len(parts)-1] = name
	return WorkspacePath{value: strings.Join(parts, "/")}
}
