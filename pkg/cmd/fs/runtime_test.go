package fs

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeConstructsServicesAndServerReleasesThem(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntime(RuntimeOptions{
		Directory:         root,
		BindingAddress:    "127.0.0.1",
		Port:              0,
		ManagementEnabled: true,
		Accounts:          []string{"Alice:password"},
		SessionDirectory:  t.TempDir(),
		ChunkedUploads:    true,
		UploadChunkSize:   4 * 1024 * 1024,
	})
	if err != nil {
		t.Fatalf("NewRuntime returned an error: %v", err)
	}
	if runtime.authentication == nil || runtime.chunkedUploads == nil || runtime.downloads == nil || runtime.extractions == nil || runtime.thumbnails == nil {
		t.Fatalf("NewRuntime did not construct every selected service: %#v", runtime)
	}
	server, err := runtime.Start()
	if err != nil {
		_ = runtime.Close()
		t.Fatalf("Runtime.Start returned an error: %v", err)
	}
	response, err := http.Get(server.URL() + "/api/session")
	if err != nil {
		t.Fatalf("GET session: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("session response = %d", response.StatusCode)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("server.Close returned an error: %v", err)
	}
	if _, err := runtime.downloads.Enqueue(DownloadRequest{URL: "https://example.com/file", DirectoryPath: ""}); !serviceCodeIs(err, "DOWNLOAD_SERVICE_STOPPED") {
		t.Fatalf("download after runtime close = %v, want DOWNLOAD_SERVICE_STOPPED", err)
	}
	if _, err := runtime.chunkedUploads.Create("anonymous", mustWorkspacePath(t, ""), "large.bin", 21*1024*1024); !serviceCodeIs(err, "CHUNKED_UPLOAD_STOPPED") {
		t.Fatalf("chunked upload after runtime close = %v, want CHUNKED_UPLOAD_STOPPED", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Runtime.Close returned an error: %v", err)
	}
}

func TestNewRuntimeClosesWorkspaceWhenAuthenticationSetupFails(t *testing.T) {
	root := t.TempDir()
	_, err := NewRuntime(RuntimeOptions{
		Directory:        root,
		Accounts:         []string{"invalid"},
		SessionDirectory: t.TempDir(),
	})
	if err == nil {
		t.Fatal("NewRuntime accepted invalid accounts")
	}
	workspace, openErr := OpenWorkspace(root)
	if openErr != nil {
		t.Fatalf("OpenWorkspace after NewRuntime failure: %v", openErr)
	}
	if closeErr := workspace.Close(); closeErr != nil {
		t.Fatalf("close replacement workspace: %v", closeErr)
	}
}

func TestRuntimeStartRejectsNilRuntime(t *testing.T) {
	var runtime *Runtime
	if server, err := runtime.Start(); err == nil || server != nil {
		t.Fatalf("nil Runtime.Start() = (%v, %v), want nil server and error", server, err)
	}
}

func serviceCodeIs(err error, code string) bool {
	var service *ServiceError
	return errors.As(err, &service) && service.Code == code
}
