package server

import (
	"context"
	"testing"
)

func TestServerSessionsAuthenticateAndRefreshPersistentGrants(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	grant, err := sessions.SignIn(context.Background(), "ADMIN", "environment-secret")
	if err != nil || grant.Account.ID != environmentAdministratorID || grant.Token == "" || grant.ExpiresAt == "" {
		t.Fatalf("SignIn(environment) = (%#v, %v)", grant, err)
	}
	resumed, err := sessions.Resume(context.Background(), grant.Token)
	if err != nil || resumed == nil || resumed.Account.Username != "admin" || resumed.Token != grant.Token {
		t.Fatalf("Resume() = (%#v, %v)", resumed, err)
	}
	if _, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount() error = %v", err)
	}
	alice, err := sessions.SignIn(context.Background(), "ALICE", "alice-secret")
	if err != nil || alice.Account.Username != "alice" {
		t.Fatalf("SignIn(local) = (%#v, %v)", alice, err)
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "wrong-secret"); err == nil {
		t.Fatal("SignIn(wrong password) error = nil")
	} else {
		assertServerDomainCode(t, err, "AUTHENTICATION_FAILED")
	}
	if err := sessions.SignOut(alice.Token); err != nil {
		t.Fatalf("SignOut() error = %v", err)
	}
	if resumed, err := sessions.Resume(context.Background(), alice.Token); err != nil || resumed != nil {
		t.Fatalf("Resume(signed out) = (%#v, %v), want nil", resumed, err)
	}
}

func TestServerSessionsRevokeCredentialsAfterAccountMutation(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	alice, err := accounts.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount() error = %v", err)
	}
	sessions := openServerSessions(t, accounts, state)
	grant, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	if _, err := sessions.ChangeLocalAccountRole(context.Background(), alice.ID, AccountRoleAdmin); err != nil {
		t.Fatalf("ChangeLocalAccountRole() error = %v", err)
	}
	if resumed, err := sessions.Resume(context.Background(), grant.Token); err != nil || resumed != nil {
		t.Fatalf("Resume(role changed) = (%#v, %v), want nil", resumed, err)
	}
	promoted, err := sessions.SignIn(context.Background(), "alice", "alice-secret")
	if err != nil || promoted.Account.Role != AccountRoleAdmin {
		t.Fatalf("SignIn(promoted) = (%#v, %v)", promoted, err)
	}
	if _, err := sessions.ResetLocalAccountPassword(context.Background(), alice.ID, "replacement-secret"); err != nil {
		t.Fatalf("ResetLocalAccountPassword() error = %v", err)
	}
	if resumed, err := sessions.Resume(context.Background(), promoted.Token); err != nil || resumed != nil {
		t.Fatalf("Resume(password reset) = (%#v, %v), want nil", resumed, err)
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "alice-secret"); err == nil {
		t.Fatal("SignIn(old password) error = nil")
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "replacement-secret"); err != nil {
		t.Fatalf("SignIn(replacement password) error = %v", err)
	}
}

func TestServerSessionsPersistOnlyUnchangedEnvironmentCredentials(t *testing.T) {
	baseDirectory := t.TempDir()
	first, err := OpenState(StateOptions{DataDirectory: baseDirectory})
	if err != nil {
		t.Fatalf("first OpenState() error = %v", err)
	}
	firstAccounts := openServerAccounts(t, first, "admin", "environment-secret")
	firstSessions := openServerSessions(t, firstAccounts, first)
	grant, err := firstSessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}

	restored, err := OpenState(StateOptions{DataDirectory: baseDirectory})
	if err != nil {
		t.Fatalf("restored OpenState() error = %v", err)
	}
	restoredAccounts := openServerAccounts(t, restored, "admin", "environment-secret")
	restoredSessions := openServerSessions(t, restoredAccounts, restored)
	if resumed, err := restoredSessions.Resume(context.Background(), grant.Token); err != nil || resumed == nil {
		t.Fatalf("Resume(unchanged environment) = (%#v, %v)", resumed, err)
	}
	if err := restored.Close(); err != nil {
		t.Fatalf("restored Close() error = %v", err)
	}

	changed, err := OpenState(StateOptions{DataDirectory: baseDirectory})
	if err != nil {
		t.Fatalf("changed OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = changed.Close() })
	changedAccounts := openServerAccounts(t, changed, "admin", "replacement-environment-secret")
	changedSessions := openServerSessions(t, changedAccounts, changed)
	if resumed, err := changedSessions.Resume(context.Background(), grant.Token); err != nil || resumed != nil {
		t.Fatalf("Resume(changed environment) = (%#v, %v), want nil", resumed, err)
	}
}

func openServerSessions(t *testing.T, accounts *ServerAccounts, state *State) *ServerSessions {
	t.Helper()
	sessions, err := NewServerSessions(accounts, state.sessions)
	if err != nil {
		t.Fatalf("NewServerSessions() error = %v", err)
	}
	return sessions
}
