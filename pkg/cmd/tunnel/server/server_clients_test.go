package server

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestServerControlPlanePersistsTrustedClientLifecycle(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	ctx := context.Background()
	events := make([]ServerControlPlaneEvent, 0)
	stop := plane.Subscribe(func(event ServerControlPlaneEvent) { events = append(events, event) })
	t.Cleanup(stop)

	created, err := plane.CreateClient(ctx, "environment-admin", "  Office Mac  ")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if created.Remark != "Office Mac" || !strings.HasPrefix(created.Token, "ycy_") || len(created.Token) != len("ycy_")+43 || created.DesiredRevision != 0 || created.LastAppliedRevision != 0 || created.RevocationPending || created.RotatedAt != nil {
		t.Fatalf("created client = %#v", created)
	}
	found, err := plane.FindClientByToken(ctx, created.Token)
	if err != nil || found == nil || found.ID != created.ID {
		t.Fatalf("FindClientByToken() = (%#v, %v)", found, err)
	}
	updated, err := plane.UpdateClientRemark(ctx, created.ID, "Office\ngateway")
	if err != nil || updated.Remark != "Office\ngateway" || updated.Token != created.Token {
		t.Fatalf("UpdateClientRemark() = (%#v, %v)", updated, err)
	}
	rotated, err := plane.RotateClientToken(ctx, created.ID)
	if err != nil || rotated.Token == created.Token || !rotated.RevocationPending || rotated.RotatedAt == nil {
		t.Fatalf("RotateClientToken() = (%#v, %v)", rotated, err)
	}
	old, err := plane.FindClientByToken(ctx, created.Token)
	if err != nil || old != nil {
		t.Fatalf("old token lookup = (%#v, %v), want nil", old, err)
	}
	if err := plane.AcknowledgeReplacementToken(ctx, created.ID); err != nil {
		t.Fatalf("AcknowledgeReplacementToken() error = %v", err)
	}
	acknowledged, err := plane.GetClient(ctx, created.ID)
	if err != nil || acknowledged.RevocationPending {
		t.Fatalf("GetClient() after acknowledgement = (%#v, %v)", acknowledged, err)
	}
	if err := plane.DeleteClient(ctx, created.ID); err != nil {
		t.Fatalf("DeleteClient() error = %v", err)
	}
	assertServerDomainCode(t, func() error {
		_, err := plane.GetClient(ctx, created.ID)
		return err
	}(), "NOT_FOUND")
	if got := eventTypes(events); strings.Join(got, ",") != "client_created,client_updated,client_rotated,client_updated,client_deleted" {
		t.Fatalf("events = %#v", events)
	}
}

func TestServerControlPlaneRestartsWithGoCreatedClientState(t *testing.T) {
	baseDirectory := t.TempDir()
	first, err := OpenState(StateOptions{DataDirectory: baseDirectory})
	if err != nil {
		t.Fatalf("first OpenState() error = %v", err)
	}
	insertServerDomainAccount(t, first, "environment-admin")
	firstPlane := openServerControlPlane(t, first)
	created, err := firstPlane.CreateClient(context.Background(), "environment-admin", "restart evidence")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	second, err := OpenState(StateOptions{DataDirectory: baseDirectory})
	if err != nil {
		t.Fatalf("second OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })
	secondPlane := openServerControlPlane(t, second)
	resumed, err := secondPlane.GetClient(context.Background(), created.ID)
	if err != nil || resumed.Token != created.Token || resumed.Remark != "restart evidence" {
		t.Fatalf("restarted GetClient() = (%#v, %v)", resumed, err)
	}
}

func TestServerControlPlaneKeepsMissingClientAndOwnerScopesPrivate(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), "environment-admin", "owner scope")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	assertServerDomainCode(t, func() error {
		_, err := plane.GetClientForOwner(context.Background(), client.ID, "different-owner")
		return err
	}(), "NOT_FOUND")
	assertServerDomainCode(t, func() error {
		_, err := plane.UpdateClientRemark(context.Background(), "missing-client", "irrelevant")
		return err
	}(), "NOT_FOUND")
}

func openServerDomainState(t *testing.T) *State {
	t.Helper()
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	insertServerDomainAccount(t, state, "environment-admin")
	return state
}

func insertServerDomainAccount(t *testing.T, state *State, id string) {
	t.Helper()
	now := "2026-08-24T00:00:00.000Z"
	if _, err := state.database.Exec(`INSERT INTO accounts(internal_id, kind, username, username_key, role, password_hash, created_at, updated_at) VALUES(?, 'environment', 'admin', 'admin', 'admin', NULL, ?, ?)`, id, now, now); err != nil {
		t.Fatalf("insert account: %v", err)
	}
}

func openServerControlPlane(t *testing.T, state *State) *ServerControlPlane {
	t.Helper()
	plane, err := NewServerControlPlane(ServerControlPlaneOptions{
		Database:  state.database,
		Now:       func() time.Time { return time.Date(2026, time.August, 24, 0, 0, 0, 0, time.UTC) },
		PortRange: ServerPortRange{Start: 20000, End: 20002},
	})
	if err != nil {
		t.Fatalf("NewServerControlPlane() error = %v", err)
	}
	return plane
}

func eventTypes(events []ServerControlPlaneEvent) []string {
	types := make([]string, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}

func TestServerControlPlaneRejectsInvalidDependencies(t *testing.T) {
	if _, err := NewServerControlPlane(ServerControlPlaneOptions{}); err == nil {
		t.Fatal("NewServerControlPlane() error = nil, want database failure")
	}
}
