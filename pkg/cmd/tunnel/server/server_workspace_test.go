package server

import (
	"context"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"testing"
)

func TestServerWorkspaceScopesClientAndTunnelOperationsByOwner(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	admin := openWorkspaceAfterSignIn(t, sessions, accounts, plane, "admin", "environment-secret")
	if _, err := admin.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	if _, err := admin.CreateLocalAccount(context.Background(), "bob", "bob-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(bob) error = %v", err)
	}
	alice := openWorkspaceAfterSignIn(t, sessions, accounts, plane, "alice", "alice-secret")
	bob := openWorkspaceAfterSignIn(t, sessions, accounts, plane, "bob", "bob-secret")
	client, err := alice.CreateClient(context.Background(), "Alice gateway")
	if err != nil || client.OwnerAccountID == environmentAdministratorID {
		t.Fatalf("alice CreateClient() = (%#v, %v)", client, err)
	}
	if clients, err := bob.ListClients(context.Background()); err != nil || len(clients) != 0 {
		t.Fatalf("bob ListClients() = (%#v, %v)", clients, err)
	}
	for _, operation := range []func() error{
		func() error { _, err := bob.UpdateClientRemark(context.Background(), client.ID, "stolen"); return err },
		func() error { _, err := bob.RotateClientToken(context.Background(), client.ID); return err },
		func() error { return bob.DeleteClient(context.Background(), client.ID) },
		func() error {
			_, err := bob.CreateTunnel(context.Background(), client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"stolen.example.com"}, LocalPort: 3000})
			return err
		},
	} {
		assertServerDomainCode(t, operation(), "NOT_FOUND")
	}
	tunnel, err := alice.CreateTunnel(context.Background(), client.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"alice.example.com"}, LocalPort: 3000})
	if err != nil {
		t.Fatalf("alice CreateTunnel() error = %v", err)
	}
	assertServerDomainCode(t, func() error {
		_, err := bob.UpdateTunnel(context.Background(), tunnel.ID, TunnelPatchInput{Enabled: boolPointer(false)})
		return err
	}(), "NOT_FOUND")
	assertServerDomainCode(t, func() error {
		_, err := bob.ImportFRPCTunnels(context.Background(), client.ID, serverImportSource, []string{"proxy-1"})
		return err
	}(), "NOT_FOUND")
	if _, err := alice.UpdateTunnel(context.Background(), tunnel.ID, TunnelPatchInput{Enabled: boolPointer(false)}); err != nil {
		t.Fatalf("alice UpdateTunnel() error = %v", err)
	}
	assertServerDomainCode(t, bob.DeleteTunnel(context.Background(), tunnel.ID), "NOT_FOUND")
	if clients, err := admin.ListClients(context.Background()); err != nil || len(clients) != 1 || clients[0].ID != client.ID {
		t.Fatalf("admin ListClients() = (%#v, %v)", clients, err)
	}
	if err := admin.DeleteTunnel(context.Background(), tunnel.ID); err != nil {
		t.Fatalf("admin DeleteTunnel() error = %v", err)
	}
}

func TestServerWorkspaceRevokesChangedRolesAndSelfPasswords(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	admin := openWorkspaceAfterSignIn(t, sessions, accounts, plane, "admin", "environment-secret")
	aliceAccount, err := admin.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser)
	if err != nil {
		t.Fatalf("CreateLocalAccount() error = %v", err)
	}
	alice := openWorkspaceAfterSignIn(t, sessions, accounts, plane, "alice", "alice-secret")
	assertServerDomainCode(t, func() error {
		_, err := alice.ListAccounts(context.Background())
		return err
	}(), "FORBIDDEN")
	if _, err := admin.ChangeLocalAccountRole(context.Background(), aliceAccount.ID, AccountRoleAdmin); err != nil {
		t.Fatalf("ChangeLocalAccountRole() error = %v", err)
	}
	assertServerDomainCode(t, func() error {
		_, err := alice.Account(context.Background())
		return err
	}(), "AUTHENTICATION_REQUIRED")
	promoted := openWorkspaceAfterSignIn(t, sessions, accounts, plane, "alice", "alice-secret")
	assertServerDomainCode(t, func() error {
		_, err := promoted.ChangeOwnPassword(context.Background(), "wrong-secret", "replacement-secret")
		return err
	}(), "INVALID_CURRENT_PASSWORD")
	if _, err := promoted.ChangeOwnPassword(context.Background(), "alice-secret", "replacement-secret"); err != nil {
		t.Fatalf("ChangeOwnPassword() error = %v", err)
	}
	assertServerDomainCode(t, func() error {
		_, err := promoted.Account(context.Background())
		return err
	}(), "AUTHENTICATION_REQUIRED")
	if _, err := sessions.SignIn(context.Background(), "alice", "alice-secret"); err == nil {
		t.Fatal("SignIn(old password) error = nil")
	}
	if _, err := sessions.SignIn(context.Background(), "alice", "replacement-secret"); err != nil {
		t.Fatalf("SignIn(replacement password) error = %v", err)
	}
}

func TestServerWorkspaceLimitsFRPSControlToAdministrators(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	controller := &serverWorkspaceTestFRPSController{}
	openWorkspace := func(t *testing.T, username, password string) *ServerWorkspace {
		t.Helper()
		grant, err := sessions.SignIn(context.Background(), username, password)
		if err != nil {
			t.Fatalf("SignIn(%q) error = %v", username, err)
		}
		workspace, err := OpenServerWorkspace(context.Background(), ServerWorkspaceDependencies{
			Sessions: sessions, Accounts: accounts, ControlPlane: plane, FRPS: controller,
		}, grant.Token)
		if err != nil {
			t.Fatalf("OpenServerWorkspace(%q) error = %v", username, err)
		}
		return workspace
	}

	admin := openWorkspace(t, "admin", "environment-secret")
	if _, err := admin.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	alice := openWorkspace(t, "alice", "alice-secret")
	assertServerDomainCode(t, alice.ControlFRPS(context.Background(), ServerFRPSActionStart), "FORBIDDEN")
	if len(controller.calls) != 0 {
		t.Fatalf("non-administrator FRPS calls = %v", controller.calls)
	}
	for _, action := range []ServerFRPSAction{ServerFRPSActionStart, ServerFRPSActionStop, ServerFRPSActionRestart} {
		if err := admin.ControlFRPS(context.Background(), action); err != nil {
			t.Fatalf("ControlFRPS(%q) error = %v", action, err)
		}
	}
	if got, want := controller.calls, []ServerFRPSAction{ServerFRPSActionStart, ServerFRPSActionStop, ServerFRPSActionRestart}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("administrator FRPS calls = %v, want %v", got, want)
	}

	grant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	unconfigured, err := OpenServerWorkspace(context.Background(), ServerWorkspaceDependencies{Sessions: sessions, Accounts: accounts, ControlPlane: plane}, grant.Token)
	if err != nil {
		t.Fatalf("OpenServerWorkspace(unconfigured) error = %v", err)
	}
	assertServerDomainCode(t, unconfigured.ControlFRPS(context.Background(), ServerFRPSActionStart), "FRPS_UNAVAILABLE")
}

func TestServerWorkspaceLimitsCustom404PageReadingToAdministrators(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	reader := &serverWorkspaceTestCustom404PageReader{content: "<main>custom 404</main>"}
	openWorkspace := func(t *testing.T, username, password string) *ServerWorkspace {
		t.Helper()
		grant, err := sessions.SignIn(context.Background(), username, password)
		if err != nil {
			t.Fatalf("SignIn(%q) error = %v", username, err)
		}
		workspace, err := OpenServerWorkspace(context.Background(), ServerWorkspaceDependencies{
			Sessions: sessions, Accounts: accounts, ControlPlane: plane, Custom404PageReader: reader,
		}, grant.Token)
		if err != nil {
			t.Fatalf("OpenServerWorkspace(%q) error = %v", username, err)
		}
		return workspace
	}

	admin := openWorkspace(t, "admin", "environment-secret")
	if _, err := admin.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	alice := openWorkspace(t, "alice", "alice-secret")
	if _, err := alice.ReadCustom404Page(context.Background()); err == nil {
		t.Fatal("ReadCustom404Page() by ordinary user error = nil")
	} else {
		assertServerDomainCode(t, err, "FORBIDDEN")
	}
	if reader.reads != 0 {
		t.Fatalf("ordinary-user reads = %d", reader.reads)
	}
	content, err := admin.ReadCustom404Page(context.Background())
	if err != nil || content != "<main>custom 404</main>" || reader.reads != 1 {
		t.Fatalf("admin ReadCustom404Page() = (%q, %v), reads = %d", content, err, reader.reads)
	}

	grant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	unconfigured, err := OpenServerWorkspace(context.Background(), ServerWorkspaceDependencies{Sessions: sessions, Accounts: accounts, ControlPlane: plane}, grant.Token)
	if err != nil {
		t.Fatalf("OpenServerWorkspace(unconfigured) error = %v", err)
	}
	_, err = unconfigured.ReadCustom404Page(context.Background())
	assertServerDomainCode(t, err, "FRPS_UNAVAILABLE")
}

func TestServerWorkspaceLimitsCustom404PageWritingToAdministrators(t *testing.T) {
	state, err := OpenState(StateOptions{DataDirectory: t.TempDir()})
	if err != nil {
		t.Fatalf("OpenState() error = %v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	accounts := openServerAccounts(t, state, "admin", "environment-secret")
	sessions := openServerSessions(t, accounts, state)
	plane := openServerControlPlane(t, state)
	writer := &serverWorkspaceTestCustom404PageWriter{}
	openWorkspace := func(t *testing.T, username, password string) *ServerWorkspace {
		t.Helper()
		grant, err := sessions.SignIn(context.Background(), username, password)
		if err != nil {
			t.Fatalf("SignIn(%q) error = %v", username, err)
		}
		workspace, err := OpenServerWorkspace(context.Background(), ServerWorkspaceDependencies{
			Sessions: sessions, Accounts: accounts, ControlPlane: plane, Custom404PageWriter: writer,
		}, grant.Token)
		if err != nil {
			t.Fatalf("OpenServerWorkspace(%q) error = %v", username, err)
		}
		return workspace
	}

	admin := openWorkspace(t, "admin", "environment-secret")
	if _, err := admin.CreateLocalAccount(context.Background(), "alice", "alice-secret", AccountRoleUser); err != nil {
		t.Fatalf("CreateLocalAccount(alice) error = %v", err)
	}
	alice := openWorkspace(t, "alice", "alice-secret")
	assertServerDomainCode(t, alice.WriteCustom404Page(context.Background(), "<main>denied</main>"), "FORBIDDEN")
	if len(writer.contents) != 0 {
		t.Fatalf("ordinary-user writes = %v", writer.contents)
	}
	if err := admin.WriteCustom404Page(context.Background(), "<main>custom 404</main>"); err != nil {
		t.Fatalf("admin WriteCustom404Page() error = %v", err)
	}
	if got, want := writer.contents, []string{"<main>custom 404</main>"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("administrator writes = %v, want %v", got, want)
	}

	grant, err := sessions.SignIn(context.Background(), "admin", "environment-secret")
	if err != nil {
		t.Fatalf("SignIn(admin) error = %v", err)
	}
	unconfigured, err := OpenServerWorkspace(context.Background(), ServerWorkspaceDependencies{Sessions: sessions, Accounts: accounts, ControlPlane: plane}, grant.Token)
	if err != nil {
		t.Fatalf("OpenServerWorkspace(unconfigured) error = %v", err)
	}
	assertServerDomainCode(t, unconfigured.WriteCustom404Page(context.Background(), "<main>unconfigured</main>"), "FRPS_UNAVAILABLE")
}

type serverWorkspaceTestFRPSController struct {
	calls []ServerFRPSAction
}

func (controller *serverWorkspaceTestFRPSController) Start(context.Context) error {
	controller.calls = append(controller.calls, ServerFRPSActionStart)
	return nil
}

func (controller *serverWorkspaceTestFRPSController) Stop() error {
	controller.calls = append(controller.calls, ServerFRPSActionStop)
	return nil
}

func (controller *serverWorkspaceTestFRPSController) Restart(context.Context) error {
	controller.calls = append(controller.calls, ServerFRPSActionRestart)
	return nil
}

type serverWorkspaceTestCustom404PageReader struct {
	content string
	reads   int
}

func (reader *serverWorkspaceTestCustom404PageReader) ReadCustom404Page() (string, error) {
	reader.reads++
	return reader.content, nil
}

type serverWorkspaceTestCustom404PageWriter struct {
	contents []string
}

func (writer *serverWorkspaceTestCustom404PageWriter) WriteCustom404Page(content string) error {
	writer.contents = append(writer.contents, content)
	return nil
}

func openWorkspaceAfterSignIn(t *testing.T, sessions *ServerSessions, accounts *ServerAccounts, plane *ServerControlPlane, username, password string) *ServerWorkspace {
	t.Helper()
	grant, err := sessions.SignIn(context.Background(), username, password)
	if err != nil {
		t.Fatalf("SignIn(%q) error = %v", username, err)
	}
	workspace, err := OpenServerWorkspace(context.Background(), ServerWorkspaceDependencies{Sessions: sessions, Accounts: accounts, ControlPlane: plane}, grant.Token)
	if err != nil {
		t.Fatalf("OpenServerWorkspace(%q) error = %v", username, err)
	}
	return workspace
}
