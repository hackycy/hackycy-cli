package server

import (
	"context"
	"strings"
	"testing"
)

func TestServerAccountsManageEnvironmentAndLocalRecords(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	ctx := context.Background()
	environment, err := accounts.GetAccount(ctx, environmentAdministratorID)
	if err != nil || environment.Kind != AccountKindEnvironment || environment.Role != AccountRoleAdmin || environment.Username != "admin" {
		t.Fatalf("environment account = (%#v, %v)", environment, err)
	}
	alice, err := accounts.CreateLocalAccount(ctx, "alice", "alice-secret", AccountRoleUser)
	if err != nil || alice.Kind != AccountKindLocal || alice.Role != AccountRoleUser {
		t.Fatalf("CreateLocalAccount() = (%#v, %v)", alice, err)
	}
	if _, err := accounts.CreateLocalAccount(ctx, "Alice", "another-secret", AccountRoleUser); err == nil {
		t.Fatal("CreateLocalAccount(case collision) error = nil")
	} else {
		assertServerDomainCode(t, err, "USERNAME_TAKEN")
	}
	changed, roleChanged, err := accounts.ChangeLocalAccountRole(ctx, alice.ID, AccountRoleAdmin)
	if err != nil || !roleChanged || changed.Role != AccountRoleAdmin {
		t.Fatalf("ChangeLocalAccountRole() = (%#v, %t, %v)", changed, roleChanged, err)
	}
	reset, err := accounts.ResetLocalAccountPassword(ctx, alice.ID, "replacement-secret")
	if err != nil || reset.ID != alice.ID {
		t.Fatalf("ResetLocalAccountPassword() = (%#v, %v)", reset, err)
	}
	if err := accounts.DeleteLocalAccount(ctx, alice.ID); err != nil {
		t.Fatalf("DeleteLocalAccount() error = %v", err)
	}
	views, err := accounts.ListAccounts(ctx)
	if err != nil || len(views) != 1 || !views[0].ManagedByEnvironment || views[0].ClientCount != 0 {
		t.Fatalf("ListAccounts() = (%#v, %v)", views, err)
	}
}

func TestServerAccountsKeepEnvironmentIdentityAndRejectOwnedDeletion(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	first := openServerAccounts(t, state, "admin", "environment-secret")
	environment, err := first.GetAccount(context.Background(), environmentAdministratorID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	plane := openServerControlPlane(t, state)
	if _, err := plane.CreateClient(context.Background(), environment.ID, "stable owner"); err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	second := openServerAccounts(t, state, "root-admin", "replacement-environment-secret")
	renamed, err := second.GetAccount(context.Background(), environmentAdministratorID)
	if err != nil || renamed.ID != environment.ID || renamed.Username != "root-admin" {
		t.Fatalf("renamed environment = (%#v, %v)", renamed, err)
	}
	local, err := second.CreateLocalAccount(context.Background(), "operator", "operator-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount() error = %v", err)
	}
	if _, err := plane.CreateClient(context.Background(), local.ID, "owned"); err != nil {
		t.Fatalf("CreateClient(local) error = %v", err)
	}
	assertServerDomainCode(t, second.DeleteLocalAccount(context.Background(), local.ID), "ACCOUNT_NOT_EMPTY")
	assertServerDomainCode(t, func() error {
		_, _, err := second.ChangeLocalAccountRole(context.Background(), environmentAdministratorID, AccountRoleUser)
		return err
	}(), "MANAGED_ACCOUNT")
}

func TestServerAccountsUseCompatibleArgon2IDPHC(t *testing.T) {
	encoded, err := hashAccountPassword("correct-horse", strings.NewReader(strings.Repeat("a", argon2SaltLength)))
	if err != nil || !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=1$") || !verifyAccountPassword("correct-horse", encoded) || verifyAccountPassword("wrong", encoded) {
		t.Fatalf("Argon2id PHC = (%q, %v)", encoded, err)
	}
	if verifyAccountPassword("correct-horse", "$argon2id$v=19$m=65536,t=3,p=1$invalid$invalid") {
		t.Fatal("verifyAccountPassword() accepted malformed PHC")
	}
}

func TestServerAccountsRejectInvalidInputsAndEnvironmentCollision(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	for _, input := range []string{" alice ", "", strings.Repeat("a", 65)} {
		input := input
		assertServerDomainCode(t, func() error {
			_, err := accounts.CreateLocalAccount(context.Background(), input, "valid-secret", AccountRoleUser)
			return err
		}(), "INVALID_ACCOUNT")
	}
	assertServerDomainCode(t, func() error {
		_, err := accounts.CreateLocalAccount(context.Background(), "short-password", "tiny", AccountRoleUser)
		return err
	}(), "INVALID_ACCOUNT")
	if _, err := accounts.CreateLocalAccount(context.Background(), "collision", "collision-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(collision) error = %v", err)
	}
	if _, err := NewServerAccounts(context.Background(), ServerAccountsOptions{Database: state.database, AdminUsername: "collision", AdminPassword: "another-environment-secret"}); err == nil {
		t.Fatal("NewServerAccounts(collision) error = nil")
	} else {
		assertServerDomainCode(t, err, "INVALID_CONFIG")
	}
}

func openServerAccounts(t *testing.T, state *State, username, password string) *ServerAccounts {
	t.Helper()
	accounts, err := NewServerAccounts(context.Background(), ServerAccountsOptions{Database: state.database, AdminUsername: username, AdminPassword: password})
	if err != nil {
		t.Fatalf("NewServerAccounts() error = %v", err)
	}
	return accounts
}
