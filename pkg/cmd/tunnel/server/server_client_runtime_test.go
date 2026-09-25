package server

import (
	"context"
	"testing"
)

func TestSyncLocalClientRuntimeRevisionAdvancesOnlyWhenEndpointOrTokenChanges(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	if err := syncLocalClientRuntimeRevision(ctx, state.database, "frp.example.test", 7000, "first-token"); err != nil {
		t.Fatal(err)
	}
	client, err := plane.CreateClient(ctx, "environment-admin", "local runtime")
	if err != nil {
		t.Fatal(err)
	}
	check := func(host string, port int64, token string, want int64) {
		t.Helper()
		if err := syncLocalClientRuntimeRevision(ctx, state.database, host, port, token); err != nil {
			t.Fatal(err)
		}
		runtime, err := plane.BuildClientRuntime(ctx, client.ID, host, port, token)
		if err != nil {
			t.Fatal(err)
		}
		if runtime.Revision != want || runtime.NodeID != "local" || runtime.Digest == "" {
			t.Fatalf("runtime after settings change = %#v, want revision %d", runtime, want)
		}
	}
	check("frp.example.test", 7000, "first-token", 0)
	check("other.example.test", 7000, "first-token", 1)
	check("other.example.test", 7000, "first-token", 1)
	check("other.example.test", 7001, "first-token", 2)
	check("other.example.test", 7001, "second-token", 3)
}
