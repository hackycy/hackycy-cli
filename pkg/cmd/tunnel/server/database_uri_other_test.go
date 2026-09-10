//go:build !windows

package server

import (
	"strings"
	"testing"
)

func TestDatabaseFileURIRetainsUnixFileURI(t *testing.T) {
	got := databaseFileURI("/tmp/Tunnel Data/数据/tunnel.sqlite")
	if !strings.HasPrefix(got, "file:///tmp/") {
		t.Fatalf("databaseFileURI() = %q, want file:///tmp/ prefix", got)
	}
	if !strings.Contains(got, "%20") || !strings.Contains(got, "%E6%95%B0%") {
		t.Fatalf("databaseFileURI() = %q, want URI escaping", got)
	}
}
