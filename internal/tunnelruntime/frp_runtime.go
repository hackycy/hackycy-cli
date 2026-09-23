package tunnelruntime

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const frpVersionProbeTimeout = 5 * time.Second

var (
	ErrFRPInstall        = errors.New("FRP installation failed")
	ErrInvalidFRPArchive = errors.New("invalid FRP archive")
	ErrInvalidFRPBinary  = errors.New("invalid FRP binary")
	ErrInvalidFRPVersion = errors.New("invalid FRP version")
	frpVersionExpression = regexp.MustCompile(`(?:^|\D)v?0\.70\.1(?:\D|$)`)
)

type FRPRuntimePaths struct {
	Directory string
	FRPC      string
	FRPS      string
}

type frpVersionVerifier func(context.Context, string) error

// EnsureFRPRuntimeAt materializes the one manifest-pinned frpc/frps pair in
// directory. It deliberately has no PATH or custom-binary fallback.
func EnsureFRPRuntimeAt(ctx context.Context, directory string, artifact FRPArtifact) (FRPRuntimePaths, error) {
	return EnsureFRPRuntimeAtWithObserver(ctx, directory, artifact, nil)
}

// EnsureFRPRuntimeAtWithObserver materializes the pinned runtime while
// reporting optional preparation events.
func EnsureFRPRuntimeAtWithObserver(ctx context.Context, directory string, artifact FRPArtifact, observer FRPRuntimeObserver) (FRPRuntimePaths, error) {
	return ensureFRPRuntimeAt(ctx, directory, artifact, http.DefaultClient, verifyFRPReportedVersion, observer)
}

// PrepareFRPRuntimeAt downloads and materializes a manifest-pinned runtime
// without executing it. This is used when assembling a cross-platform image;
// a builder must be able to prepare an arm64 payload on an amd64 host.
func PrepareFRPRuntimeAt(ctx context.Context, directory string, artifact FRPArtifact) (FRPRuntimePaths, error) {
	return PrepareFRPRuntimeAtWithClient(ctx, directory, artifact, http.DefaultClient)
}

// PrepareFRPRuntimeAtWithClient is the injectable form used by packaging
// tools and tests. The client is only used for the pinned archive URL.
func PrepareFRPRuntimeAtWithClient(ctx context.Context, directory string, artifact FRPArtifact, client *http.Client) (FRPRuntimePaths, error) {
	return ensureFRPRuntimeAt(ctx, directory, artifact, client, func(context.Context, string) error { return nil }, nil)
}

func ensureFRPRuntimeAt(ctx context.Context, directory string, artifact FRPArtifact, client *http.Client, verify frpVersionVerifier, observer FRPRuntimeObserver) (paths FRPRuntimePaths, err error) {
	paths = FRPRuntimePathsFor(directory, artifact.Target)
	if strings.TrimSpace(directory) == "" {
		observer.report(FRPRuntimeEvent{Type: FRPRuntimeEventFailed, FailureStage: FRPRuntimeFailureValidate})
		return FRPRuntimePaths{}, fmt.Errorf("%w: runtime directory is required", ErrFRPInstall)
	}
	if client == nil {
		client = http.DefaultClient
	}
	if verify == nil {
		verify = verifyFRPReportedVersion
	}
	if validFRPRuntime(paths, artifact) {
		observer.report(FRPRuntimeEvent{Type: FRPRuntimeEventReuse, Version: artifact.Description.Version, Directory: paths.Directory})
		if err := verifyFRPRuntime(ctx, paths, artifact.Description.Version, verify, observer); err != nil {
			return failFRPRuntime(paths, artifact, observer, FRPRuntimeFailureProbe, err)
		}
		observer.report(readyFRPRuntimeEvent(paths, false))
		return paths, nil
	}

	downloadStartedAt := time.Now()
	observer.report(FRPRuntimeEvent{
		Type: FRPRuntimeEventDownloadStart, Version: artifact.Description.Version,
		Archive: artifact.Description.Archive, URL: artifact.Description.URL, Directory: paths.Directory,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.Description.URL, nil)
	if err != nil {
		return failFRPRuntime(paths, artifact, observer, FRPRuntimeFailureDownload, fmt.Errorf("create archive request: %w", err))
	}
	response, err := client.Do(request)
	if err != nil {
		return failFRPRuntime(paths, artifact, observer, FRPRuntimeFailureDownload, fmt.Errorf("download archive: %w", err))
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return failFRPRuntime(paths, artifact, observer, FRPRuntimeFailureDownload, fmt.Errorf("download returned HTTP %d", response.StatusCode))
	}
	progress := newFRPDownloadProgressReader(response.Body, response.ContentLength, observer, time.Now)
	progress.start()
	archive, err := io.ReadAll(progress)
	progress.finish()
	if err != nil {
		return failFRPRuntime(paths, artifact, observer, FRPRuntimeFailureDownload, fmt.Errorf("read archive: %w", err))
	}
	observer.report(FRPRuntimeEvent{Type: FRPRuntimeEventDownloadDone, ReceivedBytes: progress.receivedBytes, Elapsed: time.Since(downloadStartedAt)})
	observer.report(FRPRuntimeEvent{Type: FRPRuntimeEventVerifyArchive, SHA256: artifact.Description.SHA256})
	if sha256Hex(archive) != artifact.Description.SHA256 {
		return failFRPRuntime(paths, artifact, observer, FRPRuntimeFailureVerifyArchive, fmt.Errorf("%w: archive SHA-256 does not match", ErrInvalidFRPArchive))
	}
	observer.report(FRPRuntimeEvent{
		Type: FRPRuntimeEventExtract, Archive: artifact.Description.Archive,
		FRPCSHA256: artifact.Description.FRPCSHA256, FRPSSHA256: artifact.FRPSSHA256,
	})
	binaries, err := extractFRPBinaries(archive, artifact)
	if err != nil {
		return failFRPRuntime(paths, artifact, observer, FRPRuntimeFailureExtract, err)
	}
	observer.report(FRPRuntimeEvent{Type: FRPRuntimeEventPublish, Directory: paths.Directory})
	if err := publishFRPRuntime(paths, artifact, binaries); err != nil {
		return failFRPRuntime(paths, artifact, observer, FRPRuntimeFailurePublish, err)
	}
	if err := verifyFRPRuntime(ctx, paths, artifact.Description.Version, verify, observer); err != nil {
		return failFRPRuntime(paths, artifact, observer, FRPRuntimeFailureProbe, err)
	}
	observer.report(readyFRPRuntimeEvent(paths, true))
	return paths, nil
}

func verifyFRPReportedVersion(ctx context.Context, binary string) error {
	return verifyFRPReportedVersionWithin(ctx, binary, frpVersionProbeTimeout)
}

func verifyFRPReportedVersionWithin(ctx context.Context, binary string, timeout time.Duration) error {
	probeContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := exec.CommandContext(probeContext, binary, "--version").CombinedOutput()
	if errors.Is(probeContext.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %s did not respond within %s", ErrInvalidFRPVersion, binary, timeout)
	}
	if err != nil || !frpVersionExpression.Match(output) {
		return fmt.Errorf("%w: %s does not report FRP %s", ErrInvalidFRPVersion, binary, FRPVersion)
	}
	return nil
}

// FRPRuntimePathsFor returns the fixed frpc and frps paths for one runtime
// directory and target.
func FRPRuntimePathsFor(directory string, target WireTarget) FRPRuntimePaths {
	return FRPRuntimePaths{
		Directory: directory,
		FRPC:      filepath.Join(directory, frpExecutableName("frpc", target)),
		FRPS:      filepath.Join(directory, frpExecutableName("frps", target)),
	}
}

func frpExecutableName(role string, target WireTarget) string {
	if target.Platform == WirePlatformWin32 {
		return role + ".exe"
	}
	return role
}

func validFRPRuntime(paths FRPRuntimePaths, artifact FRPArtifact) bool {
	return validFRPBinary(paths.FRPC, artifact.Description.FRPCSHA256) && validFRPBinary(paths.FRPS, artifact.FRPSSHA256)
}

func validFRPBinary(filePath, expectedSHA256 string) bool {
	info, err := os.Lstat(filePath)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		return false
	}
	contents, err := os.ReadFile(filePath)
	return err == nil && sha256Hex(contents) == expectedSHA256
}

func extractFRPBinaries(archive []byte, artifact FRPArtifact) (map[string][]byte, error) {
	if strings.HasSuffix(artifact.Description.Archive, ".zip") {
		return extractFRPZIP(archive, artifact)
	}
	return extractFRPTarGz(archive, artifact)
}

func extractFRPZIP(archive []byte, artifact FRPArtifact) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidFRPArchive, artifact.Description.Archive, err)
	}
	return collectFRPBinaries(artifact, func(role string) ([]byte, bool, error) {
		name := frpExecutableName(role, artifact.Target)
		for _, entry := range reader.File {
			if !frpArchiveEntryMatches(entry.Name, name) || !entry.FileInfo().Mode().IsRegular() {
				continue
			}
			contents, err := readArchiveFile(entry.Open)
			return contents, err == nil, err
		}
		return nil, false, nil
	})
}

func extractFRPTarGz(archive []byte, artifact FRPArtifact) (map[string][]byte, error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("%w: decompress %s: %v", ErrInvalidFRPArchive, artifact.Description.Archive, err)
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	binaries := make(map[string][]byte, 2)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidFRPArchive, artifact.Description.Archive, err)
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			continue
		}
		for _, role := range []string{"frpc", "frps"} {
			if _, found := binaries[role]; found || !frpArchiveEntryMatches(header.Name, frpExecutableName(role, artifact.Target)) {
				continue
			}
			contents, err := io.ReadAll(reader)
			if err != nil {
				return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidFRPArchive, header.Name, err)
			}
			binaries[role] = contents
		}
	}
	return validateFRPBinaries(binaries, artifact)
}

func collectFRPBinaries(artifact FRPArtifact, read func(string) ([]byte, bool, error)) (map[string][]byte, error) {
	binaries := make(map[string][]byte, 2)
	for _, role := range []string{"frpc", "frps"} {
		contents, found, err := read(role)
		if err != nil {
			return nil, fmt.Errorf("%w: read %s: %v", ErrInvalidFRPArchive, role, err)
		}
		if found {
			binaries[role] = contents
		}
	}
	return validateFRPBinaries(binaries, artifact)
}

func validateFRPBinaries(binaries map[string][]byte, artifact FRPArtifact) (map[string][]byte, error) {
	for _, role := range []string{"frpc", "frps"} {
		contents, found := binaries[role]
		if !found {
			return nil, fmt.Errorf("%w: %s does not contain %s", ErrInvalidFRPArchive, artifact.Description.Archive, frpExecutableName(role, artifact.Target))
		}
		expected := artifact.FRPSSHA256
		if role == "frpc" {
			expected = artifact.Description.FRPCSHA256
		}
		if sha256Hex(contents) != expected {
			return nil, fmt.Errorf("%w: extracted %s SHA-256 does not match", ErrInvalidFRPBinary, frpExecutableName(role, artifact.Target))
		}
	}
	return binaries, nil
}

func frpArchiveEntryMatches(entryName, executable string) bool {
	return strings.HasSuffix(path.Clean(entryName), "/"+executable)
}

func readArchiveFile(open func() (io.ReadCloser, error)) ([]byte, error) {
	reader, err := open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func publishFRPRuntime(paths FRPRuntimePaths, artifact FRPArtifact, binaries map[string][]byte) (err error) {
	parent := filepath.Dir(paths.Directory)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create FRP runtime parent: %w", err)
	}
	candidate, err := os.MkdirTemp(parent, ".frp-candidate-")
	if err != nil {
		return fmt.Errorf("create FRP runtime candidate: %w", err)
	}
	defer func() { _ = os.RemoveAll(candidate) }()
	candidatePaths := FRPRuntimePathsFor(candidate, artifact.Target)
	if err := writeFRPBinary(candidatePaths.FRPC, binaries["frpc"]); err != nil {
		return err
	}
	if err := writeFRPBinary(candidatePaths.FRPS, binaries["frps"]); err != nil {
		return err
	}
	if !validFRPRuntime(candidatePaths, artifact) {
		return errors.New("published FRP candidate did not verify")
	}
	if err := replaceFRPRuntimeDirectory(candidate, paths.Directory); err != nil {
		return err
	}
	return nil
}

func writeFRPBinary(filePath string, contents []byte) error {
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return fmt.Errorf("create FRP binary %s: %w", filepath.Base(filePath), err)
	}
	_, writeErr := file.Write(contents)
	if syncErr := file.Sync(); writeErr == nil {
		writeErr = syncErr
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		return fmt.Errorf("write FRP binary %s: %w", filepath.Base(filePath), writeErr)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(filePath, 0o755); err != nil {
			return fmt.Errorf("mark FRP binary executable: %w", err)
		}
	}
	return nil
}

func replaceFRPRuntimeDirectory(candidate, destination string) (err error) {
	backup := destination + ".previous-" + randomFRPIdentifier()
	movedExisting := false
	if err := os.Rename(destination, backup); err == nil {
		movedExisting = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("preserve existing FRP runtime: %w", err)
	}
	if err := os.Rename(candidate, destination); err != nil {
		if movedExisting {
			_ = os.Rename(backup, destination)
		}
		return fmt.Errorf("publish FRP runtime: %w", err)
	}
	if movedExisting {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("remove previous FRP runtime: %w", err)
		}
	}
	return nil
}

func verifyFRPRuntime(ctx context.Context, paths FRPRuntimePaths, version string, verify frpVersionVerifier, observer FRPRuntimeObserver) error {
	for _, binary := range []string{paths.FRPC, paths.FRPS} {
		observer.report(FRPRuntimeEvent{Type: FRPRuntimeEventProbe, Binary: binary, Version: version})
		if err := verify(ctx, binary); err != nil {
			return err
		}
	}
	return nil
}

func readyFRPRuntimeEvent(paths FRPRuntimePaths, downloaded bool) FRPRuntimeEvent {
	return FRPRuntimeEvent{
		Type: FRPRuntimeEventReady, Directory: paths.Directory,
		FRPC: paths.FRPC, FRPS: paths.FRPS, Downloaded: downloaded,
	}
}

func failFRPRuntime(paths FRPRuntimePaths, artifact FRPArtifact, observer FRPRuntimeObserver, stage FRPRuntimeFailureStage, cause error) (FRPRuntimePaths, error) {
	observer.report(FRPRuntimeEvent{Type: FRPRuntimeEventFailed, FailureStage: stage})
	return FRPRuntimePaths{}, installError(paths, artifact, cause)
}

func installError(paths FRPRuntimePaths, artifact FRPArtifact, cause error) error {
	return fmt.Errorf("%w: %s\nReason: %w", ErrFRPInstall, manualFRPInstallMessage(paths, artifact), cause)
}

func manualFRPInstallMessage(paths FRPRuntimePaths, artifact FRPArtifact) string {
	return strings.Join([]string{
		fmt.Sprintf("Could not install FRP %s.", artifact.Description.Version),
		"Official archive: " + artifact.Description.URL,
		"Archive SHA-256: " + artifact.Description.SHA256,
		"Place frpc at: " + paths.FRPC,
		"frpc SHA-256: " + artifact.Description.FRPCSHA256,
		"Place frps at: " + paths.FRPS,
		"frps SHA-256: " + artifact.FRPSSHA256,
	}, "\n")
}

func randomFRPIdentifier() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

func sha256Hex(contents []byte) string {
	digest := sha256.Sum256(contents)
	return hex.EncodeToString(digest[:])
}
