package diff

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestHTTPHandlerServesSnapshotBoundBlobsWithExactPresentationHeaders(t *testing.T) {
	baseline, target := comparisonRoots(t)
	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47}
	svgName := "strange ' name.SVG"
	writeComparisonBytes(t, target, "preview.png", pngBytes)
	writeComparisonFile(t, target, svgName, "<svg></svg>")
	writeComparisonBytes(t, target, "archive.bin", []byte{0xff, 0x00, 0x01})
	writeComparisonFile(t, baseline, "deleted.bin", "gone")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	handler := NewHTTPHandler(workspace)
	snapshotID := snapshot.Summary().ID

	png := httpBlobResponse(handler, "/api/entries/"+strconv.Itoa(snapshotEntryID(t, snapshot, "preview.png"))+"/blob/target?snapshot="+snapshotID, "bytes=1-2")
	assertHTTPBlobHeaders(t, png, "image/png", "inline; filename=\"preview.png\"; filename*=UTF-8''preview.png", diffAPICSP)
	if png.Code != http.StatusOK || string(png.Body.Bytes()) != string(pngBytes) || png.Header().Get("Accept-Ranges") != "" {
		t.Fatalf("png Blob response = code %d, headers %v, body %x", png.Code, png.Header(), png.Body.Bytes())
	}

	svg := httpBlobResponse(handler, "/api/entries/"+strconv.Itoa(snapshotEntryID(t, snapshot, svgName))+"/blob/target?snapshot="+snapshotID, "")
	assertHTTPBlobHeaders(t, svg, "image/svg+xml", "inline; filename=\"strange%20%27%20name.SVG\"; filename*=UTF-8''strange%20%27%20name.SVG", "sandbox; default-src 'none'; style-src 'unsafe-inline'")
	if svg.Code != http.StatusOK || svg.Body.String() != "<svg></svg>" {
		t.Fatalf("svg Blob response = code %d, headers %v, body %q", svg.Code, svg.Header(), svg.Body.String())
	}

	ordinary := httpBlobResponse(handler, "/api/entries/"+strconv.Itoa(snapshotEntryID(t, snapshot, "archive.bin"))+"/blob/target?snapshot="+snapshotID, "")
	assertHTTPBlobHeaders(t, ordinary, "application/octet-stream", "attachment; filename=\"archive.bin\"; filename*=UTF-8''archive.bin", diffAPICSP)
	if ordinary.Code != http.StatusOK || string(ordinary.Body.Bytes()) != string([]byte{0xff, 0x00, 0x01}) {
		t.Fatalf("ordinary Blob response = code %d, headers %v, body %x", ordinary.Code, ordinary.Header(), ordinary.Body.Bytes())
	}

	missing := httpBlobResponse(handler, "/api/entries/"+strconv.Itoa(snapshotEntryID(t, snapshot, "deleted.bin"))+"/blob/target?snapshot="+snapshotID, "")
	assertHTTPAPIHeaders(t, missing)
	assertHTTPAPIError(t, missing, http.StatusNotFound, "MISSING", "Blob is missing")
}

func TestHTTPHandlerDoesNotServeStaleBlobBytes(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, target, "stale.bin", "before")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	handler := NewHTTPHandler(workspace)
	if runtime.GOOS == "windows" {
		t.Skip("native reparse-point stale coverage belongs to the Windows suite")
	}
	outside := filepath.Join(filepath.Dir(target), "outside-secret.bin")
	if err := os.WriteFile(outside, []byte("secret bytes that must not be returned"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(target, "stale.bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "stale.bin")); err != nil {
		t.Fatal(err)
	}
	response := httpBlobResponse(handler, "/api/entries/"+strconv.Itoa(snapshotEntryID(t, snapshot, "stale.bin"))+"/blob/target?snapshot="+snapshot.Summary().ID, "")
	assertHTTPAPIHeaders(t, response)
	assertHTTPAPIError(t, response, http.StatusConflict, "STALE", "Blob is stale")
	if strings.Contains(response.Body.String(), "secret bytes") {
		t.Fatalf("stale Blob leaked external data: %s", response.Body.String())
	}
}

func httpBlobResponse(handler http.Handler, target, rangeHeader string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if rangeHeader != "" {
		request.Header.Set("Range", rangeHeader)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertHTTPBlobHeaders(t *testing.T, response *httptest.ResponseRecorder, contentType, disposition, csp string) {
	t.Helper()
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Content-Type") != contentType || response.Header().Get("Content-Disposition") != disposition || response.Header().Get("Content-Security-Policy") != csp || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("Blob headers = %v", response.Header())
	}
}
