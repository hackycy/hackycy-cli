package fs

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type ArchiveExtractionResult struct {
	Inspection  ArchiveInspection
	Destination WorkspacePath
}

type ArchiveCapacityProvider func(string) (ArchiveCapacity, error)

type ArchiveExtractionOptions struct {
	Inspector *SevenZipArchiveInspector
	Capacity  ArchiveCapacityProvider
	Progress  func(int)
	OnInspect func(ArchiveInspection)
}

func (workspace *Workspace) ExtractArchive(ctx context.Context, source WorkspacePath, options ArchiveExtractionOptions) (ArchiveExtractionResult, error) {
	workspace.writes.Lock()
	defer workspace.writes.Unlock()
	if err := ctx.Err(); err != nil {
		return ArchiveExtractionResult{}, err
	}
	staging, err := workspace.prepareArchiveStaging(source)
	if err != nil {
		return ArchiveExtractionResult{}, err
	}
	defer staging.Cleanup()
	inspector := options.Inspector
	if inspector == nil {
		inspector = NewSevenZipArchiveInspector()
	}
	capacity := options.Capacity
	if capacity == nil {
		capacity = defaultArchiveCapacity
	}
	outerInspection, err := inspector.Inspect(ctx, staging.sourcePath())
	if err != nil {
		return ArchiveExtractionResult{}, err
	}
	available, err := capacity(filepath.Dir(staging.destinationPath()))
	if err != nil {
		return ArchiveExtractionResult{}, workspaceUnavailable("inspect archive extraction capacity", err)
	}
	if err := requireArchiveCapacity(outerInspection, available); err != nil {
		return ArchiveExtractionResult{}, err
	}
	inspection := outerInspection
	if layeredTarArchiveName(source.baseName()) {
		outer := WorkspacePath{value: staging.destination.String() + ".outer"}
		if err := workspace.root.Mkdir(outer.rootName(), 0o700); err != nil {
			return ArchiveExtractionResult{}, workspaceUnavailable("create layered archive staging directory", err)
		}
		defer workspace.root.RemoveAll(outer.rootName())
		if err := inspector.Extract(ctx, staging.sourcePath(), workspace.absolutePath(outer), scaledArchiveProgress(options.Progress, 0, 35)); err != nil {
			return ArchiveExtractionResult{}, err
		}
		tar, err := onlyExtractedTar(workspace.absolutePath(outer))
		if err != nil {
			return ArchiveExtractionResult{}, err
		}
		inspection, err = inspector.Inspect(ctx, tar)
		if err != nil {
			return ArchiveExtractionResult{}, err
		}
		available, err = capacity(filepath.Dir(staging.destinationPath()))
		if err != nil {
			return ArchiveExtractionResult{}, workspaceUnavailable("inspect archive extraction capacity", err)
		}
		if err := requireArchiveCapacity(inspection, available); err != nil {
			return ArchiveExtractionResult{}, err
		}
		if options.OnInspect != nil {
			options.OnInspect(inspection)
		}
		if err := inspector.Extract(ctx, tar, staging.destinationPath(), scaledArchiveProgress(options.Progress, 35, 65)); err != nil {
			return ArchiveExtractionResult{}, err
		}
	} else {
		if options.OnInspect != nil {
			options.OnInspect(inspection)
		}
		if err := inspector.Extract(ctx, staging.sourcePath(), staging.destinationPath(), options.Progress); err != nil {
			return ArchiveExtractionResult{}, err
		}
	}
	if err := validateArchiveTree(staging.destinationPath()); err != nil {
		return ArchiveExtractionResult{}, err
	}
	directory := parentWorkspacePath(source)
	name, err := workspace.availableCopyDestination(directory, archiveDestinationName(source.baseName()))
	if err != nil {
		return ArchiveExtractionResult{}, err
	}
	if err := workspace.root.Rename(staging.destination.rootName(), name.rootName()); err != nil {
		return ArchiveExtractionResult{}, workspaceUnavailable("publish extracted archive", err)
	}
	return ArchiveExtractionResult{Inspection: inspection, Destination: name}, nil
}

func scaledArchiveProgress(progress func(int), start, span int) func(int) {
	if progress == nil {
		return nil
	}
	return func(value int) {
		progress(start + value*span/100)
	}
}

func onlyExtractedTar(directory string) (string, error) {
	files := make([]string, 0, 1)
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return "", workspaceUnavailable("inspect compressed TAR staging", err)
	}
	if len(files) != 1 || strings.ToLower(filepath.Ext(files[0])) != ".tar" {
		return "", &ServiceError{Code: "INVALID_ARCHIVE", Message: "Compressed TAR archive did not contain one TAR stream"}
	}
	return files[0], nil
}

func validateArchiveTree(directory string) error {
	root, err := filepath.Abs(directory)
	if err != nil {
		return workspaceUnavailable("resolve extracted staging", err)
	}
	root = filepath.Clean(root)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return workspaceUnavailable("resolve extracted staging", err)
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return workspaceUnavailable("inspect extracted entry", err)
		}
		if path == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return workspaceUnavailable("inspect extracted entry", err)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil || filepath.IsAbs(target) || filepath.VolumeName(target) != "" {
				return &ServiceError{Code: "UNAVAILABLE", Message: "Extracted archive contains an unsafe symbolic link"}
			}
			lexical := filepath.Clean(filepath.Join(filepath.Dir(path), target))
			if !withinPath(root, lexical) {
				return &ServiceError{Code: "UNAVAILABLE", Message: "Extracted archive contains an unsafe symbolic link"}
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return &ServiceError{Code: "UNAVAILABLE", Message: "Extracted archive contains an unsafe symbolic link"}
			}
			if !withinPath(resolvedRoot, resolved) {
				return &ServiceError{Code: "UNAVAILABLE", Message: "Extracted archive contains an unsafe symbolic link"}
			}
			return nil
		}
		if info.IsDir() || info.Mode().IsRegular() {
			return nil
		}
		return &ServiceError{Code: "UNAVAILABLE", Message: "Extracted archive contains an unsupported special file"}
	})
}

func withinPath(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
