package fs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	ErrWorkspaceNotFound     = errors.New("workspace root not found")
	ErrWorkspaceNotDirectory = errors.New("workspace root is not a directory")
	ErrWorkspaceUnavailable  = errors.New("workspace is unavailable")
	ErrWorkspacePathNotFile  = errors.New("workspace path is not a file")
	ErrWorkspacePathNotDir   = errors.New("workspace path is not a directory")
)

var temporaryWorkspaceNames = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^\.download-[0-9a-f-]{36}\.tmp$`),
	regexp.MustCompile(`(?i)^\.extract-[0-9a-f-]{36}\.tmp(?:\.outer|\.source)?$`),
	regexp.MustCompile(`(?i)^\.edit-[0-9a-f-]{36}\.tmp$`),
	regexp.MustCompile(`(?i)^\.upload-[0-9a-f-]{36}\.tmp$`),
}

type EntryKind string

const (
	EntryKindDirectory   EntryKind = "directory"
	EntryKindFile        EntryKind = "file"
	EntryKindUnavailable EntryKind = "unavailable"
)

type Entry struct {
	Name       string
	Path       WorkspacePath
	Kind       EntryKind
	IsSymlink  bool
	Size       int64
	ModifiedAt time.Time
}

type FileIdentity struct {
	Name       string
	Path       WorkspacePath
	Size       int64
	ModifiedAt time.Time
}

// OpenedFile is an already-contained regular file. It deliberately exposes
// only byte access and identity, never the operating-system path.
type OpenedFile struct {
	file     *os.File
	identity FileIdentity
}

type workspaceRoot interface {
	Close() error
	Open(string) (*os.File, error)
	OpenFile(string, int, os.FileMode) (*os.File, error)
	Lstat(string) (os.FileInfo, error)
	Stat(string) (os.FileInfo, error)
	Mkdir(string, os.FileMode) error
	Remove(string) error
	RemoveAll(string) error
	Rename(string, string) error
	Link(string, string) error
	Symlink(string, string) error
	Readlink(string) (string, error)
}

type workspaceOpenedFileRoot interface {
	OpenWorkspaceFile(string) (*os.File, error)
}

func (file *OpenedFile) Read(data []byte) (int, error) {
	return file.file.Read(data)
}

func (file *OpenedFile) Seek(offset int64, whence int) (int64, error) {
	return file.file.Seek(offset, whence)
}

func (file *OpenedFile) Close() error {
	return file.file.Close()
}

func (file *OpenedFile) Identity() FileIdentity {
	return file.identity
}

// Workspace owns a Browse Root handle. All child access uses WorkspacePath,
// so operating-system paths cannot cross this boundary after construction.
type Workspace struct {
	root          workspaceRoot
	rootDirectory string
	rootName      string
	writes        sync.Mutex
}

func OpenWorkspace(directory string) (*Workspace, error) {
	abs, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrWorkspaceNotFound, abs)
		}
		return nil, fmt.Errorf("%w: resolve workspace root: %v", ErrWorkspaceUnavailable, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrWorkspaceNotFound, abs)
		}
		return nil, fmt.Errorf("%w: inspect workspace root: %v", ErrWorkspaceUnavailable, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrWorkspaceNotDirectory, resolved)
	}
	root, err := openWorkspaceRoot(resolved)
	if err != nil {
		return nil, fmt.Errorf("%w: open workspace root: %v", ErrWorkspaceUnavailable, err)
	}
	return &Workspace{root: root, rootDirectory: resolved, rootName: filepath.Base(resolved)}, nil
}

func (workspace *Workspace) Close() error {
	if err := workspace.root.Close(); err != nil {
		return fmt.Errorf("close workspace root: %w", err)
	}
	return nil
}

func (workspace *Workspace) RootName() string {
	return workspace.rootName
}

func (workspace *Workspace) List(path WorkspacePath) ([]Entry, error) {
	directory, err := workspace.open(path)
	if err != nil {
		return nil, err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil {
		return nil, fmt.Errorf("%w: inspect %s: %v", ErrWorkspaceUnavailable, path.String(), err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrWorkspacePathNotDir, path.String())
	}
	children, err := directory.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("%w: list %s: %v", ErrWorkspaceUnavailable, path.String(), err)
	}

	entries := make([]Entry, 0, len(children))
	for _, child := range children {
		name := child.Name()
		if err := validateWorkspaceEntryName(name); err != nil {
			return nil, fmt.Errorf("%w: directory contains a name that cannot be represented safely", err)
		}
		if isTemporaryWorkspaceName(name) {
			continue
		}
		entries = append(entries, workspace.listEntry(path.child(name)))
	}
	sortWorkspaceEntries(entries)
	return entries, nil
}

func (workspace *Workspace) OpenFile(path WorkspacePath) (*OpenedFile, error) {
	file, err := workspace.openFile(path)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("%w: inspect %s: %v", ErrWorkspaceUnavailable, path.String(), err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("%w: %s", ErrWorkspacePathNotFile, path.String())
	}
	return &OpenedFile{
		file: file,
		identity: FileIdentity{
			Name:       path.baseName(),
			Path:       path,
			Size:       info.Size(),
			ModifiedAt: info.ModTime(),
		},
	}, nil
}

func (workspace *Workspace) open(path WorkspacePath) (*os.File, error) {
	file, err := workspace.root.Open(path.rootName())
	if err != nil {
		return nil, fmt.Errorf("%w: open %s: %v", ErrWorkspaceUnavailable, path.String(), err)
	}
	return file, nil
}

func (workspace *Workspace) openFile(path WorkspacePath) (*os.File, error) {
	if root, ok := workspace.root.(workspaceOpenedFileRoot); ok {
		file, err := root.OpenWorkspaceFile(path.rootName())
		if err != nil {
			return nil, fmt.Errorf("%w: open %s: %v", ErrWorkspaceUnavailable, path.String(), err)
		}
		return file, nil
	}
	return workspace.open(path)
}

func (workspace *Workspace) listEntry(path WorkspacePath) Entry {
	entry := Entry{Name: path.baseName(), Path: path, Kind: EntryKindUnavailable}
	linkInfo, err := workspace.root.Lstat(path.rootName())
	if err != nil {
		return entry
	}
	entry.IsSymlink = linkInfo.Mode()&os.ModeSymlink != 0
	entry.ModifiedAt = linkInfo.ModTime()

	info, err := workspace.root.Stat(path.rootName())
	if err != nil {
		return entry
	}
	if info.IsDir() {
		if err := workspace.probeDirectory(path); err != nil {
			return entry
		}
		entry.Kind = EntryKindDirectory
		entry.ModifiedAt = info.ModTime()
		return entry
	}
	if !info.Mode().IsRegular() {
		return entry
	}
	file, err := workspace.open(path)
	if err != nil {
		return entry
	}
	defer file.Close()
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return entry
	}
	entry.Kind = EntryKindFile
	entry.Size = info.Size()
	entry.ModifiedAt = info.ModTime()
	return entry
}

func (workspace *Workspace) probeDirectory(path WorkspacePath) error {
	directory, err := workspace.open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return ErrWorkspacePathNotDir
	}
	if _, err := directory.ReadDir(1); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func isTemporaryWorkspaceName(name string) bool {
	for _, expression := range temporaryWorkspaceNames {
		if expression.MatchString(name) {
			return true
		}
	}
	return false
}

func utf8VisibleName(name string) bool {
	return utf8.ValidString(name)
}

func validateWorkspaceEntryName(name string) error {
	if !utf8VisibleName(name) {
		return ErrWorkspaceUnavailable
	}
	return nil
}

func sortWorkspaceEntries(entries []Entry) {
	sort.Slice(entries, func(left, right int) bool {
		leftRank := entryKindRank(entries[left].Kind)
		rightRank := entryKindRank(entries[right].Kind)
		if leftRank != rightRank {
			return leftRank < rightRank
		}
		leftFolded := strings.ToLower(entries[left].Name)
		rightFolded := strings.ToLower(entries[right].Name)
		if leftFolded != rightFolded {
			return leftFolded < rightFolded
		}
		return entries[left].Name < entries[right].Name
	})
}

func entryKindRank(kind EntryKind) int {
	switch kind {
	case EntryKindDirectory:
		return 0
	case EntryKindFile:
		return 1
	default:
		return 2
	}
}
