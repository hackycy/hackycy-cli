package updater

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestArtifactForUsesPublicNames(t *testing.T) {
	tests := []struct {
		goos, goarch, want string
	}{
		{"darwin", "amd64", "ycy-macos-x64"},
		{"darwin", "arm64", "ycy-macos-arm64"},
		{"linux", "amd64", "ycy-linux-x64"},
		{"linux", "arm64", "ycy-linux-arm64"},
		{"windows", "amd64", "ycy-windows-x64.exe"},
		{"windows", "arm64", "ycy-windows-arm64.exe"},
	}
	for _, test := range tests {
		got, err := ArtifactFor(test.goos, test.goarch)
		if err != nil || got.Name != test.want {
			t.Fatalf("ArtifactFor(%q, %q) = %#v, %v; want %q", test.goos, test.goarch, got, err, test.want)
		}
	}
	for _, test := range [][2]string{{"freebsd", "amd64"}, {"linux", "386"}, {"windows", "mips64"}} {
		if _, err := ArtifactFor(test[0], test[1]); err == nil {
			t.Fatalf("ArtifactFor(%q, %q) unexpectedly succeeded", test[0], test[1])
		}
	}
}

func TestCompareVersionsSemverPrecedence(t *testing.T) {
	pairs := []struct {
		left, right string
		want        int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0-alpha", "1.0.0", -1},
		{"1.0.0-alpha.2", "1.0.0-alpha.10", -1},
		{"1.0.0-1", "1.0.0-alpha", -1},
		{"1.0.0+one", "1.0.0+two", 0},
	}
	for _, pair := range pairs {
		got, err := CompareVersions(pair.left, pair.right)
		if err != nil || got != pair.want {
			t.Fatalf("CompareVersions(%q, %q) = %d, %v; want %d", pair.left, pair.right, got, err, pair.want)
		}
	}
	for _, value := range []string{"1.0", "v1.0.0", "01.0.0", "1.0.0-", "1.0.0+", "18446744073709551616.0.0"} {
		if _, err := CompareVersions(value, "1.0.0"); err == nil {
			t.Fatalf("CompareVersions accepted invalid %q", value)
		}
	}
}

func TestParseChecksumManifestAcceptsGNUFormsAndRejectsDuplicates(t *testing.T) {
	manifest := strings.Repeat("a", 64) + "  *ycy-linux-x64\n" + strings.Repeat("b", 64) + " ycy-linux-arm64\n"
	got, err := ParseChecksumManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got["ycy-linux-x64"] != strings.Repeat("a", 64) || got["ycy-linux-arm64"] != strings.Repeat("b", 64) {
		t.Fatalf("parsed checksums = %#v", got)
	}
	if _, err := ParseChecksumManifest(strings.Repeat("a", 64) + " ycy-linux-x64\n" + strings.Repeat("b", 64) + " ycy-linux-x64\n"); err == nil {
		t.Fatal("duplicate checksum unexpectedly accepted")
	}
	if _, err := ParseChecksumManifest("not-a-checksum ycy-linux-x64\n"); err == nil {
		t.Fatal("malformed checksum unexpectedly accepted")
	}
}

func TestResolveReleasePrefersAssetDigestAndBuildsFixedURLs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/latest" {
			t.Fatalf("request path = %s", request.URL.Path)
		}
		if request.Header.Get("Accept") != "application/vnd.github.v3+json" {
			t.Fatalf("Accept = %q", request.Header.Get("Accept"))
		}
		digest := strings.Repeat("A", 64)
		_, _ = io.WriteString(writer, `{"tag_name":"v1.2.3","assets":[{"name":"ycy-linux-x64","digest":"sha256:`+digest+`"}]}`)
	}))
	defer server.Close()
	got, err := ResolveRelease(context.Background(), ReleaseResolverOptions{
		LatestURL:       server.URL + "/latest",
		DownloadBaseURL: server.URL + "/download",
		CurrentVersion:  "1.0.0",
		GOOS:            "linux",
		GOARCH:          "amd64",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "1.2.3" || got.Tag != "v1.2.3" || got.ExpectedHash != strings.Repeat("a", 64) || got.ChecksumSource != ChecksumReleaseDigest || got.ArtifactURL != server.URL+"/download/v1.2.3/ycy-linux-x64" {
		t.Fatalf("resolution = %#v", got)
	}
}

func TestResolveReleaseFallsBackToChecksumManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest":
			_, _ = io.WriteString(writer, `{"tag_name":"v2.0.0","assets":[{"name":"ycy-windows-arm64.exe"}]}`)
		case "/download/v2.0.0/SHA256SUMS":
			_, _ = io.WriteString(writer, strings.Repeat("c", 64)+"  ycy-windows-arm64.exe\n")
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
		}
	}))
	defer server.Close()
	got, err := ResolveRelease(context.Background(), ReleaseResolverOptions{
		LatestURL:       server.URL + "/latest",
		DownloadBaseURL: server.URL + "/download",
		CurrentVersion:  "1.9.9",
		GOOS:            "windows",
		GOARCH:          "arm64",
	})
	if err != nil || got.ExpectedHash != strings.Repeat("c", 64) || got.ChecksumSource != ChecksumManifestFile {
		t.Fatalf("resolution = %#v, %v", got, err)
	}
}

func TestResolveReleaseAlreadyCurrentAndMetadataFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"tag_name":"v1.0.0"}`)
	}))
	defer server.Close()
	_, err := ResolveRelease(context.Background(), ReleaseResolverOptions{LatestURL: server.URL, CurrentVersion: "1.0.0", GOOS: "linux", GOARCH: "amd64"})
	var current *AlreadyCurrentError
	if !errors.As(err, &current) {
		t.Fatalf("already-current error = %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, `{"tag_name":"release-1"}`)
	}))
	defer bad.Close()
	if _, err := ResolveRelease(context.Background(), ReleaseResolverOptions{LatestURL: bad.URL, CurrentVersion: "1.0.0", GOOS: "linux", GOARCH: "amd64"}); err == nil {
		t.Fatal("invalid tag unexpectedly accepted")
	}
}
