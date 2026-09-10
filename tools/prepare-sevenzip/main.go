package main

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/sevenzipmanifest"
)

const windowsExtractorSHA256 = "56b8cc9f4971cef253644fafe54063ed7fdca551d4dee0f8c6baa81b855acd72"

func main() {
	target := flag.String("target", "", "Go target as os-arch")
	all := flag.Bool("all", false, "prepare every supported target")
	flag.Parse()
	if (*target == "") == !*all {
		fatal(errors.New("pass exactly one of --target or --all"))
	}
	if *all {
		for _, artifact := range sevenzipmanifest.All() {
			if err := prepare(artifact); err != nil {
				fatal(err)
			}
		}
		return
	}
	parts := strings.Split(*target, "-")
	if len(parts) != 2 {
		fatal(fmt.Errorf("unsupported 7-Zip target %q", *target))
	}
	artifact, found := sevenzipmanifest.For(parts[0], parts[1])
	if !found {
		fatal(fmt.Errorf("unsupported 7-Zip target %q", *target))
	}
	if err := prepare(artifact); err != nil {
		fatal(err)
	}
}

func prepare(artifact sevenzipmanifest.Artifact) error {
	target := filepath.Join("internal", "sevenzipruntime", "payload", artifact.Target)
	if validPayload(target, artifact) {
		return nil
	}
	archive, err := download(artifact)
	if err != nil {
		return err
	}
	directory, err := os.MkdirTemp("", "ycy-sevenzip-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	if err := extract(artifact, archive, directory); err != nil {
		return err
	}
	candidate := target + ".candidate"
	if err := os.RemoveAll(candidate); err != nil {
		return err
	}
	if err := os.MkdirAll(candidate, 0o700); err != nil {
		return err
	}
	for _, file := range artifact.Files {
		bytes, err := os.ReadFile(filepath.Join(directory, file.SourceName))
		if err != nil {
			return fmt.Errorf("read %s from %s: %w", file.SourceName, artifact.Asset, err)
		}
		if digest(bytes) != file.SHA256 {
			return fmt.Errorf("extracted %s from %s failed SHA-256 verification", file.SourceName, artifact.Asset)
		}
		mode := os.FileMode(0o600)
		if file.Executable {
			mode = 0o700
		}
		if err := os.WriteFile(filepath.Join(candidate, file.Filename), bytes, mode); err != nil {
			return err
		}
	}
	if !validPayload(candidate, artifact) {
		return fmt.Errorf("prepared 7-Zip payload for %s did not verify", artifact.Target)
	}
	if err := os.RemoveAll(target); err != nil {
		return err
	}
	return os.Rename(candidate, target)
}

func validPayload(directory string, artifact sevenzipmanifest.Artifact) bool {
	for _, file := range artifact.Files {
		info, err := os.Lstat(filepath.Join(directory, file.Filename))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
		if runtime.GOOS != "windows" && file.Executable && info.Mode().Perm()&0o111 == 0 {
			return false
		}
		bytes, err := os.ReadFile(filepath.Join(directory, file.Filename))
		if err != nil || digest(bytes) != file.SHA256 {
			return false
		}
	}
	return true
}

func download(artifact sevenzipmanifest.Artifact) (string, error) {
	directory := filepath.Join(".tmp", "7zip", "downloads")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(directory, artifact.Asset)
	if bytes, err := os.ReadFile(target); err == nil && digest(bytes) == artifact.SHA256 {
		return target, nil
	}
	response, err := (&http.Client{}).Get(sevenzipmanifest.ReleaseBaseURL + "/" + artifact.Asset)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", artifact.Asset, response.StatusCode)
	}
	bytes, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if digest(bytes) != artifact.SHA256 {
		return "", fmt.Errorf("downloaded %s failed SHA-256 verification", artifact.Asset)
	}
	return target, os.WriteFile(target, bytes, 0o600)
}

func extract(artifact sevenzipmanifest.Artifact, archive, destination string) error {
	if artifact.Format == "tar.xz" {
		return run("tar", "-xJf", archive, "-C", destination)
	}
	extractor, err := windowsExtractor()
	if err != nil {
		return err
	}
	return run(extractor, "x", "-y", "-o"+destination, "--", archive)
}

func windowsExtractor() (string, error) {
	for _, name := range []string{"7zz", "7z"} {
		if executable, err := exec.LookPath(name); err == nil {
			return executable, nil
		}
	}
	if artifact, found := sevenzipmanifest.Current(); found && artifact.Format == "tar.xz" {
		candidate := filepath.Join("internal", "sevenzipruntime", "payload", artifact.Target, "7zz")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate, nil
		}
	}
	if runtime.GOOS != "windows" {
		return "", errors.New("preparing Windows 7-Zip payloads requires a prepared host 7zz or 7z in PATH")
	}
	artifact := sevenzipmanifest.Artifact{Asset: "7zr.exe", SHA256: windowsExtractorSHA256}
	return download(artifact)
}

func run(command string, arguments ...string) error {
	output, err := exec.Command(command, arguments...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s failed: %w\n%s", filepath.Base(command), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func digest(bytes []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(bytes))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
