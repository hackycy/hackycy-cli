package fs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
)

type Operation struct {
	Action          string
	ParentPath      string
	Name            string
	Path            string
	NewName         string
	Paths           []string
	DestinationPath string
}

type OperationItem struct {
	Status          string          `json:"status"`
	SourcePath      string          `json:"sourcePath,omitempty"`
	DestinationPath string          `json:"destinationPath,omitempty"`
	Error           *OperationError `json:"error,omitempty"`
}

type OperationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type OperationResult struct {
	Action string          `json:"action"`
	Items  []OperationItem `json:"items"`
}

func (workspace *Workspace) ApplyOperation(operation Operation) OperationResult {
	workspace.writes.Lock()
	defer workspace.writes.Unlock()
	result := OperationResult{Action: operation.Action}
	switch operation.Action {
	case "create-directory":
		result.Items = []OperationItem{workspace.createDirectory(operation.ParentPath, operation.Name)}
	case "rename":
		result.Items = []OperationItem{workspace.rename(operation.Path, operation.NewName)}
	case "copy":
		result.Items = workspace.copy(operation.Paths, operation.DestinationPath)
	case "move":
		result.Items = workspace.move(operation.Paths, operation.DestinationPath)
	case "delete":
		result.Items = workspace.delete(operation.Paths)
	default:
		result.Items = []OperationItem{operationFailure("", "", &ServiceError{Code: "INVALID_OPERATION", Message: "Operation action is invalid"})}
	}
	return result
}

func (workspace *Workspace) createDirectory(parentValue, name string) OperationItem {
	parent, err := ParseWorkspacePath(parentValue)
	if err == nil {
		err = workspace.requireDirectory(parent)
	}
	if err != nil {
		return operationFailure("", "", err)
	}
	if err := validateOperationName(name); err != nil {
		return operationFailure("", "", err)
	}
	destination := parent.child(name)
	if err := workspace.root.Mkdir(destination.rootName(), 0o755); err != nil {
		if errors.Is(err, os.ErrExist) {
			return operationFailure("", destination.String(), &ServiceError{Code: "ALREADY_EXISTS", Message: "An entry with that name already exists"})
		}
		return operationFailure("", destination.String(), workspaceUnavailable("create directory", err))
	}
	return OperationItem{Status: "ok", DestinationPath: destination.String()}
}

func (workspace *Workspace) rename(value, newName string) OperationItem {
	source, err := ParseWorkspacePath(value)
	if err != nil {
		return operationFailure(value, "", err)
	}
	if source.String() == "" {
		return operationFailure(value, "", &ServiceError{Code: "ROOT_IMMUTABLE", Message: "The file browser root cannot be changed"})
	}
	if err := validateOperationName(newName); err != nil {
		return operationFailure(value, "", err)
	}
	if _, err := workspace.root.Lstat(source.rootName()); err != nil {
		return operationFailure(value, "", workspaceUnavailable("inspect rename source", err))
	}
	destination := siblingWorkspacePath(source, newName)
	if _, err := workspace.root.Lstat(destination.rootName()); err == nil {
		return operationFailure(value, destination.String(), &ServiceError{Code: "ALREADY_EXISTS", Message: "An entry with that name already exists"})
	} else if !errors.Is(err, os.ErrNotExist) {
		return operationFailure(value, destination.String(), workspaceUnavailable("inspect rename destination", err))
	}
	if err := workspace.root.Rename(source.rootName(), destination.rootName()); err != nil {
		return operationFailure(value, destination.String(), workspaceUnavailable("rename entry", err))
	}
	return OperationItem{Status: "ok", SourcePath: source.String(), DestinationPath: destination.String()}
}

func (workspace *Workspace) copy(values []string, destinationValue string) []OperationItem {
	destination, err := ParseWorkspacePath(destinationValue)
	if err == nil {
		err = workspace.requireDirectory(destination)
	}
	if err != nil {
		return repeatedOperationFailure(values, destinationValue, err)
	}
	items := make([]OperationItem, 0, len(values))
	for _, value := range values {
		source, err := ParseWorkspacePath(value)
		if err != nil || source.String() == "" {
			if err == nil {
				err = &ServiceError{Code: "ROOT_IMMUTABLE", Message: "The file browser root cannot be changed"}
			}
			items = append(items, operationFailure(value, "", err))
			continue
		}
		info, err := workspace.root.Lstat(source.rootName())
		if err != nil {
			items = append(items, operationFailure(value, "", workspaceUnavailable("inspect copy source", err)))
			continue
		}
		if info.IsDir() && isWorkspaceDescendant(destination, source) {
			items = append(items, operationFailure(value, "", &ServiceError{Code: "INVALID_OPERATION", Message: "A directory cannot be copied into itself"}))
			continue
		}
		target, err := workspace.availableCopyDestination(destination, source.baseName())
		if err != nil {
			items = append(items, operationFailure(value, "", err))
			continue
		}
		if err := workspace.copyEntry(source, target); err != nil {
			items = append(items, operationFailure(value, target.String(), err))
			continue
		}
		items = append(items, OperationItem{Status: "ok", SourcePath: source.String(), DestinationPath: target.String()})
	}
	return items
}

func (workspace *Workspace) move(values []string, destinationValue string) []OperationItem {
	destination, err := ParseWorkspacePath(destinationValue)
	if err == nil {
		err = workspace.requireDirectory(destination)
	}
	if err != nil {
		return repeatedOperationFailure(values, destinationValue, err)
	}
	items := make([]OperationItem, 0, len(values))
	for _, value := range values {
		source, err := ParseWorkspacePath(value)
		if err != nil || source.String() == "" {
			if err == nil {
				err = &ServiceError{Code: "ROOT_IMMUTABLE", Message: "The file browser root cannot be changed"}
			}
			items = append(items, operationFailure(value, "", err))
			continue
		}
		info, err := workspace.root.Lstat(source.rootName())
		if err != nil {
			items = append(items, operationFailure(value, "", workspaceUnavailable("inspect move source", err)))
			continue
		}
		if info.IsDir() && isWorkspaceDescendant(destination, source) {
			items = append(items, operationFailure(value, "", &ServiceError{Code: "INVALID_OPERATION", Message: "A directory cannot be moved into itself"}))
			continue
		}
		target := destination.child(source.baseName())
		if _, err := workspace.root.Lstat(target.rootName()); err == nil {
			items = append(items, operationFailure(value, target.String(), &ServiceError{Code: "ALREADY_EXISTS", Message: "An entry with that name already exists"}))
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			items = append(items, operationFailure(value, target.String(), workspaceUnavailable("inspect move destination", err)))
			continue
		}
		if err := workspace.root.Rename(source.rootName(), target.rootName()); err != nil {
			items = append(items, operationFailure(value, target.String(), workspaceUnavailable("move entry", err)))
			continue
		}
		items = append(items, OperationItem{Status: "ok", SourcePath: source.String(), DestinationPath: target.String()})
	}
	return items
}

func (workspace *Workspace) delete(values []string) []OperationItem {
	items := make([]OperationItem, 0, len(values))
	for _, value := range values {
		source, err := ParseWorkspacePath(value)
		if err != nil || source.String() == "" {
			if err == nil {
				err = &ServiceError{Code: "ROOT_IMMUTABLE", Message: "The file browser root cannot be changed"}
			}
			items = append(items, operationFailure(value, "", err))
			continue
		}
		if _, err := workspace.root.Lstat(source.rootName()); err != nil {
			items = append(items, operationFailure(value, "", workspaceUnavailable("inspect delete source", err)))
			continue
		}
		if err := workspace.root.RemoveAll(source.rootName()); err != nil {
			items = append(items, operationFailure(value, "", workspaceUnavailable("delete entry", err)))
			continue
		}
		items = append(items, OperationItem{Status: "ok", SourcePath: source.String()})
	}
	return items
}

func (workspace *Workspace) requireDirectory(path WorkspacePath) error {
	file, err := workspace.root.Open(path.rootName())
	if err != nil {
		return workspaceUnavailable("open directory", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return workspaceUnavailable("inspect directory", err)
	}
	if !info.IsDir() {
		return ErrWorkspacePathNotDir
	}
	return nil
}

func (workspace *Workspace) availableCopyDestination(directory WorkspacePath, filename string) (WorkspacePath, error) {
	for index := 0; index <= 9999; index++ {
		candidate := filename
		if index > 0 {
			candidate = copyFilename(filename, index)
		}
		path := directory.child(candidate)
		if _, err := workspace.root.Lstat(path.rootName()); errors.Is(err, os.ErrNotExist) {
			return path, nil
		} else if err != nil {
			return WorkspacePath{}, workspaceUnavailable("inspect copy destination", err)
		}
	}
	return WorkspacePath{}, &ServiceError{Code: "NAME_EXHAUSTED", Message: "Too many entries have the same name"}
}

func (workspace *Workspace) copyEntry(source, destination WorkspacePath) error {
	info, err := workspace.root.Lstat(source.rootName())
	if err != nil {
		return workspaceUnavailable("inspect copy source", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := workspace.root.Readlink(source.rootName())
		if err != nil {
			return workspaceUnavailable("read copy link", err)
		}
		if err := workspace.root.Symlink(target, destination.rootName()); err != nil {
			return workspaceUnavailable("copy link", err)
		}
		return nil
	}
	if info.IsDir() {
		if err := workspace.root.Mkdir(destination.rootName(), info.Mode().Perm()); err != nil {
			return workspaceUnavailable("create copied directory", err)
		}
		directory, err := workspace.root.Open(source.rootName())
		if err != nil {
			return workspaceUnavailable("open copied directory", err)
		}
		children, err := directory.ReadDir(-1)
		_ = directory.Close()
		if err != nil {
			return workspaceUnavailable("list copied directory", err)
		}
		for _, child := range children {
			if err := workspace.copyEntry(source.child(child.Name()), destination.child(child.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return &ServiceError{Code: "UNAVAILABLE", Message: "Filesystem operation failed"}
	}
	input, err := workspace.root.Open(source.rootName())
	if err != nil {
		return workspaceUnavailable("open copy source", err)
	}
	defer input.Close()
	output, err := workspace.root.OpenFile(destination.rootName(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return workspaceUnavailable("create copy destination", err)
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = workspace.root.Remove(destination.rootName())
		return workspaceUnavailable("copy file", copyErr)
	}
	if closeErr != nil {
		return workspaceUnavailable("close copied file", closeErr)
	}
	return nil
}

func validateOperationName(value string) error {
	if strings.TrimSpace(value) == "" || value == "." || value == ".." || strings.Contains(value, "/") || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) {
		return &ServiceError{Code: "INVALID_NAME", Message: "Entry name is invalid"}
	}
	return nil
}

func copyFilename(value string, index int) string {
	extension := path.Ext(value)
	if extension == "" || extension == value {
		return fmt.Sprintf("%s (%d)", value, index)
	}
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(value, extension), index, extension)
}

func siblingWorkspacePath(path WorkspacePath, name string) WorkspacePath {
	parts := strings.Split(path.String(), "/")
	parts[len(parts)-1] = name
	return WorkspacePath{value: strings.Join(parts, "/")}
}

func isWorkspaceDescendant(candidate, ancestor WorkspacePath) bool {
	return candidate.String() == ancestor.String() || strings.HasPrefix(candidate.String(), ancestor.String()+"/")
}

func repeatedOperationFailure(values []string, destination string, err error) []OperationItem {
	items := make([]OperationItem, 0, len(values))
	for _, value := range values {
		items = append(items, operationFailure(value, destination, err))
	}
	return items
}

func operationFailure(source, destination string, err error) OperationItem {
	item := OperationItem{Status: "error", SourcePath: source, DestinationPath: destination}
	var service *ServiceError
	if errors.As(err, &service) {
		item.Error = &OperationError{Code: service.Code, Message: service.Message}
		return item
	}
	switch {
	case errors.Is(err, ErrInvalidWorkspacePath):
		item.Error = &OperationError{Code: "INVALID_PATH", Message: "Path must be relative to the file browser directory"}
	case errors.Is(err, ErrWorkspacePathNotDir):
		item.Error = &OperationError{Code: "NOT_DIRECTORY", Message: "Path is not a directory"}
	default:
		item.Error = &OperationError{Code: "UNAVAILABLE", Message: "Filesystem operation failed"}
	}
	return item
}

func workspaceUnavailable(operation string, err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return &ServiceError{Code: "NOT_FOUND", Message: "Path does not exist"}
	}
	return fmt.Errorf("%w: %s: %v", ErrWorkspaceUnavailable, operation, err)
}
