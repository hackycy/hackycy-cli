package fs

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadOnlyHandlerServesConditionalHeadThumbnailResponses(t *testing.T) {
	root := t.TempDir()
	writeThumbnailServicePNG(t, root, "photo.png")
	workspace := openReadOnlyWorkspace(t, root)
	service := newThumbnailService(workspace, &thumbnailTestConverter{output: []byte("webp")})
	defer service.Close()
	handler := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{Thumbnails: service})
	response := readOnlyResponse(handler, http.MethodGet, "/thumbnails/photo.png", nil)
	if response.Code != http.StatusOK || response.Body.String() != "webp" || response.Header().Get("Content-Type") != "image/webp" || response.Header().Get("Cache-Control") != "no-cache" || response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("thumbnail response = %d %#v %q", response.Code, response.Header(), response.Body.String())
	}
	etag := response.Header().Get("ETag")
	if !strings.HasPrefix(etag, "W/\"thumb-") || response.Header().Get("Last-Modified") == "" {
		t.Fatalf("thumbnail validators = %#v", response.Header())
	}
	cached := readOnlyResponse(handler, http.MethodGet, "/thumbnails/photo.png", map[string]string{"If-None-Match": etag})
	if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 || cached.Header().Get("Content-Length") != "" {
		t.Fatalf("cached thumbnail = %d %#v", cached.Code, cached.Header())
	}
	head := readOnlyResponse(handler, http.MethodHead, "/thumbnails/photo.png", map[string]string{"Range": "bytes=0-1"})
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "4" || head.Header().Get("Accept-Ranges") != "" {
		t.Fatalf("thumbnail HEAD = %d %#v", head.Code, head.Header())
	}
}

func TestReadOnlyHandlerMapsThumbnailFailuresAndAuthentication(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "image.svg"), []byte("<svg/>"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	service := newThumbnailService(workspace, &thumbnailTestConverter{})
	defer service.Close()
	handler := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{Thumbnails: service})
	for _, test := range []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodGet, path: "/thumbnails/image.svg", status: http.StatusNotFound},
		{method: http.MethodPost, path: "/thumbnails/image.svg", status: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: "/thumbnails/../image.svg", status: http.StatusBadRequest},
	} {
		response := readOnlyResponse(handler, test.method, test.path, nil)
		if response.Code != test.status || !strings.Contains(response.Body.String(), "\"code\":\"THUMBNAIL_ERROR\"") && test.status != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s = %d %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	authentication := newTestAuthentication(t)
	authenticated := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{Thumbnails: service, Authentication: authentication})
	response := readOnlyResponse(authenticated, http.MethodGet, "/thumbnails/image.svg", nil)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "AUTHENTICATION_REQUIRED") {
		t.Fatalf("unauthenticated thumbnail = %d %s", response.Code, response.Body.String())
	}
}
