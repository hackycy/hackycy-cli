package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestPrepareRequiresOutputDirectory(t *testing.T) {
	if err := prepare(context.Background(), ""); err == nil {
		t.Fatal("prepare(\"\") unexpectedly succeeded")
	}
}

func TestPrepareArtifactWritesAndVerifiesBothOutputs(t *testing.T) {
	archive, artifact := testArtifact(t, map[string][]byte{"frpc": []byte("client"), "frps": []byte("server")})
	client := &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
	})}
	output := t.TempDir()
	if err := prepareArtifact(context.Background(), output, artifact, client); err != nil {
		t.Fatalf("prepareArtifact() error = %v", err)
	}
	for _, name := range []string{"frpc", "frps"} {
		path := filepath.Join(output, "linux-x64", name)
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
			t.Fatalf("prepared %s = (%v, %v)", name, info, err)
		}
	}
}

func TestPrepareArtifactRejectsMissingArchiveBinary(t *testing.T) {
	archive, artifact := testArtifact(t, map[string][]byte{"frpc": []byte("client")})
	client := testClient(archive)
	err := prepareArtifact(context.Background(), t.TempDir(), artifact, client)
	if err == nil || !strings.Contains(err.Error(), "does not contain frps") {
		t.Fatalf("missing FRPS error = %v", err)
	}
}

func TestPrepareArtifactRejectsArchiveHashMismatch(t *testing.T) {
	archive, artifact := testArtifact(t, map[string][]byte{"frpc": []byte("client"), "frps": []byte("server")})
	artifact.Description.SHA256 = strings.Repeat("0", 64)
	err := prepareArtifact(context.Background(), t.TempDir(), artifact, testClient(archive))
	if err == nil || !strings.Contains(err.Error(), "archive SHA-256 does not match") {
		t.Fatalf("archive hash error = %v", err)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (r roundTripper) RoundTrip(request *http.Request) (*http.Response, error) { return r(request) }

func testClient(archive []byte) *http.Client {
	return &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(archive)), Header: make(http.Header)}, nil
	})}
}

func testArtifact(t *testing.T, binaries map[string][]byte) ([]byte, tunnelruntime.FRPArtifact) {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, role := range []string{"frpc", "frps"} {
		contents, found := binaries[role]
		if !found {
			continue
		}
		if err := tarWriter.WriteHeader(&tar.Header{Name: "frp_0.70.1_linux_amd64/" + role, Mode: 0o755, Size: int64(len(contents)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	archive := compressed.Bytes()
	artifact := tunnelruntime.FRPArtifact{
		Target: tunnelruntime.WireTarget{Platform: tunnelruntime.WirePlatformLinux, Architecture: tunnelruntime.WireArchitectureX64},
		Description: tunnelruntime.FRPArtifactDescription{
			Version: "0.70.1", Archive: "frp_0.70.1_linux_amd64.tar.gz", URL: "https://example.invalid/frp.tar.gz", SHA256: hexDigest(archive),
			FRPCSHA256: hexDigest(binaries["frpc"]),
		},
		FRPSSHA256: hexDigest(binaries["frps"]),
	}
	return archive, artifact
}

func hexDigest(contents []byte) string { return fmt.Sprintf("%x", sha256.Sum256(contents)) }
