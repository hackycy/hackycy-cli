package main

import (
	"crypto/sha256"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/updater"
)

var artifactTargets = [][2]string{
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"windows", "amd64"},
	{"windows", "arm64"},
}

func main() {
	directory := flag.String("directory", "", "directory containing the six release artifacts")
	verify := flag.Bool("verify", false, "verify an existing release artifact set")
	sourceRoot := flag.String("source-root", ".", "repository root containing the release inputs")
	flag.Parse()
	if *directory == "" || flag.NArg() != 0 {
		fail(fmt.Errorf("pass only --directory <release-directory>"))
	}
	if *verify {
		if err := verifyReleaseCandidate(*directory, *sourceRoot); err != nil {
			fail(err)
		}
		return
	}
	if err := writeChecksumManifest(*directory); err != nil {
		fail(err)
	}
}

func writeChecksumManifest(directory string) error {
	artifacts, err := releaseArtifacts(directory, false)
	if err != nil {
		return err
	}
	manifest := filepath.Join(directory, updater.ChecksumManifest)
	if _, err := os.Lstat(manifest); err == nil {
		return fmt.Errorf("checksum manifest already exists: %s", manifest)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect checksum manifest: %w", err)
	}

	var contents strings.Builder
	for _, artifact := range artifacts {
		digest, err := fileDigest(artifact.path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(&contents, "%s  %s\n", digest, artifact.name)
	}
	return os.WriteFile(manifest, []byte(contents.String()), 0o644)
}

type releaseArtifact struct {
	name   string
	goos   string
	goarch string
	path   string
}

func releaseArtifacts(directory string, requireChecksumManifest bool) ([]releaseArtifact, error) {
	expected, err := expectedArtifacts()
	if err != nil {
		return nil, err
	}
	expectedByName := make(map[string]releaseArtifact, len(expected))
	for _, artifact := range expected {
		expectedByName[artifact.name] = artifact
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read release directory: %w", err)
	}
	found := make(map[string]bool, len(entries))
	foundChecksumManifest := false
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("release directory contains a non-file entry: %s", entry.Name())
		}
		if entry.Name() == updater.ChecksumManifest {
			if !requireChecksumManifest {
				return nil, fmt.Errorf("checksum manifest already exists: %s", entry.Name())
			}
			foundChecksumManifest = true
			continue
		}
		if _, ok := expectedByName[entry.Name()]; !ok {
			return nil, fmt.Errorf("release directory contains an unexpected artifact: %s", entry.Name())
		}
		if found[entry.Name()] {
			return nil, fmt.Errorf("release directory contains duplicate artifact: %s", entry.Name())
		}
		found[entry.Name()] = true
	}

	if requireChecksumManifest && !foundChecksumManifest {
		return nil, fmt.Errorf("release directory is missing checksum manifest: %s", updater.ChecksumManifest)
	}
	artifacts := make([]releaseArtifact, 0, len(expected))
	for _, expectedArtifact := range expected {
		if !found[expectedArtifact.name] {
			return nil, fmt.Errorf("release directory is missing artifact: %s", expectedArtifact.name)
		}
		expectedArtifact.path = filepath.Join(directory, expectedArtifact.name)
		artifacts = append(artifacts, expectedArtifact)
	}
	return artifacts, nil
}

func expectedArtifacts() ([]releaseArtifact, error) {
	expected := make([]releaseArtifact, 0, len(artifactTargets))
	seen := make(map[string]bool, len(artifactTargets))
	for _, target := range artifactTargets {
		artifact, err := updater.ArtifactFor(target[0], target[1])
		if err != nil {
			return nil, err
		}
		if seen[artifact.Name] {
			return nil, fmt.Errorf("duplicate configured release artifact: %s", artifact.Name)
		}
		seen[artifact.Name] = true
		expected = append(expected, releaseArtifact{name: artifact.Name, goos: artifact.GOOS, goarch: artifact.GOARCH})
	}
	return expected, nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func fail(err error) {
	_, _ = fmt.Fprintf(os.Stderr, "release artifacts: %v\n", err)
	os.Exit(1)
}
