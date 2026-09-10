package fs

import (
	"context"
	"net/netip"
	"strconv"
	"testing"
)

func TestModuleStartsBoundFileBrowserThenTreatsContextCancellationAsSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	module, err := New(Dependencies{
		NetworkInterfaces: func() ([]NetworkInterface, error) {
			return []NetworkInterface{
				{Internal: true, Addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
				{Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.44"), netip.MustParseAddr("2001:db8::44")}},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	operation, err := module.Start(ctx, Input{
		Directory:         t.TempDir(),
		Address:           "0.0.0.0",
		Port:              0,
		ManagementEnabled: true,
		ChunkedUploads:    true,
		UploadChunkSize:   4 * 1024 * 1024,
	})
	if err != nil || operation == nil {
		t.Fatalf("Start() operation = %#v, error = %v", operation, err)
	}
	startup := operation.Startup
	if startup.Port == 0 || startup.Directory == "" || !startup.ManagementEnabled || !startup.ChunkedUploads || startup.UploadChunkSize != 4*1024*1024 {
		t.Fatalf("startup = %#v", startup)
	}
	if len(startup.URLs) != 2 || startup.URLs[0] != (StartupURL{Label: "Local", URL: "http://localhost:" + strconv.Itoa(startup.Port)}) || startup.URLs[1] != (StartupURL{Label: "Network", URL: "http://192.0.2.44:" + strconv.Itoa(startup.Port)}) {
		t.Fatalf("startup URLs = %#v", startup.URLs)
	}
	cancel()
	if err := operation.Wait(ctx); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestModuleDoesNothingForAnAlreadyCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	module, err := New(Dependencies{
		NetworkInterfaces: func() ([]NetworkInterface, error) {
			t.Fatal("NetworkInterfaces was called")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	operation, err := module.Start(ctx, Input{})
	if err != nil || operation != nil {
		t.Fatalf("Start() operation = %#v, error = %v", operation, err)
	}
}

func TestOperationCloseStopsItsRuntime(t *testing.T) {
	module, err := New(Dependencies{NetworkInterfaces: func() ([]NetworkInterface, error) { return nil, nil }})
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}
	operation, err := module.Start(context.Background(), Input{Directory: t.TempDir(), Address: "127.0.0.1", Port: 0})
	if err != nil || operation == nil {
		t.Fatalf("Start() operation = %#v, error = %v", operation, err)
	}
	if err := operation.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := operation.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() after Close() error = %v", err)
	}
}

func TestFSModuleRequiresItsNetworkAdapter(t *testing.T) {
	if module, err := New(Dependencies{}); err == nil || module != nil {
		t.Fatalf("New(empty) = (%v, %v), want nil module and error", module, err)
	}
}
