package fs

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAuthenticatedTaskEventsCloseWhenSessionIsRevoked(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	authentication := newTestAuthentication(t)
	downloads := NewDownloadManager(workspace)
	defer downloads.Close()
	extractions := newExtractionManager(func(context.Context, WorkspacePath, ArchiveExtractionOptions) (ArchiveExtractionResult, error) {
		return ArchiveExtractionResult{}, nil
	})
	defer extractions.Close()
	handler := NewReadOnlyHandler(workspace, ReadOnlyServerOptions{
		ManagementEnabled: true,
		Authentication:    authentication,
		Downloads:         downloads,
		Extractions:       extractions,
	})
	server := httptest.NewServer(handler)
	defer server.Close()

	for _, endpoint := range []string{"/api/downloads/events", "/api/extractions/events"} {
		grant, err := authentication.SignIn("Alice", "password:with-colon")
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodGet, server.URL+endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.AddCookie(&http.Cookie{Name: "ycy_fs_session", Value: grant.Token})
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream; charset=utf-8" || response.Header.Get("X-Accel-Buffering") != "no" {
			_ = response.Body.Close()
			t.Fatalf("%s response = %d %#v", endpoint, response.StatusCode, response.Header)
		}
		reader := bufio.NewReader(response.Body)
		line, err := reader.ReadString('\n')
		if err != nil || line != "data: {\"version\":1,\"tasks\":[]}\n" {
			_ = response.Body.Close()
			t.Fatalf("%s first SSE line = %q, %v", endpoint, line, err)
		}
		if _, err := reader.ReadString('\n'); err != nil {
			_ = response.Body.Close()
			t.Fatalf("%s SSE frame separator = %v", endpoint, err)
		}
		if err := authentication.SignOut(grant.Token); err != nil {
			_ = response.Body.Close()
			t.Fatal(err)
		}
		ended := make(chan error, 1)
		go func() {
			_, readErr := reader.ReadByte()
			ended <- readErr
		}()
		select {
		case readErr := <-ended:
			if readErr != io.EOF {
				_ = response.Body.Close()
				t.Fatalf("%s stream after revocation = %v, want EOF", endpoint, readErr)
			}
		case <-time.After(time.Second):
			_ = response.Body.Close()
			t.Fatalf("%s stream did not close after revocation", endpoint)
		}
		_ = response.Body.Close()
	}
}
