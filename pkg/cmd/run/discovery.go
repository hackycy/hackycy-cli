// Package run owns the project package-manager runner command.
package run

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
)

var (
	errNoPackage    = errors.New("No package.json found in current directory.")
	errNoScripts    = errors.New("No scripts found in package.json.")
	errNoRunnable   = errors.New("No runnable scripts found in package.json.")
	errPackageParse = errors.New("Failed to parse package.json.")
)

// FileReader is the command-owned package file boundary.
type FileReader interface {
	ReadFile(string) ([]byte, error)
}

// Script is one runnable package script in declaration order.
type Script struct {
	Name    string
	Command string
}

// Discovery describes the selected project and its runnable scripts.
type Discovery struct {
	Directory string
	Scripts   []Script
}

// DiscoverProject resolves a project path and reads its runnable package scripts.
func DiscoverProject(workingDirectory, projectPath string, reader FileReader) (Discovery, error) {
	directory, err := resolveProjectDirectory(workingDirectory, projectPath)
	if err != nil {
		return Discovery{}, err
	}

	contents, err := reader.ReadFile(filepath.Join(directory, "package.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Discovery{}, errNoPackage
		}
		return Discovery{}, errPackageParse
	}

	scripts, err := discoverScripts(contents)
	if err != nil {
		return Discovery{}, err
	}
	return Discovery{Directory: directory, Scripts: scripts}, nil
}

func resolveProjectDirectory(workingDirectory, projectPath string) (string, error) {
	workingDirectory, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", err
	}
	if projectPath == "" {
		return workingDirectory, nil
	}
	if filepath.IsAbs(projectPath) {
		return filepath.Clean(projectPath), nil
	}
	return filepath.Join(workingDirectory, projectPath), nil
}

func discoverScripts(contents []byte) ([]Script, error) {
	trimmed := bytes.TrimSpace(contents)
	if !json.Valid(trimmed) {
		return nil, errPackageParse
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, errNoScripts
	}

	var packageDocument map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &packageDocument); err != nil {
		return nil, errPackageParse
	}
	scriptsDocument, ok := packageDocument["scripts"]
	if !ok || !jsonObject(scriptsDocument) {
		return nil, errNoScripts
	}

	decoder := json.NewDecoder(bytes.NewReader(scriptsDocument))
	if _, err := decoder.Token(); err != nil {
		return nil, errPackageParse
	}

	scripts := make([]Script, 0)
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return nil, errPackageParse
		}
		name, ok := nameToken.(string)
		if !ok {
			return nil, errPackageParse
		}
		var rawValue json.RawMessage
		if err := decoder.Decode(&rawValue); err != nil {
			return nil, errPackageParse
		}
		var command string
		if err := json.Unmarshal(rawValue, &command); err == nil && strings.TrimSpace(command) != "" {
			scripts = append(scripts, Script{Name: name, Command: command})
		}
	}
	if _, err := decoder.Token(); err != nil {
		return nil, errPackageParse
	}
	if len(scripts) == 0 {
		return nil, errNoRunnable
	}
	return scripts, nil
}

func jsonObject(document []byte) bool {
	trimmed := bytes.TrimSpace(document)
	return len(trimmed) > 0 && trimmed[0] == '{'
}
