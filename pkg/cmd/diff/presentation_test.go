package diff

import (
	"net/netip"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
)

func TestDiffStartupURLsPreserveLocalAndPublicIPv4Rules(t *testing.T) {
	interfaces := []NetworkInterface{
		{Internal: true, Addresses: []netip.Addr{netip.MustParseAddr("127.0.0.1")}},
		{Addresses: []netip.Addr{netip.MustParseAddr("192.168.1.50"), netip.MustParseAddr("fe80::1")}},
		{Addresses: []netip.Addr{netip.MustParseAddr("10.0.0.8")}},
	}

	local := makeDiffStartupURLs(false, 43123, interfaces)
	if local.local != "http://127.0.0.1:43123" || len(local.network) != 0 {
		t.Fatalf("local URLs = %#v", local)
	}

	public := makeDiffStartupURLs(true, 43123, interfaces)
	if public.local != "http://localhost:43123" {
		t.Fatalf("public local URL = %q", public.local)
	}
	if want := []string{"http://192.168.1.50:43123", "http://10.0.0.8:43123"}; !reflect.DeepEqual(public.network, want) {
		t.Fatalf("public network URLs = %#v, want %#v", public.network, want)
	}
}

func TestComparisonSessionBuildsResolvedStartupPresentation(t *testing.T) {
	baseline, target := comparisonRoots(t)
	resolvedBaseline, err := filepath.EvalSymlinks(baseline)
	if err != nil {
		t.Fatalf("resolve baseline: %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	session, err := startComparison(Input{BaselineDirectory: baseline, TargetDirectory: target, Port: 0})
	if err != nil {
		t.Fatalf("startComparison() error = %v", err)
	}
	defer func() {
		if err := session.server.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	presentation := session.startupPresentation(nil)
	if presentation.LocalURL != "http://127.0.0.1:"+strconv.Itoa(session.server.Port()) || presentation.BaselineDirectory != resolvedBaseline || presentation.TargetDirectory != resolvedTarget || presentation.Port != session.server.Port() || len(presentation.NetworkURLs) != 0 {
		t.Fatalf("presentation = %#v", presentation)
	}
}
