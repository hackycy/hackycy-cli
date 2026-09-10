package env

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Discovery contains the usable direct .env files in a directory.
type Discovery struct {
	Directory        string
	BaseFile         string
	EnvironmentFiles []string
}

// Discover finds direct .env files without reading their contents.
func Discover(directory string) (Discovery, error) {
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return Discovery{}, err
	}
	entries, err := os.ReadDir(absoluteDirectory)
	if err != nil {
		return Discovery{}, err
	}

	discovery := Discovery{Directory: absoluteDirectory, EnvironmentFiles: []string{}}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if entry.Name() == ".env" {
			discovery.BaseFile = entry.Name()
			continue
		}
		if _, ok := environmentSuffix(entry.Name()); ok {
			discovery.EnvironmentFiles = append(discovery.EnvironmentFiles, entry.Name())
		}
	}
	sort.Strings(discovery.EnvironmentFiles)
	if discovery.BaseFile == "" && len(discovery.EnvironmentFiles) == 0 {
		return Discovery{}, fmt.Errorf("No .env files found in %s", absoluteDirectory)
	}
	return discovery, nil
}

func environmentSuffix(filename string) (string, bool) {
	if !strings.HasPrefix(filename, ".env") {
		return "", false
	}
	rest := filename[len(".env"):]
	if !strings.HasPrefix(rest, ".") {
		return "", false
	}
	suffix := rest[1:]
	if suffix == "example" || suffix == "sample" {
		return "", false
	}
	return suffix, true
}
