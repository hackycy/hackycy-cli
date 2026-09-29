package server

import (
	"context"
	"encoding/json"
	"testing"
)

type nodePolicyTest404Reader struct{ content string }

func (reader *nodePolicyTest404Reader) ReadCustom404Page() (string, error) {
	return reader.content, nil
}

func TestNodeConfigurationPolicyResolvesSources(t *testing.T) {
	tests := []struct {
		name     string
		settings serverNodeSettings
		global   string
		wantMode string
		wantPage string
	}{
		{name: "inherit", settings: serverNodeSettings{}, global: "<main>server</main>", wantMode: "inherit", wantPage: "<main>server</main>"},
		{name: "custom", settings: serverNodeSettings{Custom404PageMode: "custom", Custom404Page: "<main>node</main>"}, global: "<main>server</main>", wantMode: "custom", wantPage: "<main>node</main>"},
		{name: "default", settings: serverNodeSettings{Custom404PageMode: "default"}, global: "<main>server</main>", wantMode: "default", wantPage: ""},
		{name: "legacy content", settings: serverNodeSettings{Custom404Page: "<main>node</main>"}, global: "<main>server</main>", wantMode: "custom", wantPage: "<main>node</main>"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, err := policyForNodeSettings(test.settings)
			if err != nil {
				t.Fatal(err)
			}
			page, err := resolveNodeField(policy.Fields["custom404Page"], test.global)
			if err != nil {
				t.Fatal(err)
			}
			if policy.Fields["custom404Page"].Mode != test.wantMode || page != test.wantPage {
				t.Fatalf("policy=%+v page=%q, want mode=%q page=%q", policy.Fields["custom404Page"], page, test.wantMode, test.wantPage)
			}
		})
	}
}

func TestNodeConfigurationPolicyRejectsValueForNonCustomMode(t *testing.T) {
	for _, mode := range []string{"inherit", "default", "invalid"} {
		if _, err := policyForNodeSettings(serverNodeSettings{Custom404PageMode: mode, Custom404Page: "content"}); err == nil {
			t.Fatalf("mode %q accepted an override", mode)
		}
	}
}

func TestNodeConfigurationPolicyRoundTripsFutureFields(t *testing.T) {
	policy := defaultNodeConfigurationPolicy()
	policy.Fields["futureSetting"] = nodeFieldPolicy{Mode: "custom", Value: "future"}
	contents, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := parseNodeConfigurationPolicy(string(contents))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Fields["futureSetting"].Value != "future" {
		t.Fatalf("future policy field was lost: %+v", decoded.Fields)
	}
}

func TestRefreshInheritedNodeOnlyChangesInheritedSources(t *testing.T) {
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	reader := &nodePolicyTest404Reader{content: "<main>one</main>"}
	registry.default404Page = reader
	for index, item := range []struct {
		id   string
		mode string
		page string
	}{
		{id: "0123456789abcdef0123456789abcdef", mode: "inherit"},
		{id: "fedcba9876543210fedcba9876543210", mode: "custom", page: "<main>custom</main>"},
		{id: "abcdef0123456789abcdef0123456789", mode: "default"},
	} {
		publicKey := make([]byte, 32)
		publicKey[0] = byte(index)
		if _, err := registry.register(context.Background(), item.id, item.id, "http://127.0.0.1:"+string(rune('1'+index))+"760", publicKey, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.saveDesired(context.Background(), item.id, 0, serverNodeSettings{BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 8080, PortRangeStart: 20000, PortRangeEnd: 20100, Custom404Page: item.page, Custom404PageMode: item.mode}); err != nil {
			t.Fatal(err)
		}
	}
	reader.content = "<main>two</main>"
	changed, err := registry.refreshInherited(context.Background(), "0123456789abcdef0123456789abcdef")
	if err != nil || !changed {
		t.Fatalf("refresh inherited = (%t, %v)", changed, err)
	}
	for _, id := range []string{"0123456789abcdef0123456789abcdef", "fedcba9876543210fedcba9876543210", "abcdef0123456789abcdef0123456789"} {
		changed, err := registry.refreshInherited(context.Background(), id)
		if err != nil || changed {
			t.Fatalf("repeat or overridden refresh for %s = (%t, %v)", id, changed, err)
		}
	}
	for _, item := range []struct {
		id   string
		page string
		rev  int64
	}{
		{id: "0123456789abcdef0123456789abcdef", page: "<main>two</main>", rev: 2},
		{id: "fedcba9876543210fedcba9876543210", page: "<main>custom</main>", rev: 1},
		{id: "abcdef0123456789abcdef0123456789", page: "", rev: 1},
	} {
		record, err := registry.get(context.Background(), item.id)
		if err != nil {
			t.Fatal(err)
		}
		var snapshot serverDesiredNodeSnapshot
		if err := json.Unmarshal([]byte(record.DesiredSnapshot.String), &snapshot); err != nil {
			t.Fatal(err)
		}
		if record.DesiredRevision != item.rev || snapshot.Custom404Page != item.page {
			t.Fatalf("node %s refreshed to revision %d page %q, want revision %d page %q", item.id, record.DesiredRevision, snapshot.Custom404Page, item.rev, item.page)
		}
	}
}
