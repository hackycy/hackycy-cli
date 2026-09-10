package tunnelruntime

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const frpVersionFixtureTimeout = 15 * time.Second

func TestEnsureFRPRuntimeAtDownloadsVerifiesAndPublishesOnePair(t *testing.T) {
	archive, artifact := frpTarFixture(t, map[string][]byte{"frpc": []byte("frpc bytes"), "frps": []byte("frps bytes")})
	requests := 0
	client := &http.Client{Transport: frpRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.String() != artifact.Description.URL {
			t.Fatalf("request URL = %q", request.URL)
		}
		return frpHTTPResponse(http.StatusOK, archive), nil
	})}
	var verified []string
	verify := func(_ context.Context, binary string) error {
		verified = append(verified, binary)
		return nil
	}
	directory := filepath.Join(t.TempDir(), "frp", FRPVersion)
	paths, err := ensureFRPRuntimeAt(context.Background(), directory, artifact, client, verify)
	if err != nil {
		t.Fatalf("ensureFRPRuntimeAt() error = %v", err)
	}
	if requests != 1 || paths.Directory != directory || len(verified) != 2 || !validFRPRuntime(paths, artifact) {
		t.Fatalf("runtime = (%#v, requests=%d, verified=%#v)", paths, requests, verified)
	}
	for _, test := range []struct {
		path, want string
	}{{paths.FRPC, "frpc bytes"}, {paths.FRPS, "frps bytes"}} {
		contents, err := os.ReadFile(test.path)
		if err != nil || string(contents) != test.want {
			t.Fatalf("published %s = (%q, %v)", test.path, contents, err)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(test.path)
			if err != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("published mode = (%v, %v)", info.Mode(), err)
			}
		}
	}

	client.Transport = frpRoundTripper(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network must not be used for valid runtime")
	})
	if _, err := ensureFRPRuntimeAt(context.Background(), directory, artifact, client, verify); err != nil || requests != 1 || len(verified) != 4 {
		t.Fatalf("reuse valid runtime = (%v, requests=%d, verified=%#v)", err, requests, verified)
	}
}

func TestEnsureFRPRuntimeAtRejectsBadArchivesWithoutPublishing(t *testing.T) {
	archive, artifact := frpTarFixture(t, map[string][]byte{"frpc": []byte("frpc bytes"), "frps": []byte("frps bytes")})
	artifact.Description.SHA256 = strings.Repeat("0", 64)
	directory := filepath.Join(t.TempDir(), "frp", FRPVersion)
	_, err := ensureFRPRuntimeAt(context.Background(), directory, artifact, &http.Client{Transport: frpRoundTripper(func(*http.Request) (*http.Response, error) {
		return frpHTTPResponse(http.StatusOK, archive), nil
	})}, func(context.Context, string) error { return nil })
	if !errors.Is(err, ErrFRPInstall) || !errors.Is(err, ErrInvalidFRPArchive) {
		t.Fatalf("bad archive error = %v", err)
	}
	if _, statErr := os.Stat(directory); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("runtime directory after failed archive = %v", statErr)
	}
}

func TestExtractFRPBinariesReadsTargetZipAndRejectsMissingRole(t *testing.T) {
	archive := frpZipFixture(t, map[string][]byte{"frpc.exe": []byte("frpc windows"), "frps.exe": []byte("frps windows")})
	artifact := frpTestArtifact("frp_test_windows_amd64.zip", WireTarget{Platform: WirePlatformWin32, Architecture: WireArchitectureX64}, archive, []byte("frpc windows"), []byte("frps windows"))
	binaries, err := extractFRPBinaries(archive, artifact)
	if err != nil || string(binaries["frpc"]) != "frpc windows" || string(binaries["frps"]) != "frps windows" {
		t.Fatalf("extract ZIP = (%#v, %v)", binaries, err)
	}
	missing := frpZipFixture(t, map[string][]byte{"frpc.exe": []byte("frpc windows")})
	_, err = extractFRPBinaries(missing, artifact)
	if !errors.Is(err, ErrInvalidFRPArchive) {
		t.Fatalf("missing FRPS error = %v", err)
	}
}

func TestVerifyFRPReportedVersionUsesPinnedVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture is Unix-only")
	}
	directory := t.TempDir()
	valid := filepath.Join(directory, "frpc")
	if err := os.WriteFile(valid, []byte("#!/bin/sh\necho 'frpc version 0.70.1'\n"), 0o755); err != nil {
		t.Fatalf("write valid version fixture: %v", err)
	}
	if err := verifyFRPReportedVersionWithin(context.Background(), valid, frpVersionFixtureTimeout); err != nil {
		t.Fatalf("verify valid version: %v", err)
	}
	invalid := filepath.Join(directory, "frps")
	if err := os.WriteFile(invalid, []byte("#!/bin/sh\necho 'frps version 0.70.0'\n"), 0o755); err != nil {
		t.Fatalf("write invalid version fixture: %v", err)
	}
	if err := verifyFRPReportedVersionWithin(context.Background(), invalid, frpVersionFixtureTimeout); !errors.Is(err, ErrInvalidFRPVersion) {
		t.Fatalf("verify invalid version error = %v", err)
	}
}

func TestManualFRPInstallMessageNamesBothFixedTargetsAndPins(t *testing.T) {
	_, artifact := frpTarFixture(t, map[string][]byte{"frpc": []byte("frpc"), "frps": []byte("frps")})
	paths := FRPRuntimePathsFor("/state/ycy/frp/0.70.1", artifact.Target)
	message := manualFRPInstallMessage(paths, artifact)
	for _, value := range []string{artifact.Description.URL, artifact.Description.SHA256, artifact.Description.FRPCSHA256, artifact.FRPSSHA256, paths.FRPC, paths.FRPS} {
		if !strings.Contains(message, value) {
			t.Fatalf("manual message omitted %q:\n%s", value, message)
		}
	}
}

func frpTarFixture(t *testing.T, binaries map[string][]byte) ([]byte, FRPArtifact) {
	t.Helper()
	archiveName := "frp_test_darwin_arm64.tar.gz"
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, role := range []string{"frpc", "frps"} {
		contents, found := binaries[role]
		if !found {
			continue
		}
		entryName := "frp_test_darwin_arm64/" + role
		if err := tarWriter.WriteHeader(&tar.Header{Name: entryName, Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tarWriter.Write(contents); err != nil {
			t.Fatalf("write tar contents: %v", err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	archive := compressed.Bytes()
	return archive, frpTestArtifact(archiveName, WireTarget{Platform: WirePlatformDarwin, Architecture: WireArchitectureARM64}, archive, binaries["frpc"], binaries["frps"])
}

func frpZipFixture(t *testing.T, binaries map[string][]byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	for role, contents := range binaries {
		entry, err := writer.Create("frp_test_windows_amd64/" + role)
		if err != nil {
			t.Fatalf("create ZIP entry: %v", err)
		}
		if _, err := entry.Write(contents); err != nil {
			t.Fatalf("write ZIP entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close ZIP: %v", err)
	}
	return archive.Bytes()
}

func frpTestArtifact(archiveName string, target WireTarget, archive, frpc, frps []byte) FRPArtifact {
	return FRPArtifact{
		Target: target,
		Description: FRPArtifactDescription{
			Version: FRPVersion, Archive: archiveName, URL: "https://fixtures.test/" + archiveName,
			SHA256: sha256Hex(archive), FRPCSHA256: sha256Hex(frpc),
		},
		FRPSSHA256: sha256Hex(frps),
	}
}

type frpRoundTripper func(*http.Request) (*http.Response, error)

func (roundTrip frpRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func frpHTTPResponse(status int, body []byte) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}
}
