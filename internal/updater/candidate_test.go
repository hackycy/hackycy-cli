package updater

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadCandidateVerifiesBeforeSelfCheck(t *testing.T) {
	content := []byte("candidate bytes")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Length", "999")
		_, _ = writer.Write(content)
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "ycy")
	executed := false
	_, err := DownloadCandidate(context.Background(), ReleaseResolution{
		Version:      "1.2.3",
		ArtifactURL:  server.URL,
		ExpectedHash: sha256Bytes(content),
	}, target, CandidateOptions{
		TransactionID: func() (string, error) { return "tx", nil },
		Executor: func(context.Context, string, []string, []string) (ProcessResult, error) {
			executed = true
			return ProcessResult{Stdout: []byte("1.2.3\n")}, nil
		},
	})
	if err == nil || (!strings.Contains(err.Error(), "truncated") && !strings.Contains(err.Error(), "unexpected EOF")) {
		t.Fatalf("truncated download error = %v", err)
	}
	if executed {
		t.Fatal("candidate executed before complete body verification")
	}
	if _, err := os.Stat(expectedTransactionPath(target, ".new.", "tx")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged candidate remains after failure: %v", err)
	}
}

func TestDownloadCandidateHashesAndSelfChecksPlainVersion(t *testing.T) {
	content := []byte("candidate bytes")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(writer, strings.NewReader(string(content)))
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "ycy")
	got, err := DownloadCandidate(context.Background(), ReleaseResolution{
		Version:      "1.2.3",
		ArtifactURL:  server.URL,
		ExpectedHash: sha256Bytes(content),
	}, target, CandidateOptions{
		TransactionID: func() (string, error) { return "tx", nil },
		Executor: func(_ context.Context, path string, args, environment []string) (ProcessResult, error) {
			if path != expectedTransactionPath(target, ".new.", "tx") || len(args) != 1 || args[0] != "--version" || len(environment) != 0 {
				t.Fatalf("self-check invocation = %s, %#v, %#v", path, args, environment)
			}
			return ProcessResult{Stdout: []byte("1.2.3\n")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Path == "" || got.TransactionID != "tx" || got.ExpectedHash != sha256Bytes(content) {
		t.Fatalf("candidate = %#v", got)
	}
	bytes, err := os.ReadFile(got.Path)
	if err != nil || string(bytes) != string(content) {
		t.Fatalf("candidate bytes = %q, %v", bytes, err)
	}
	assertPrivateUpgradePath(t, got.Path, 0o755)
}

func TestVerifyBinaryRejectsWrongOutputAndExit(t *testing.T) {
	for _, result := range []ProcessResult{{Stdout: []byte("1.2.4\n")}, {Stdout: []byte("1.2.3\n"), ExitCode: 1, Stderr: []byte("failed")}, {Stdout: []byte("\n")}} {
		if err := VerifyBinary(context.Background(), "/tmp/candidate", "1.2.3", func(context.Context, string, []string, []string) (ProcessResult, error) {
			return result, nil
		}, nil); err == nil {
			t.Fatalf("VerifyBinary accepted %#v", result)
		}
	}
	if err := VerifyBinary(context.Background(), "/tmp/candidate", "1.2.3", func(context.Context, string, []string, []string) (ProcessResult, error) {
		return ProcessResult{Stdout: []byte("ycy/1.2.3\n")}, nil
	}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadCandidateRejectsDigestMismatch(t *testing.T) {
	content := []byte("candidate bytes")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write(content)
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "ycy")
	called := false
	_, err := DownloadCandidate(context.Background(), ReleaseResolution{Version: "1.2.3", ArtifactURL: server.URL, ExpectedHash: strings.Repeat("0", 64)}, target, CandidateOptions{
		TransactionID: func() (string, error) { return "mismatch", nil },
		Executor: func(context.Context, string, []string, []string) (ProcessResult, error) {
			called = true
			return ProcessResult{}, nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("digest mismatch error = %v", err)
	}
	if called {
		t.Fatal("self-check ran after digest mismatch")
	}
}
