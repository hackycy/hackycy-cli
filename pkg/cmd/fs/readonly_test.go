package fs

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadDirectoryBuildsBrowserEntriesFromContainedWorkspacePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "notes.TXT"), []byte("notes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "photo.PNG"), []byte("not decoded here"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "archive.TAR.GZ"), []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	listing, err := workspace.ReadDirectory(mustWorkspacePath(t, ""), false, nil)
	if err != nil {
		t.Fatalf("ReadDirectory() error = %v", err)
	}
	if listing.RootName != filepath.Base(root) || listing.Path != "" || listing.ParentPath != "" || listing.ManagementEnabled || listing.MaxUploadBytes != 1024*1024*1024 {
		t.Fatalf("listing = %#v", listing)
	}
	entries := map[string]DirectoryEntry{}
	for _, entry := range listing.Entries {
		entries[entry.Name] = entry
	}
	docs := entries["docs"]
	if docs.Kind != EntryKindDirectory || docs.BrowseURL != "/browse/docs" || docs.FileURL != "" || docs.PreviewKind != PreviewNone {
		t.Fatalf("directory entry = %#v", docs)
	}
	notes := entries["notes.TXT"]
	if notes.Kind != EntryKindFile || notes.Size == nil || *notes.Size != 5 || notes.MIMEType != "text/plain;charset=utf-8" || notes.PreviewKind != PreviewText || notes.FileURL != "/files/notes.TXT" || notes.DownloadURL != "/files/notes.TXT?download=1" || notes.ThumbnailURL != "" || notes.Extractable {
		t.Fatalf("text entry = %#v", notes)
	}
	photo := entries["photo.PNG"]
	if photo.MIMEType != "image/png" || photo.PreviewKind != PreviewImage || photo.ThumbnailURL != "/thumbnails/photo.PNG" {
		t.Fatalf("image entry = %#v", photo)
	}
	archive := entries["archive.TAR.GZ"]
	if !archive.Extractable {
		t.Fatalf("archive entry = %#v, want extractable", archive)
	}
}

func TestReadTextPreservesSupportedEncodingAndClassifiesInvalidOrLargeInput(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"utf8.txt":    append([]byte{0xef, 0xbb, 0xbf}, []byte("hello\x00world")...),
		"utf16le.txt": {0xff, 0xfe, 'h', 0, 'i', 0},
		"utf16be.txt": {0xfe, 0xff, 0, 'h', 0, 'i'},
		"binary.bin":  {0xc3, 0x28},
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(root, name), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	large, err := os.Create(filepath.Join(root, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := large.Truncate(MaxTextPreviewBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := large.Close(); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	ready, err := workspace.ReadText(mustWorkspacePath(t, "utf8.txt"))
	if err != nil || ready.Status != "ready" || ready.Text != "hello\x00world" || ready.Encoding != "utf-8" || ready.Size != int64(len(files["utf8.txt"])) || len(ready.Revision) != 64 {
		t.Fatalf("utf8 preview = %#v, error = %v", ready, err)
	}
	for _, name := range []string{"utf16le.txt", "utf16be.txt"} {
		preview, err := workspace.ReadText(mustWorkspacePath(t, name))
		if err != nil || preview.Status != "ready" || preview.Text != "hi" {
			t.Fatalf("%s preview = %#v, error = %v", name, preview, err)
		}
	}
	binaryPreview, err := workspace.ReadText(mustWorkspacePath(t, "binary.bin"))
	if err != nil || binaryPreview.Status != "binary" || binaryPreview.Size != 2 {
		t.Fatalf("binary preview = %#v, error = %v", binaryPreview, err)
	}
	tooLarge, err := workspace.ReadText(mustWorkspacePath(t, "large.bin"))
	if err != nil || tooLarge.Status != "too_large" || tooLarge.Size != MaxTextPreviewBytes+1 || tooLarge.MaxBytes != MaxTextPreviewBytes {
		t.Fatalf("large preview = %#v, error = %v", tooLarge, err)
	}
}

func TestReadOnlyHandlerServesDirectoryAndTextWithProtocolHeaders(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "binary.txt"), []byte{0xc3, 0x28}, 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewReadOnlyHandler(openReadOnlyWorkspace(t, root), ReadOnlyServerOptions{})
	directory := readOnlyResponse(handler, http.MethodGet, "/api/directory?path=", nil)
	if directory.Code != http.StatusOK || directory.Header().Get("Cache-Control") != "no-store" || directory.Header().Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" {
		t.Fatalf("directory response = %#v", directory.Result())
	}
	var listing struct {
		Version int              `json:"version"`
		Entries []DirectoryEntry `json:"entries"`
	}
	if err := json.NewDecoder(directory.Body).Decode(&listing); err != nil || listing.Version != 1 || len(listing.Entries) != 2 {
		t.Fatalf("directory JSON = %#v, error = %v", listing, err)
	}
	text := readOnlyResponse(handler, http.MethodGet, "/api/text?path=hello.txt", nil)
	var preview struct {
		Version int `json:"version"`
		TextPreview
	}
	if err := json.NewDecoder(text.Body).Decode(&preview); err != nil || preview.Version != 1 || preview.Status != "ready" || preview.Text != "hello world" {
		t.Fatalf("text JSON = %#v, error = %v", preview, err)
	}
	binary := readOnlyResponse(handler, http.MethodGet, "/api/text?path=binary.txt", nil)
	if !strings.Contains(binary.Body.String(), `"status":"binary"`) {
		t.Fatalf("binary response = %q", binary.Body.String())
	}
	for _, target := range []string{"/api/directory?path=../outside", "/api/text?path=%ZZ"} {
		response := readOnlyResponse(handler, http.MethodGet, target, nil)
		if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"INVALID_PATH"`) {
			t.Fatalf("%s response = %d %s", target, response.Code, response.Body.String())
		}
	}
	method := readOnlyResponse(handler, http.MethodPost, "/api/text?path=hello.txt", nil)
	if method.Code != http.StatusMethodNotAllowed {
		t.Fatalf("text method = %d", method.Code)
	}
}

func TestReadOnlyHandlerServesOriginalFilesWithConditionalsRangesAndPolicies(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "hello file.txt"), []byte("hello world"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "page.html"), []byte("<script>active</script>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "vector.svg"), []byte("<svg/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := NewReadOnlyHandler(openReadOnlyWorkspace(t, root), ReadOnlyServerOptions{})
	file := readOnlyResponse(handler, http.MethodGet, "/files/hello%20file.txt", nil)
	if file.Code != http.StatusOK || file.Body.String() != "hello world" || file.Header().Get("Content-Type") != "text/plain;charset=utf-8" || !strings.HasPrefix(file.Header().Get("Content-Disposition"), "inline;") || file.Header().Get("Access-Control-Allow-Origin") != "*" || file.Header().Get("Accept-Ranges") != "bytes" {
		t.Fatalf("file response = %d %#v %q", file.Code, file.Header(), file.Body.String())
	}
	etag := file.Header().Get("ETag")
	cached := readOnlyResponse(handler, http.MethodGet, "/files/hello%20file.txt", map[string]string{"If-None-Match": etag})
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
		t.Fatalf("cached response = %d %q", cached.Code, cached.Body.String())
	}
	ranged := readOnlyResponse(handler, http.MethodGet, "/files/hello%20file.txt", map[string]string{"Range": "bytes=0-4"})
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "hello" || ranged.Header().Get("Content-Range") != "bytes 0-4/11" {
		t.Fatalf("range response = %d %#v %q", ranged.Code, ranged.Header(), ranged.Body.String())
	}
	suffix := readOnlyResponse(handler, http.MethodHead, "/files/hello%20file.txt", map[string]string{"Range": "bytes=-5"})
	if suffix.Code != http.StatusPartialContent || suffix.Body.Len() != 0 || suffix.Header().Get("Content-Length") != "5" {
		t.Fatalf("head range response = %d %#v", suffix.Code, suffix.Header())
	}
	invalid := readOnlyResponse(handler, http.MethodGet, "/files/hello%20file.txt", map[string]string{"Range": "bytes=0-1,4-5"})
	if invalid.Code != http.StatusRequestedRangeNotSatisfiable || invalid.Header().Get("Content-Range") != "bytes */11" {
		t.Fatalf("invalid range response = %d %#v", invalid.Code, invalid.Header())
	}
	directory := readOnlyResponse(handler, http.MethodGet, "/files/docs", nil)
	if directory.Code != http.StatusFound || directory.Header().Get("Location") != "/browse/docs" {
		t.Fatalf("directory response = %d %#v", directory.Code, directory.Header())
	}
	html := readOnlyResponse(handler, http.MethodGet, "/files/page.html", nil)
	if !strings.HasPrefix(html.Header().Get("Content-Disposition"), "inline;") || html.Header().Get("Content-Security-Policy") != "" {
		t.Fatalf("html response = %#v", html.Header())
	}
	svg := readOnlyResponse(handler, http.MethodGet, "/files/vector.svg", nil)
	if !strings.Contains(svg.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("svg response = %#v", svg.Header())
	}
	safe := NewReadOnlyHandler(openReadOnlyWorkspace(t, root), ReadOnlyServerOptions{SafeHTML: true})
	safeHTML := readOnlyResponse(safe, http.MethodGet, "/files/page.html", nil)
	if !strings.HasPrefix(safeHTML.Header().Get("Content-Disposition"), "attachment;") || !strings.Contains(safeHTML.Header().Get("Content-Security-Policy"), "sandbox") || safeHTML.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("safe HTML response = %#v", safeHTML.Header())
	}
	options := readOnlyResponse(handler, http.MethodOptions, "/files/hello%20file.txt", nil)
	if options.Code != http.StatusNoContent || options.Header().Get("Access-Control-Allow-Methods") != "GET, HEAD, OPTIONS" {
		t.Fatalf("options response = %d %#v", options.Code, options.Header())
	}
}

func TestReadOnlyResourcePathDecodingRejectsInvalidAndRetainsEncodedSlashes(t *testing.T) {
	path, err := resourceWorkspacePath(&url.URL{Path: "/files/dir/name.txt", RawPath: "/files/dir%2Fname.txt"}, "/files")
	if err != nil || path.String() != "dir/name.txt" {
		t.Fatalf("resourceWorkspacePath() = %q, %v", path.String(), err)
	}
	if _, err := resourceWorkspacePath(&url.URL{Path: "/files/../secret"}, "/files"); !errors.Is(err, ErrInvalidWorkspacePath) {
		t.Fatalf("invalid resource path error = %v", err)
	}
}

func openReadOnlyWorkspace(t *testing.T, root string) *Workspace {
	t.Helper()
	workspace, err := OpenWorkspace(root)
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	return workspace
}

func readOnlyResponse(handler http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestReadonlyFormatUsesUTCMilliseconds(t *testing.T) {
	entry := makeDirectoryEntry(Entry{Name: "file.txt", Path: WorkspacePath{value: "file.txt"}, Kind: EntryKindFile, ModifiedAt: time.Date(2026, time.August, 24, 1, 2, 3, 456_789_000, time.FixedZone("other", 3600))})
	if entry.ModifiedAt != "2026-08-24T00:02:03.456Z" {
		t.Fatalf("ModifiedAt = %q", entry.ModifiedAt)
	}
	if !bytes.Equal([]byte(encodeFilename("space and quote'\".txt")), []byte("space%20and%20quote%27%22.txt")) {
		t.Fatalf("encoded filename = %q", encodeFilename("space and quote'\".txt"))
	}
}
