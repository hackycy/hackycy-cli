package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var legacyRuntime = "b" + "un"

func main() {
	if err := run(".", os.Stdout); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "error: %s\n", err)
		os.Exit(1)
	}
}

func run(root string, output io.Writer) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	if err := rejectObsoleteRootEntries(absRoot); err != nil {
		return err
	}
	var violations []string
	err = filepath.WalkDir(absRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(absRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if skippedDirectory(relative) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				violations = append(violations, relative+": unreadable symlink")
				return nil
			}
			if strings.Contains(filepath.ToSlash(resolved), "legacy/"+legacyRuntime) {
				violations = append(violations, relative+": symlink resolves through frozen legacy")
			}
			return nil
		}
		if !scannedFile(relative) {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if containsForbiddenTooling(string(contents)) {
			violations = append(violations, relative+": active tooling references a forbidden legacy dependency")
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(violations) > 0 {
		return errors.New(strings.Join(violations, "\n"))
	}
	_, err = fmt.Fprintln(output, "active toolchain isolation: clean")
	return err
}

func rejectObsoleteRootEntries(root string) error {
	for _, name := range []string{
		"package.json", legacyRuntime + ".lock", legacyRuntime + ".lockb", legacyRuntime + "fig.toml", "eslint.config.js", "tsconfig.json", "types.d.ts", "Dockerfile", ".dockerignore", "deploy", "src", "node_modules",
	} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			return fmt.Errorf("active obsolete root entry %s is present", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if info, err := os.Stat(filepath.Join(root, ".github", "workflows")); err == nil && info.IsDir() {
		return errors.New("active obsolete workflow directory is present")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func skippedDirectory(relative string) bool {
	switch relative {
	case ".git", "legacy", ".scratch", "mock", "web/node_modules", "web/dist", "tools/lefthook/bin", "build":
		return true
	}
	return false
}

func scannedFile(relative string) bool {
	if relative == "Makefile" || relative == "lefthook.yml" || relative == "lefthook.rc" {
		return true
	}
	extension := strings.ToLower(filepath.Ext(relative))
	switch extension {
	case ".go", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".json", ".yaml", ".yml", ".sh", ".ps1":
		return true
	default:
		return false
	}
}

func containsForbiddenTooling(contents string) bool {
	lower := strings.ToLower(contents)
	lower = strings.ReplaceAll(lower, "check-no-"+legacyRuntime, "")
	lower = strings.ReplaceAll(lower, "legacy/"+legacyRuntime, "")
	if containsToken(lower, legacyRuntime) {
		return true
	}
	for _, forbidden := range []string{"simple-git-" + "hooks", "lint-" + "staged"} {
		if strings.Contains(lower, forbidden) {
			return true
		}
	}
	return false
}

func containsToken(contents, token string) bool {
	for offset := 0; ; {
		index := strings.Index(contents[offset:], token)
		if index < 0 {
			return false
		}
		index += offset
		end := index + len(token)
		beforeOK := index == 0 || !identifierByte(contents[index-1])
		afterOK := end == len(contents) || !identifierByte(contents[end])
		if beforeOK && afterOK {
			return true
		}
		offset = end
	}
}

func identifierByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9' || value == '_'
}
