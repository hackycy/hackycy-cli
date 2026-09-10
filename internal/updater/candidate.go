package updater

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ProcessResult contains the observable result of a candidate self-check.
type ProcessResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// BinaryExecutor is injected by tests and by the standalone integration probe.
type BinaryExecutor func(context.Context, string, []string, []string) (ProcessResult, error)

// Candidate is a fully downloaded and self-checked staged artifact.
type Candidate struct {
	TransactionID string
	Path          string
	ExpectedHash  string
	Version       string
}

// CandidateOptions supplies local transports and OS adapters.
type CandidateOptions struct {
	Client          HTTPDoer
	Executor        BinaryExecutor
	ClearQuarantine func(string) error
	Chmod           func(string, os.FileMode) error
	Remove          func(string) error
	TransactionID   func() (string, error)
}

// DownloadCandidate writes a same-directory candidate and verifies it before execution.
func DownloadCandidate(ctx context.Context, resolution ReleaseResolution, targetPath string, options CandidateOptions) (Candidate, error) {
	return downloadCandidate(ctx, resolution, targetPath, options, UpgradeObserver{})
}

func downloadCandidate(ctx context.Context, resolution ReleaseResolution, targetPath string, options CandidateOptions, observer UpgradeObserver) (candidate Candidate, resultErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	downloadEvent := UpgradePhaseEvent{CandidateVersion: resolution.Version, ArtifactName: resolution.Artifact.Name}
	observer.begin(UpgradePhaseDownloadCandidate)
	downloadCompleted := false
	defer func() {
		if !downloadCompleted {
			if resultErr == nil {
				observer.complete(UpgradePhaseDownloadCandidate, downloadEvent)
			} else {
				observer.end(ctx, UpgradePhaseDownloadCandidate, resultErr, downloadEvent)
			}
			return
		}
		if resultErr == nil {
			observer.complete(UpgradePhaseVerifyCandidate, downloadEvent)
		} else {
			observer.end(ctx, UpgradePhaseVerifyCandidate, resultErr, downloadEvent)
		}
	}()
	if options.Client == nil {
		options.Client = http.DefaultClient
	}
	if options.Executor == nil {
		options.Executor = executeBinary
	}
	if options.ClearQuarantine == nil {
		options.ClearQuarantine = clearQuarantine
	}
	if options.Chmod == nil {
		options.Chmod = os.Chmod
	}
	if options.Remove == nil {
		options.Remove = os.Remove
	}
	if options.TransactionID == nil {
		options.TransactionID = newTransactionID
	}
	if resolution.ArtifactURL == "" || resolution.ExpectedHash == "" || resolution.Version == "" {
		return Candidate{}, errors.New("candidate resolution is incomplete")
	}
	if !digestPattern.MatchString(resolution.ExpectedHash) {
		return Candidate{}, errors.New("candidate checksum is malformed")
	}
	targetPath, err := filepath.Abs(targetPath)
	if err != nil {
		return Candidate{}, fmt.Errorf("resolve install target: %w", err)
	}
	transactionID, err := options.TransactionID()
	if err != nil {
		return Candidate{}, fmt.Errorf("create update transaction: %w", err)
	}
	if strings.TrimSpace(transactionID) == "" {
		return Candidate{}, errors.New("update transaction ID is empty")
	}
	stagedPath := transactionBinaryPath(targetPath, ".new.", transactionID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, resolution.ArtifactURL, nil)
	if err != nil {
		return Candidate{}, fmt.Errorf("create artifact request: %w", err)
	}
	response, err := options.Client.Do(request)
	if err != nil {
		return Candidate{}, fmt.Errorf("download artifact: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Candidate{}, &HTTPStatusError{URL: resolution.ArtifactURL, Status: response.StatusCode}
	}
	file, err := os.OpenFile(stagedPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Candidate{}, fmt.Errorf("write staged candidate: %w", err)
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = options.Remove(stagedPath)
		}
	}()
	var hasher hash.Hash = sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hasher), response.Body)
	if err != nil {
		return Candidate{}, fmt.Errorf("download artifact: %w", err)
	}
	if response.ContentLength >= 0 && written != response.ContentLength {
		return Candidate{}, fmt.Errorf("download artifact truncated: wrote %d of %d bytes", written, response.ContentLength)
	}
	if written == 0 {
		return Candidate{}, errors.New("downloaded file is empty")
	}
	if err := file.Close(); err != nil {
		return Candidate{}, fmt.Errorf("close staged candidate: %w", err)
	}
	actualHash := fmt.Sprintf("%x", hasher.Sum(nil))
	downloadCompleted = true
	observer.complete(UpgradePhaseDownloadCandidate, downloadEvent)
	observer.begin(UpgradePhaseVerifyCandidate)
	if !strings.EqualFold(actualHash, resolution.ExpectedHash) {
		return Candidate{}, errors.New("checksum verification failed")
	}
	if err := protectUpgradePath(stagedPath, 0o755, options.Chmod); err != nil {
		return Candidate{}, fmt.Errorf("protect staged candidate: %w", err)
	}
	if err := options.ClearQuarantine(stagedPath); err != nil {
		return Candidate{}, err
	}
	if err := VerifyBinary(ctx, stagedPath, resolution.Version, options.Executor, nil); err != nil {
		return Candidate{}, err
	}
	keep = true
	return Candidate{TransactionID: transactionID, Path: stagedPath, ExpectedHash: strings.ToLower(resolution.ExpectedHash), Version: resolution.Version}, nil
}

// VerifyBinary executes only the plain version self-check channel.
func VerifyBinary(ctx context.Context, path, expectedVersion string, executor BinaryExecutor, environment []string) error {
	if executor == nil {
		executor = executeBinary
	}
	result, err := executor(ctx, path, []string{"--version"}, environment)
	if err != nil {
		return fmt.Errorf("candidate self-check failed: %w", err)
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(string(result.Stderr))
		if message == "" {
			message = "candidate self-check exited unsuccessfully"
		}
		return errors.New(message)
	}
	actual := strings.TrimSpace(string(result.Stdout))
	if actual != expectedVersion && !strings.HasPrefix(actual, "ycy/"+expectedVersion) {
		return fmt.Errorf("candidate reported unexpected version: %s", displayVersion(actual))
	}
	return nil
}

func executeBinary(ctx context.Context, path string, arguments, environment []string) (ProcessResult, error) {
	command := exec.CommandContext(ctx, path, arguments...)
	command.Env = append(os.Environ(), environment...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	outputErr := command.Run()
	if outputErr == nil {
		return ProcessResult{Stdout: stdout.Bytes(), ExitCode: 0}, nil
	}
	if exitError, ok := outputErr.(*exec.ExitError); ok {
		return ProcessResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitError.ExitCode()}, nil
	}
	return ProcessResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: -1}, outputErr
}

func clearQuarantine(path string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	command := exec.Command("xattr", "-d", "com.apple.quarantine", path)
	if err := command.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok && exitError.ExitCode() == 1 {
			return nil
		}
		return errors.New("failed to clear macOS quarantine attribute")
	}
	return nil
}

func newTransactionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16]), nil
}

func displayVersion(value string) string {
	if value == "" {
		return "<empty>"
	}
	return value
}
