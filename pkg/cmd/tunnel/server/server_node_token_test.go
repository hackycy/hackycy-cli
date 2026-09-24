package server

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestServerNodeTokenStageKeepsActiveClientCredentialUntilConfirmation(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	id := "a123456789abcdef0123456789abcdef"
	if _, err := registry.register(ctx, id, "Remote", "http://127.0.0.1:7600", make([]byte, 32), 0); err != nil {
		t.Fatal(err)
	}
	settings := serverNodeSettings{BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 8080, PortRangeStart: 20000, PortRangeEnd: 20001}
	if _, err := registry.saveDesired(ctx, id, 0, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := state.database.ExecContext(ctx, `UPDATE nodes SET advertised_frp_host='remote.example.test',advertised_frp_port=7000 WHERE node_id=?`, id); err != nil {
		t.Fatal(err)
	}
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(ctx, environmentAdministratorID, "remote")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plane.AssignClientNode(ctx, client.ID, id, true, true); err != nil {
		t.Fatal(err)
	}
	before, err := plane.BuildClientRuntime(ctx, client.ID, "local.example.test", 7000, "local-token")
	if err != nil {
		t.Fatal(err)
	}
	revision, err := registry.stageTokenRotation(ctx, id, 1)
	if err != nil || revision != 2 {
		t.Fatalf("stage rotation = (%d, %v)", revision, err)
	}
	var active, staged, saved string
	var stagedRevision int64
	if err := state.database.QueryRow(`SELECT active_token,staged_token,staged_token_revision,desired_snapshot FROM remote_nodes WHERE node_id=?`, id).Scan(&active, &staged, &stagedRevision, &saved); err != nil {
		t.Fatal(err)
	}
	var snapshot serverDesiredNodeSnapshot
	if err := json.Unmarshal([]byte(saved), &snapshot); err != nil {
		t.Fatal(err)
	}
	if active != before.FRPToken || staged == active || snapshot.Token != staged || stagedRevision != 2 || snapshot.Revision != 2 {
		t.Fatal("staged Node snapshot or active credential is inconsistent")
	}
	after, err := plane.BuildClientRuntime(ctx, client.ID, "local.example.test", 7000, "local-token")
	if err != nil || after.FRPToken != before.FRPToken || after.Revision != before.Revision {
		t.Fatalf("Client credential changed before Node confirmation = (%+v, %v)", after.Reference(), err)
	}
	if _, err := registry.stageTokenRotation(ctx, id, 2); err == nil {
		t.Fatal("second rotation overwrote staged Token")
	}
}

func TestServerNodeManagementViewProjectsTokenStageWithoutSecrets(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := newServerNodeObservations(state.database)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := newServerNodeCoordinator(state.sessions.Directory(), registry, observations)
	if err != nil {
		t.Fatal(err)
	}
	service := newServerNodeService(registry, observations, coordinator)
	id := "f123456789abcdef0123456789abcdef"
	if _, err := registry.register(ctx, id, "Remote", "http://127.0.0.1:7600", make([]byte, 32), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.saveDesired(ctx, id, 0, serverNodeSettings{BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 8080, PortRangeStart: 20000, PortRangeEnd: 20001}); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.stageTokenRotation(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	record, err := registry.get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	view, err := service.managementView(ctx, id)
	if err != nil || view.TokenRotation.State != "pending_node" || view.TokenRotation.Revision != 2 {
		t.Fatalf("pending rotation view = (%+v, %v)", view.TokenRotation, err)
	}
	status := nodeStatus{Claimed: true, HighestAcceptedRevision: 2, AppliedRevision: 1, FailedRevision: 2, SHA256: record.DesiredHash.String, Phase: "failed", FailureCode: "NODE_APPLY_FAILED", FRPSProcess: "running", ObservedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if err := observations.recordStatus(ctx, id, status); err != nil {
		t.Fatal(err)
	}
	view, err = service.managementView(ctx, id)
	if err != nil || view.TokenRotation.State != "apply_failed" {
		t.Fatalf("failed rotation view = (%+v, %v)", view.TokenRotation, err)
	}
	status.AppliedRevision, status.FailedRevision, status.Phase, status.FailureCode = 2, 0, "applied", ""
	if err := observations.recordStatus(ctx, id, status); err != nil {
		t.Fatal(err)
	}
	view, err = service.managementView(ctx, id)
	if err != nil || view.TokenRotation.State != "publishing_clients" {
		t.Fatalf("publishing rotation view = (%+v, %v)", view.TokenRotation, err)
	}
	var active, staged string
	if err := state.database.QueryRow(`SELECT active_token,staged_token FROM remote_nodes WHERE node_id=?`, id).Scan(&active, &staged); err != nil {
		t.Fatal(err)
	}
	contents, err := json.Marshal(view)
	if err != nil || bytes.Contains(contents, []byte(active)) || bytes.Contains(contents, []byte(staged)) {
		t.Fatal("management projection included a Node Token")
	}
}

func TestServerNodeTokenStageSurvivesHigherRevisionSettingsRepair(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	id := "b123456789abcdef0123456789abcdef"
	if _, err := registry.register(ctx, id, "Remote", "http://127.0.0.1:7600", make([]byte, 32), 0); err != nil {
		t.Fatal(err)
	}
	settings := serverNodeSettings{BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 8080, PortRangeStart: 20000, PortRangeEnd: 20001}
	if _, err := registry.saveDesired(ctx, id, 0, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.stageTokenRotation(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	settings.BindPort = 7001
	if revision, err := registry.saveDesired(ctx, id, 2, settings); err != nil || revision != 3 {
		t.Fatalf("repair staged snapshot = (%d, %v)", revision, err)
	}
	var active, staged, saved string
	var stagedRevision int64
	if err := state.database.QueryRow(`SELECT active_token,staged_token,staged_token_revision,desired_snapshot FROM remote_nodes WHERE node_id=?`, id).Scan(&active, &staged, &stagedRevision, &saved); err != nil {
		t.Fatal(err)
	}
	var snapshot serverDesiredNodeSnapshot
	if err := json.Unmarshal([]byte(saved), &snapshot); err != nil {
		t.Fatal(err)
	}
	if active == staged || snapshot.Token != staged || snapshot.BindPort != 7001 || snapshot.Revision != 3 || stagedRevision != 3 {
		t.Fatal("higher-revision repair lost staged Token or changed active Token")
	}
}

func TestServerNodeTokenPromotionRequiresExactAppliedStatusAndUpdatesCurrentClients(t *testing.T) {
	ctx := context.Background()
	state := openServerDomainState(t)
	registry, err := newServerNodeRegistry(state.database)
	if err != nil {
		t.Fatal(err)
	}
	settings := serverNodeSettings{BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 8080, PortRangeStart: 20000, PortRangeEnd: 20001}
	id := "c123456789abcdef0123456789abcdef"
	otherID := "d123456789abcdef0123456789abcdef"
	for index, nodeID := range []string{id, otherID} {
		publicKey := make([]byte, 32)
		publicKey[0] = byte(index + 1)
		if _, err := registry.register(ctx, nodeID, "Remote", "http://127.0.0.1:"+map[string]string{id: "7600", otherID: "7601"}[nodeID], publicKey, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.saveDesired(ctx, nodeID, 0, settings); err != nil {
			t.Fatal(err)
		}
		if _, err := state.database.ExecContext(ctx, `UPDATE nodes SET advertised_frp_host=?,advertised_frp_port=7000 WHERE node_id=?`, nodeID+".example.test", nodeID); err != nil {
			t.Fatal(err)
		}
	}
	plane := openServerControlPlane(t, state)
	var assigned []TrustedTunnelClient
	for _, nodeID := range []string{id, id, otherID} {
		client, err := plane.CreateClient(ctx, environmentAdministratorID, nodeID)
		if err != nil {
			t.Fatal(err)
		}
		client, err = plane.AssignClientNode(ctx, client.ID, nodeID, true, true)
		if err != nil {
			t.Fatal(err)
		}
		assigned = append(assigned, client)
	}
	if _, err := registry.stageTokenRotation(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	before, err := plane.BuildClientRuntime(ctx, assigned[0].ID, "local.example.test", 7000, "local-token")
	if err != nil {
		t.Fatal(err)
	}
	otherBefore, err := plane.BuildClientRuntime(ctx, assigned[2].ID, "local.example.test", 7000, "local-token")
	if err != nil {
		t.Fatal(err)
	}
	record, err := registry.get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	status := nodeStatus{Claimed: true, Phase: "applied", HighestAcceptedRevision: 2, AppliedRevision: 2, SHA256: record.DesiredHash.String}
	for _, invalid := range []nodeStatus{
		{Claimed: true, Phase: "failed", HighestAcceptedRevision: 2, AppliedRevision: 1, SHA256: status.SHA256},
		{Claimed: true, Phase: "applied", HighestAcceptedRevision: 2, AppliedRevision: 2, SHA256: "wrong"},
		{Claimed: true, Phase: "applied", HighestAcceptedRevision: 1, AppliedRevision: 1, SHA256: status.SHA256},
	} {
		promotion, err := registry.promoteTokenRotation(ctx, id, invalid)
		if err != nil || promotion.Promoted {
			t.Fatalf("invalid Node status promoted Token: (%+v, %v)", promotion, err)
		}
	}
	promotion, err := registry.promoteTokenRotation(ctx, id, status)
	if err != nil || !promotion.Promoted || len(promotion.Events) != 2 {
		t.Fatalf("confirmed promotion = (%+v, %v)", promotion, err)
	}
	for _, client := range assigned[:2] {
		updated, err := plane.BuildClientRuntime(ctx, client.ID, "local.example.test", 7000, "local-token")
		if err != nil || updated.FRPToken == before.FRPToken || updated.Revision != before.Revision+1 {
			t.Fatalf("assigned Client did not get promoted Token = (%+v, %v)", updated.Reference(), err)
		}
	}
	otherAfter, err := plane.BuildClientRuntime(ctx, assigned[2].ID, "local.example.test", 7000, "local-token")
	if err != nil || otherAfter.FRPToken != otherBefore.FRPToken || otherAfter.Revision != otherBefore.Revision {
		t.Fatalf("other Node Client changed = (%+v, %v)", otherAfter.Reference(), err)
	}
	var staged *string
	if err := state.database.QueryRow(`SELECT staged_token FROM remote_nodes WHERE node_id=?`, id).Scan(&staged); err != nil || staged != nil {
		t.Fatalf("staged Token was not cleared = (%v, %v)", staged, err)
	}
	again, err := registry.promoteTokenRotation(ctx, id, status)
	if err != nil || again.Promoted {
		t.Fatalf("duplicate promotion = (%+v, %v)", again, err)
	}
}
