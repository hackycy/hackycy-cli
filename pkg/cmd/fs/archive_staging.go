package fs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type archiveStaging struct {
	workspace   *Workspace
	source      WorkspacePath
	destination WorkspacePath
}

func (workspace *Workspace) prepareArchiveStaging(source WorkspacePath) (archiveStaging, error) {
	if source.String() == "" || !extractableArchiveName(source.baseName()) {
		return archiveStaging{}, &ServiceError{Code: "INVALID_ARCHIVE", Message: "Archive type is not supported"}
	}
	directory := parentWorkspacePath(source)
	if err := workspace.requireDirectory(directory); err != nil {
		return archiveStaging{}, err
	}
	input, err := workspace.OpenFile(source)
	if err != nil {
		return archiveStaging{}, err
	}
	defer input.Close()
	name := temporaryExtractionName()
	staging := archiveStaging{
		workspace:   workspace,
		source:      directory.child(name + ".source"),
		destination: directory.child(name),
	}
	output, err := workspace.root.OpenFile(staging.source.rootName(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return archiveStaging{}, workspaceUnavailable("create archive source staging file", err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = workspace.root.Remove(staging.source.rootName())
		return archiveStaging{}, workspaceUnavailable("copy archive source into staging", copyErr)
	}
	if closeErr != nil {
		_ = workspace.root.Remove(staging.source.rootName())
		return archiveStaging{}, workspaceUnavailable("close archive source staging file", closeErr)
	}
	if err := workspace.root.Mkdir(staging.destination.rootName(), 0o700); err != nil {
		_ = workspace.root.Remove(staging.source.rootName())
		return archiveStaging{}, workspaceUnavailable("create archive extraction staging directory", err)
	}
	return staging, nil
}

func (staging archiveStaging) sourcePath() string {
	return staging.workspace.absolutePath(staging.source)
}

func (staging archiveStaging) destinationPath() string {
	return staging.workspace.absolutePath(staging.destination)
}

func (staging archiveStaging) Cleanup() error {
	var result error
	if err := staging.workspace.root.RemoveAll(staging.destination.rootName()); err != nil && !os.IsNotExist(err) {
		result = workspaceUnavailable("remove archive extraction staging directory", err)
	}
	if err := staging.workspace.root.Remove(staging.source.rootName()); err != nil && !os.IsNotExist(err) && result == nil {
		result = workspaceUnavailable("remove archive source staging file", err)
	}
	return result
}

func (workspace *Workspace) absolutePath(path WorkspacePath) string {
	return filepath.Join(workspace.rootDirectory, filepath.FromSlash(path.rootName()))
}

func parentWorkspacePath(path WorkspacePath) WorkspacePath {
	index := strings.LastIndex(path.String(), "/")
	if index == -1 {
		return WorkspacePath{}
	}
	return WorkspacePath{value: path.String()[:index]}
}

func temporaryExtractionName() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		panic(fmt.Sprintf("generate extraction staging name: %v", err))
	}
	return ".extract-" + hex.EncodeToString(bytes[:4]) + "-" + hex.EncodeToString(bytes[4:6]) + "-" + hex.EncodeToString(bytes[6:8]) + "-" + hex.EncodeToString(bytes[8:10]) + "-" + hex.EncodeToString(bytes[10:]) + ".tmp"
}
