package main

import (
	"bytes"
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"debug/pe"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"github.com/hackycy/hackycy-cli/internal/updater"
)

const releaseVersion = "0.1.0"

func verifyReleaseCandidate(directory, sourceRoot string) error {
	artifacts, err := releaseArtifacts(directory, true)
	if err != nil {
		return err
	}
	if err := verifyChecksumManifest(directory, artifacts); err != nil {
		return err
	}
	if err := verifyReleaseInputs(sourceRoot); err != nil {
		return err
	}
	for _, artifact := range artifacts {
		binary, err := os.ReadFile(artifact.path)
		if err != nil {
			return fmt.Errorf("read %s: %w", artifact.name, err)
		}
		if len(binary) == 0 {
			return fmt.Errorf("artifact is empty: %s", artifact.name)
		}
		if err := verifyExecutableFormat(artifact); err != nil {
			return err
		}
		if err := verifyBuildMetadata(artifact); err != nil {
			return err
		}
		if !bytes.Contains(binary, []byte(releaseVersion)) {
			return fmt.Errorf("artifact does not contain release identity %s: %s", releaseVersion, artifact.name)
		}
		if err := verifyEmbeddedWeb(binary, sourceRoot, artifact.name); err != nil {
			return err
		}
		if err := verifyEmbeddedSevenZip(binary, sourceRoot, artifact); err != nil {
			return err
		}
		if err := verifyFRPManifest(binary, artifact.name); err != nil {
			return err
		}
	}
	return nil
}

func verifyChecksumManifest(directory string, artifacts []releaseArtifact) error {
	contents, err := os.ReadFile(filepath.Join(directory, updater.ChecksumManifest))
	if err != nil {
		return fmt.Errorf("read checksum manifest: %w", err)
	}
	checksums, err := updater.ParseChecksumManifest(string(contents))
	if err != nil {
		return fmt.Errorf("parse checksum manifest: %w", err)
	}
	if len(checksums) != len(artifacts) {
		return fmt.Errorf("checksum manifest entries = %d, want %d", len(checksums), len(artifacts))
	}
	for _, artifact := range artifacts {
		digest, err := fileDigest(artifact.path)
		if err != nil {
			return err
		}
		if checksums[artifact.name] != digest {
			return fmt.Errorf("checksum mismatch for %s", artifact.name)
		}
	}
	return nil
}

func verifyExecutableFormat(artifact releaseArtifact) error {
	switch artifact.goos {
	case "darwin":
		file, err := macho.Open(artifact.path)
		if err != nil {
			return fmt.Errorf("%s is not a Mach-O executable: %w", artifact.name, err)
		}
		defer file.Close()
		want := macho.CpuAmd64
		if artifact.goarch == "arm64" {
			want = macho.CpuArm64
		}
		if file.Cpu != want {
			return fmt.Errorf("%s Mach-O CPU = %s, want %s", artifact.name, file.Cpu, want)
		}
		libraries, err := file.ImportedLibraries()
		if err != nil {
			return fmt.Errorf("inspect Mach-O libraries for %s: %w", artifact.name, err)
		}
		return rejectLegacyRuntimeLibraries(artifact.name, libraries)
	case "linux":
		file, err := elf.Open(artifact.path)
		if err != nil {
			return fmt.Errorf("%s is not an ELF executable: %w", artifact.name, err)
		}
		defer file.Close()
		want := elf.EM_X86_64
		if artifact.goarch == "arm64" {
			want = elf.EM_AARCH64
		}
		if file.Machine != want || file.Class != elf.ELFCLASS64 {
			return fmt.Errorf("%s ELF target = %s/%s, want %s/ELFCLASS64", artifact.name, file.Machine, file.Class, want)
		}
		libraries, err := file.ImportedLibraries()
		if err != nil {
			return fmt.Errorf("inspect ELF libraries for %s: %w", artifact.name, err)
		}
		if len(libraries) != 0 {
			return fmt.Errorf("CGO-free Linux artifact imports libraries: %s", artifact.name)
		}
		return rejectLegacyRuntimeLibraries(artifact.name, libraries)
	case "windows":
		file, err := pe.Open(artifact.path)
		if err != nil {
			return fmt.Errorf("%s is not a PE executable: %w", artifact.name, err)
		}
		defer file.Close()
		want := uint16(pe.IMAGE_FILE_MACHINE_AMD64)
		if artifact.goarch == "arm64" {
			want = pe.IMAGE_FILE_MACHINE_ARM64
		}
		if file.Machine != want {
			return fmt.Errorf("%s PE CPU = %#x, want %#x", artifact.name, file.Machine, want)
		}
		libraries, err := file.ImportedLibraries()
		if err != nil {
			return fmt.Errorf("inspect PE libraries for %s: %w", artifact.name, err)
		}
		return rejectLegacyRuntimeLibraries(artifact.name, libraries)
	default:
		return fmt.Errorf("unsupported artifact target: %s", artifact.name)
	}
}

func rejectLegacyRuntimeLibraries(name string, libraries []string) error {
	legacyRuntime := "b" + "un"
	for _, library := range libraries {
		lower := strings.ToLower(library)
		if strings.Contains(lower, legacyRuntime) || strings.Contains(lower, "node") {
			return fmt.Errorf("artifact imports a forbidden runtime library: %s -> %s", name, library)
		}
	}
	return nil
}

func verifyBuildMetadata(artifact releaseArtifact) error {
	info, err := buildinfo.ReadFile(artifact.path)
	if err != nil {
		return fmt.Errorf("read Go build metadata for %s: %w", artifact.name, err)
	}
	if info.GoVersion != "go1.26.7" {
		return fmt.Errorf("%s Go version = %s, want go1.26.7", artifact.name, info.GoVersion)
	}
	if info.Path != "github.com/hackycy/hackycy-cli/cmd/ycy" {
		return fmt.Errorf("%s Go main path = %s", artifact.name, info.Path)
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	for key, want := range map[string]string{"CGO_ENABLED": "0", "GOOS": artifact.goos, "GOARCH": artifact.goarch} {
		if settings[key] != want {
			return fmt.Errorf("%s build setting %s = %q, want %q", artifact.name, key, settings[key], want)
		}
	}
	dependencies := make(map[string]string, len(info.Deps))
	for _, dependency := range info.Deps {
		dependencies[dependency.Path] = dependency.Version
	}
	for path, version := range map[string]string{
		"github.com/gen2brain/gav1d": "v0.2.5",
		"github.com/gen2brain/vpx":   "v0.2.1",
	} {
		if dependencies[path] != version {
			return fmt.Errorf("%s does not record required thumbnail dependency %s %s", artifact.name, path, version)
		}
	}
	return nil
}

func verifyReleaseInputs(sourceRoot string) error {
	notices, err := os.ReadFile(filepath.Join(sourceRoot, "THIRD_PARTY_NOTICES.md"))
	if err != nil {
		return fmt.Errorf("read third-party notices: %w", err)
	}
	for _, required := range []string{
		"github.com/gen2brain/gav1d v0.2.5",
		"github.com/gen2brain/vpx v0.2.1",
		"Alliance for Open Media Patent License 1.0",
		"Additional IP Rights Grant (Patents)",
	} {
		if !bytes.Contains(notices, []byte(required)) {
			return fmt.Errorf("third-party notices are missing %q", required)
		}
	}
	return verifyNoEmbeddedFRP(sourceRoot)
}

func verifyEmbeddedWeb(binary []byte, sourceRoot, artifactName string) error {
	root := filepath.Join(sourceRoot, "web", "dist")
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if relative == ".vite" {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("Web graph contains non-file entry: %s", relative)
		}
		if !bytes.Contains(binary, []byte(relative)) {
			return fmt.Errorf("%s does not embed Web asset %s", artifactName, relative)
		}
		return nil
	})
}

func verifyEmbeddedSevenZip(binary []byte, sourceRoot string, artifact releaseArtifact) error {
	payloadRoot := filepath.Join(sourceRoot, "internal", "sevenzipruntime", "payload")
	target := artifact.goos + "-" + artifact.goarch
	expectedDirectory := filepath.Join(payloadRoot, target)
	expected, err := payloadContents(expectedDirectory)
	if err != nil {
		return err
	}
	for name, contents := range expected {
		if !bytes.Contains(binary, contents) {
			return fmt.Errorf("%s does not embed target 7-Zip payload %s", artifact.name, name)
		}
	}
	entries, err := os.ReadDir(payloadRoot)
	if err != nil {
		return fmt.Errorf("read 7-Zip payload root: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == target {
			continue
		}
		other, err := payloadContents(filepath.Join(payloadRoot, entry.Name()))
		if err != nil {
			return err
		}
		for name, contents := range other {
			if containsPayload(expected, contents) {
				continue
			}
			if bytes.Contains(binary, contents) {
				return fmt.Errorf("%s embeds non-target 7-Zip payload %s/%s", artifact.name, entry.Name(), name)
			}
		}
	}
	return nil
}

func payloadContents(directory string) (map[string][]byte, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read 7-Zip payload %s: %w", directory, err)
	}
	contents := make(map[string][]byte, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return nil, fmt.Errorf("7-Zip payload contains non-file entry: %s", entry.Name())
		}
		bytes, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return nil, err
		}
		contents[entry.Name()] = bytes
	}
	if len(contents) == 0 {
		return nil, fmt.Errorf("7-Zip payload is empty: %s", directory)
	}
	return contents, nil
}

func containsPayload(payload map[string][]byte, candidate []byte) bool {
	for _, contents := range payload {
		if bytes.Equal(contents, candidate) {
			return true
		}
	}
	return false
}

func verifyFRPManifest(binary []byte, artifactName string) error {
	for _, artifact := range tunnelruntime.FRPArtifacts() {
		for _, value := range []string{
			artifact.Description.Archive,
			artifact.Description.SHA256,
			artifact.Description.FRPCSHA256,
			artifact.FRPSSHA256,
		} {
			if !bytes.Contains(binary, []byte(value)) {
				return fmt.Errorf("%s does not embed FRP manifest value %s", artifactName, value)
			}
		}
	}
	return nil
}

func verifyNoEmbeddedFRP(sourceRoot string) error {
	root := filepath.Join(sourceRoot, "internal", "tunnelruntime")
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(contents), "//go:embed") {
			return fmt.Errorf("Tunnel source embeds a file instead of acquiring FRP at runtime: %s", path)
		}
		return nil
	})
}
