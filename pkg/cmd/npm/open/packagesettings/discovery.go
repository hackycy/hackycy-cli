package packagesettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"go.yaml.in/yaml/v3"
)

const (
	packageManifest       = "package.json"
	pnpmWorkspaceManifest = "pnpm-workspace.yaml"
)

var errNoProject = errors.New("no npm project found; run this command from a project directory")

type manifest map[string]json.RawMessage

func discoverPackages(ctx context.Context, workingDirectory string) ([]string, error) {
	current, err := filepath.Abs(workingDirectory)
	if err != nil {
		return nil, err
	}
	current, err = filepath.EvalSymlinks(current)
	if err != nil {
		return nil, err
	}

	var nearest manifest
	for directory := current; ; directory = filepath.Dir(directory) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		project, names, err := inspectProject(ctx, directory, current)
		if err != nil || names != nil {
			return names, err
		}
		if nearest == nil {
			nearest = project
		}
		boundary, err := gitBoundary(directory)
		if err != nil {
			return nil, err
		}
		if boundary || directory == filepath.Dir(directory) {
			break
		}
	}
	if nearest == nil {
		return nil, errNoProject
	}
	if name := eligibleName(nearest); name != "" {
		return []string{name}, nil
	}
	return []string{}, nil
}

// inspectProject returns a non-nil list only if this directory is the workspace
// that owns the invocation. Otherwise its manifest may still be a single package.
func inspectProject(ctx context.Context, directory, current string) (manifest, []string, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	files := root.FS()
	project, err := readPackage(files, directory, packageManifest)
	if err != nil {
		return nil, nil, err
	}
	workspace, err := readWorkspace(files, directory, project)
	if err != nil || workspace == nil {
		return project, nil, err
	}
	relative, err := filepath.Rel(directory, current)
	if err != nil {
		return nil, nil, err
	}
	owned, err := workspace.containsInvocation(files, filepath.ToSlash(relative))
	if err != nil {
		return nil, nil, fmt.Errorf("inspect workspace %q: %w", directory, err)
	}
	if !owned {
		return project, nil, nil
	}
	names, err := workspace.packages(ctx, files, directory, project)
	return project, names, err
}

func gitBoundary(directory string) (bool, error) {
	entry, err := os.Lstat(filepath.Join(directory, ".git"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return entry.IsDir() || entry.Mode().IsRegular(), nil
}

func readPackage(files fs.FS, directory, file string) (manifest, error) {
	contents, err := readProjectFile(files, directory, file)
	if err != nil || contents == nil {
		return nil, err
	}
	var project manifest
	if err := json.Unmarshal(contents, &project); err != nil {
		return nil, projectFileError(directory, file, err)
	}
	if project == nil {
		return nil, projectFileError(directory, file, errors.New("expected a JSON object"))
	}
	return project, nil
}

// A missing manifest is normal in an ancestor or a globbed directory. A
// present one must be readable, including when it is a link to another file.
func readProjectFile(files fs.FS, directory, file string) ([]byte, error) {
	entry, err := fs.Lstat(files, file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, projectFileError(directory, file, err)
	}
	if entry.Mode()&fs.ModeSymlink != 0 {
		entry, err = fs.Stat(files, file)
		if err != nil {
			return nil, projectFileError(directory, file, err)
		}
	}
	if !entry.Mode().IsRegular() {
		return nil, projectFileError(directory, file, errors.New("expected a regular file"))
	}
	contents, err := fs.ReadFile(files, file)
	if err != nil {
		return nil, projectFileError(directory, file, err)
	}
	return contents, nil
}

func projectFileError(directory, file string, err error) error {
	return fmt.Errorf("read %q: %w", filepath.Join(directory, filepath.FromSlash(file)), err)
}

func eligibleName(project manifest) string {
	var private bool
	_ = json.Unmarshal(project["private"], &private)
	if private {
		return ""
	}
	var name string
	if err := json.Unmarshal(project["name"], &name); err != nil || !validPackageName(name) {
		return ""
	}
	return name
}

var packageNamePart = regexp.MustCompile(`^[a-zA-Z0-9._~!'()*-]+$`)

// Settings may belong to an existing legacy npm package. Accept URL-safe
// legacy names as well, while rejecting reserved names and dot path segments.
func validPackageName(name string) bool {
	if tail, scoped := strings.CutPrefix(name, "@"); scoped {
		scope, local, ok := strings.Cut(tail, "/")
		return ok && scope != "." && scope != ".." && !strings.HasPrefix(local, ".") &&
			packageNamePart.MatchString(scope) && packageNamePart.MatchString(local)
	}
	if name == "" || strings.ContainsRune(".-_", rune(name[0])) {
		return false
	}
	reserved := strings.ToLower(name)
	return reserved != "node_modules" && reserved != "favicon.ico" && packageNamePart.MatchString(name)
}

type workspacePattern struct {
	glob    string
	exclude bool
}

type workspaceDeclaration struct {
	file     string
	patterns []workspacePattern
}

func readWorkspace(files fs.FS, directory string, project manifest) (*workspaceDeclaration, error) {
	contents, err := readProjectFile(files, directory, pnpmWorkspaceManifest)
	if err != nil {
		return nil, err
	}
	if contents != nil {
		var config struct{ Packages []string }
		if err := yaml.Unmarshal(contents, &config); err != nil {
			return nil, projectFileError(directory, pnpmWorkspaceManifest, err)
		}
		return parseWorkspace(directory, pnpmWorkspaceManifest, config.Packages)
	}
	raw, ok := project["workspaces"]
	if !ok {
		return nil, nil
	}
	var entries []string
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, projectFileError(directory, packageManifest, fmt.Errorf("workspaces must be an array of paths: %w", err))
	}
	if entries == nil {
		return nil, projectFileError(directory, packageManifest, errors.New("workspaces must be an array of paths"))
	}
	return parseWorkspace(directory, packageManifest, entries)
}

func parseWorkspace(directory, file string, entries []string) (*workspaceDeclaration, error) {
	workspace := &workspaceDeclaration{file: file}
	for _, entry := range entries {
		glob := strings.TrimLeft(entry, "!")
		exclude := (len(entry)-len(glob))%2 == 1
		glob = strings.ReplaceAll(glob, `\`, "/")
		cleaned := path.Clean(glob)
		if strings.TrimSpace(glob) == "" || !filepath.IsLocal(filepath.FromSlash(cleaned)) || !doublestar.ValidatePattern(cleaned) {
			return nil, projectFileError(directory, file, fmt.Errorf("invalid workspace pattern %q", entry))
		}
		workspace.patterns = append(workspace.patterns, workspacePattern{glob: cleaned, exclude: exclude})
	}
	return workspace, nil
}

func (workspace *workspaceDeclaration) matches(directory string) bool {
	if ignoredWorkspacePath(directory) {
		return false
	}
	included := false
	for _, pattern := range workspace.patterns {
		if !doublestar.MatchUnvalidated(pattern.glob, directory) {
			continue
		}
		if pattern.exclude && workspace.file == pnpmWorkspaceManifest {
			return false
		}
		included = !pattern.exclude
	}
	return included
}

func (workspace *workspaceDeclaration) containsInvocation(files fs.FS, relative string) (bool, error) {
	if relative == "." {
		return true, nil
	}
	for directory := relative; directory != "."; directory = path.Dir(directory) {
		if !workspace.matches(directory) {
			continue
		}
		_, err := fs.Lstat(files, path.Join(directory, packageManifest))
		if !errors.Is(err, fs.ErrNotExist) {
			return err == nil, err
		}
	}
	return false, nil
}

func (workspace *workspaceDeclaration) packages(ctx context.Context, files fs.FS, directory string, rootPackage manifest) ([]string, error) {
	names := make(map[string]struct{})
	if name := eligibleName(rootPackage); name != "" {
		names[name] = struct{}{}
	}
	seen := map[string]bool{".": true}
	for _, pattern := range workspace.patterns {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pattern.exclude || ignoredWorkspacePath(pattern.glob) {
			continue
		}
		err := doublestar.GlobWalk(workspaceFS{FS: files, ctx: ctx}, pattern.glob, func(member string, entry fs.DirEntry) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if !entry.IsDir() || seen[member] || !workspace.matches(member) {
				return nil
			}
			seen[member] = true
			project, err := readPackage(files, directory, path.Join(member, packageManifest))
			if err != nil {
				return err
			}
			if name := eligibleName(project); name != "" {
				names[name] = struct{}{}
			}
			return nil
		}, doublestar.WithFailOnIOErrors(), doublestar.WithNoFollow())
		if err != nil {
			return nil, fmt.Errorf("discover workspace members from %q using %q: %w", filepath.Join(directory, workspace.file), pattern.glob, err)
		}
	}
	sorted := slices.AppendSeq(make([]string, 0, len(names)), maps.Keys(names))
	slices.Sort(sorted)
	return sorted, nil
}

// Filter common non-workspace trees before a broad glob can traverse them.
type workspaceFS struct {
	fs.FS
	ctx context.Context
}

func (files workspaceFS) Open(name string) (fs.File, error) {
	if err := files.ctx.Err(); err != nil {
		return nil, err
	}
	if ignoredWorkspacePath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return files.FS.Open(name)
}

func (files workspaceFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := files.ctx.Err(); err != nil {
		return nil, err
	}
	if ignoredWorkspacePath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	entries, err := fs.ReadDir(files.FS, name)
	return slices.DeleteFunc(entries, func(entry fs.DirEntry) bool { return ignoredWorkspacePath(entry.Name()) }), err
}

func ignoredWorkspacePath(name string) bool {
	for part := range strings.SplitSeq(name, "/") {
		if part == "node_modules" || part == ".git" {
			return true
		}
	}
	return false
}
