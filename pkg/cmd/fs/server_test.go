package fs

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestRunningServerServesTheComposedFSSiteAndReleasesOnce(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	workspace := openReadOnlyWorkspace(t, root)
	var releases atomic.Int32
	server, err := StartServer(workspace, ServerOptions{
		BindingAddress: "127.0.0.1",
		Port:           0,
		ReadOnly:       ReadOnlyServerOptions{BindingAddress: "127.0.0.1"},
		Release: func() error {
			releases.Add(1)
			return workspace.Close()
		},
	})
	if err != nil {
		t.Fatalf("StartServer returned an error: %v", err)
	}
	if server.Port() == 0 {
		t.Fatal("StartServer did not expose the kernel-assigned port")
	}

	response, err := http.Get(server.URL() + "/api/directory?path=")
	if err != nil {
		t.Fatalf("GET directory: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Security-Policy") != "default-src 'none'; frame-ancestors 'none'" {
		t.Fatalf("directory response = %d headers=%v", response.StatusCode, response.Header)
	}

	if err := server.Close(); err != nil {
		t.Fatalf("Close returned an error: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("second Close returned an error: %v", err)
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("Wait returned an error after Close: %v", err)
	}
	if releases.Load() != 1 {
		t.Fatalf("release count = %d, want 1", releases.Load())
	}
}

func TestRunningServerReleasesResourcesWhenServingStopsUnexpectedly(t *testing.T) {
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	var releases atomic.Int32
	server, err := StartServer(workspace, ServerOptions{
		BindingAddress: "127.0.0.1",
		Port:           0,
		ReadOnly:       ReadOnlyServerOptions{BindingAddress: "127.0.0.1"},
		Release: func() error {
			releases.Add(1)
			return workspace.Close()
		},
	})
	if err != nil {
		t.Fatalf("StartServer returned an error: %v", err)
	}
	if err := server.listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if err := server.Wait(); err == nil {
		t.Fatal("Wait returned nil after an externally closed listener")
	}
	if releases.Load() != 1 {
		t.Fatalf("release count = %d, want 1", releases.Load())
	}
}

func TestStartServerReleasesResourcesWhenBindingFails(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer listener.Close()
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	var releases atomic.Int32
	_, err = StartServer(workspace, ServerOptions{
		BindingAddress: "127.0.0.1",
		Port:           listener.Addr().(*net.TCPAddr).Port,
		ReadOnly:       ReadOnlyServerOptions{BindingAddress: "127.0.0.1"},
		Release: func() error {
			releases.Add(1)
			return workspace.Close()
		},
	})
	if err == nil {
		t.Fatal("StartServer succeeded with an occupied port")
	}
	if releases.Load() != 1 {
		t.Fatalf("release count = %d, want 1", releases.Load())
	}
}

func TestStartServerJoinsBindingAndReleaseFailures(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	defer listener.Close()
	workspace := openReadOnlyWorkspace(t, t.TempDir())
	releaseErr := errors.New("release failed")
	_, err = StartServer(workspace, ServerOptions{
		BindingAddress: "127.0.0.1",
		Port:           listener.Addr().(*net.TCPAddr).Port,
		ReadOnly:       ReadOnlyServerOptions{BindingAddress: "127.0.0.1"},
		Release:        func() error { return releaseErr },
	})
	if !errors.Is(err, releaseErr) {
		t.Fatalf("StartServer error = %v, want joined release error", err)
	}
}
