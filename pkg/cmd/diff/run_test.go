package diff

import (
	"context"
	"net/netip"
	"path/filepath"
	"strconv"
	"testing"
)

func TestModuleStartsBoundComparisonThenTreatsContextCancellationAsSuccess(t *testing.T) {
	baseline, target := comparisonRoots(t)
	resolvedBaseline, err := filepath.EvalSymlinks(baseline)
	if err != nil {
		t.Fatalf("resolve baseline: %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	module, err := New(Dependencies{
		NetworkInterfaces: func() ([]NetworkInterface, error) {
			return []NetworkInterface{{Addresses: []netip.Addr{netip.MustParseAddr("192.168.1.50")}}}, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	operation, err := module.Start(ctx, Input{BaselineDirectory: baseline, TargetDirectory: target, Port: 0, Public: true})
	if err != nil || operation == nil {
		t.Fatalf("Start() operation = %#v, error = %v", operation, err)
	}
	start := operation.Startup
	if start.LocalURL == "" || start.Port == 0 || start.BaselineDirectory != resolvedBaseline || start.TargetDirectory != resolvedTarget || len(start.NetworkURLs) != 1 || start.NetworkURLs[0] != "http://192.168.1.50:"+strconv.Itoa(start.Port) {
		t.Fatalf("startup = %#v", start)
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
		t.Fatalf("New() error = %v", err)
	}
	operation, err := module.Start(ctx, Input{})
	if err != nil || operation != nil {
		t.Fatalf("Start() operation = %#v, error = %v", operation, err)
	}
}

func TestDiffModuleRequiresItsNetworkAdapter(t *testing.T) {
	if _, err := New(Dependencies{}); err == nil || err.Error() != "diff network interface provider is required" {
		t.Fatalf("New() missing network adapter error = %v", err)
	}
}
