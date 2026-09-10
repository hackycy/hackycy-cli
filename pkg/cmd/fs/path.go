package fs

import (
	"errors"
	pathpkg "path"
	"strings"
	"unicode/utf8"
)

var ErrInvalidWorkspacePath = errors.New("invalid workspace path")

// WorkspacePath is a slash-separated path relative to a Browse Root.
// It is deliberately distinct from an operating-system path.
type WorkspacePath struct {
	value string
}

func ParseWorkspacePath(value string) (WorkspacePath, error) {
	if !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || hasDrivePrefix(value) {
		return WorkspacePath{}, ErrInvalidWorkspacePath
	}

	segments := strings.Split(value, "/")
	normalized := make([]string, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			continue
		}
		if segment == "." || segment == ".." {
			return WorkspacePath{}, ErrInvalidWorkspacePath
		}
		normalized = append(normalized, segment)
	}
	return WorkspacePath{value: strings.Join(normalized, "/")}, nil
}

func (path WorkspacePath) String() string {
	return path.value
}

func (path WorkspacePath) rootName() string {
	if path.value == "" {
		return "."
	}
	return path.value
}

func (path WorkspacePath) child(name string) WorkspacePath {
	if path.value == "" {
		return WorkspacePath{value: name}
	}
	return WorkspacePath{value: path.value + "/" + name}
}

func (path WorkspacePath) baseName() string {
	return pathpkg.Base(path.value)
}

func hasDrivePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}
