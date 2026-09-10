package server

import (
	"context"
	"errors"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"sync"
)

type ServerWorkspaceDependencies struct {
	Sessions            *ServerSessions
	Accounts            *ServerAccounts
	ControlPlane        *ServerControlPlane
	FRPS                ServerFRPSController
	Custom404PageReader ServerFRPSCustom404PageReader
	Custom404PageWriter ServerFRPSCustom404PageWriter
	FRPSChanges         ServerFRPSChangeObserver
}

// ServerFRPSAction is the closed control set exposed to an administrator.
type ServerFRPSAction string

const (
	ServerFRPSActionStart   ServerFRPSAction = "start"
	ServerFRPSActionStop    ServerFRPSAction = "stop"
	ServerFRPSActionRestart ServerFRPSAction = "restart"
)

// ServerFRPSController keeps the workspace authorization boundary independent
// from the concrete process supervisor.
type ServerFRPSController interface {
	Start(context.Context) error
	Stop() error
	Restart(context.Context) error
}

// ServerFRPSCustom404PageReader exposes only the managed custom-page read
// operation needed by an administrator workspace.
type ServerFRPSCustom404PageReader interface {
	ReadCustom404Page() (string, error)
}

// ServerFRPSCustom404PageWriter exposes only the managed custom-page write
// operation needed by an administrator workspace.
type ServerFRPSCustom404PageWriter interface {
	WriteCustom404Page(string) error
}

// ServerFRPSChangeObserver exposes managed FRPS runtime and custom-page
// invalidations without granting a workspace any configuration access.
type ServerFRPSChangeObserver interface {
	ObserveFRPSChanges(func()) func()
}

// ServerWorkspace applies a fresh session check at every domain operation.
// It is the sole owner of account-role and resource-owner authorization.
type ServerWorkspace struct {
	sessions            *ServerSessions
	accounts            *ServerAccounts
	controlPlane        *ServerControlPlane
	frps                ServerFRPSController
	custom404PageReader ServerFRPSCustom404PageReader
	custom404PageWriter ServerFRPSCustom404PageWriter
	frpsChanges         ServerFRPSChangeObserver
	token               string
}

type ServerWorkspaceEvent string

const (
	ServerWorkspaceChanged        ServerWorkspaceEvent = "changed"
	ServerWorkspaceSessionRevoked ServerWorkspaceEvent = "session_revoked"
)

func OpenServerWorkspace(ctx context.Context, dependencies ServerWorkspaceDependencies, token string) (*ServerWorkspace, error) {
	if dependencies.Sessions == nil || dependencies.Accounts == nil || dependencies.ControlPlane == nil {
		return nil, errors.New("Tunnel server workspace dependencies are required")
	}
	workspace := &ServerWorkspace{
		sessions:            dependencies.Sessions,
		accounts:            dependencies.Accounts,
		controlPlane:        dependencies.ControlPlane,
		frps:                dependencies.FRPS,
		custom404PageReader: dependencies.Custom404PageReader,
		custom404PageWriter: dependencies.Custom404PageWriter,
		frpsChanges:         dependencies.FRPSChanges,
		token:               token,
	}
	if _, err := workspace.currentAccount(ctx); err != nil {
		return nil, err
	}
	return workspace, nil
}

func (workspace *ServerWorkspace) currentAccount(ctx context.Context) (ServerAccount, error) {
	session, err := workspace.sessions.Resume(ctx, workspace.token)
	if err != nil {
		return ServerAccount{}, err
	}
	if session == nil {
		return ServerAccount{}, serverDomainError("AUTHENTICATION_REQUIRED", "Authenticated session is required")
	}
	return session.Account, nil
}

func (workspace *ServerWorkspace) Account(ctx context.Context) (ServerAccount, error) {
	return workspace.currentAccount(ctx)
}

// Observe receives resource invalidations visible to this workspace and an
// immediate notification when its session is revoked.
func (workspace *ServerWorkspace) Observe(ctx context.Context, listener func(ServerWorkspaceEvent)) (func(), error) {
	if listener == nil {
		return func() {}, nil
	}
	account, err := workspace.currentAccount(ctx)
	if err != nil {
		return nil, err
	}
	stopSession := workspace.sessions.Observe(workspace.token, func() {
		listener(ServerWorkspaceSessionRevoked)
	})
	stopAccountChanges := workspace.sessions.observeAccountChanges(func() {
		if account.Role == AccountRoleAdmin {
			listener(ServerWorkspaceChanged)
		}
	})
	stopControlPlane := workspace.controlPlane.Subscribe(func(event ServerControlPlaneEvent) {
		if account.Role == AccountRoleAdmin || account.ID == event.OwnerAccountID {
			listener(ServerWorkspaceChanged)
		}
	})
	stopFRPSChanges := func() {}
	if account.Role == AccountRoleAdmin && workspace.frpsChanges != nil {
		stopFRPSChanges = workspace.frpsChanges.ObserveFRPSChanges(func() {
			listener(ServerWorkspaceChanged)
		})
	}
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			stopFRPSChanges()
			stopControlPlane()
			stopAccountChanges()
			stopSession()
		})
	}, nil
}

func (workspace *ServerWorkspace) ListClients(ctx context.Context) ([]TrustedTunnelClient, error) {
	account, err := workspace.currentAccount(ctx)
	if err != nil {
		return nil, err
	}
	if account.Role == AccountRoleAdmin {
		return workspace.controlPlane.ListClients(ctx)
	}
	return workspace.controlPlane.ListClientsForOwner(ctx, account.ID)
}

func (workspace *ServerWorkspace) GetClient(ctx context.Context, clientID string) (TrustedTunnelClient, error) {
	account, err := workspace.currentAccount(ctx)
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	if account.Role == AccountRoleAdmin {
		return workspace.controlPlane.GetClient(ctx, clientID)
	}
	return workspace.controlPlane.GetClientForOwner(ctx, clientID, account.ID)
}

func (workspace *ServerWorkspace) CreateClient(ctx context.Context, remark string) (TrustedTunnelClient, error) {
	account, err := workspace.currentAccount(ctx)
	if err != nil {
		return TrustedTunnelClient{}, err
	}
	return workspace.controlPlane.CreateClient(ctx, account.ID, remark)
}

func (workspace *ServerWorkspace) UpdateClientRemark(ctx context.Context, clientID, remark string) (TrustedTunnelClient, error) {
	if _, err := workspace.GetClient(ctx, clientID); err != nil {
		return TrustedTunnelClient{}, err
	}
	return workspace.controlPlane.UpdateClientRemark(ctx, clientID, remark)
}

func (workspace *ServerWorkspace) RotateClientToken(ctx context.Context, clientID string) (TrustedTunnelClient, error) {
	if _, err := workspace.GetClient(ctx, clientID); err != nil {
		return TrustedTunnelClient{}, err
	}
	return workspace.controlPlane.RotateClientToken(ctx, clientID)
}

func (workspace *ServerWorkspace) DeleteClient(ctx context.Context, clientID string) error {
	if _, err := workspace.GetClient(ctx, clientID); err != nil {
		return err
	}
	return workspace.controlPlane.DeleteClient(ctx, clientID)
}

func (workspace *ServerWorkspace) ListTunnels(ctx context.Context, clientID string) ([]tunnelruntime.TunnelDefinition, error) {
	if _, err := workspace.GetClient(ctx, clientID); err != nil {
		return nil, err
	}
	return workspace.controlPlane.ListTunnels(ctx, clientID)
}

func (workspace *ServerWorkspace) CreateTunnel(ctx context.Context, clientID string, input TunnelMutationInput) (tunnelruntime.TunnelDefinition, error) {
	if _, err := workspace.GetClient(ctx, clientID); err != nil {
		return tunnelruntime.TunnelDefinition{}, err
	}
	return workspace.controlPlane.CreateTunnel(ctx, clientID, input)
}

func (workspace *ServerWorkspace) UpdateTunnel(ctx context.Context, tunnelID string, patch TunnelPatchInput) (tunnelruntime.TunnelDefinition, error) {
	if _, err := workspace.GetTunnel(ctx, tunnelID); err != nil {
		return tunnelruntime.TunnelDefinition{}, err
	}
	return workspace.controlPlane.UpdateTunnel(ctx, tunnelID, patch)
}

func (workspace *ServerWorkspace) ImportFRPCTunnels(ctx context.Context, clientID, source string, candidateIDs []string) ([]tunnelruntime.TunnelDefinition, error) {
	if _, err := workspace.GetClient(ctx, clientID); err != nil {
		return nil, err
	}
	return workspace.controlPlane.ImportFRPCTunnels(ctx, clientID, source, candidateIDs)
}

func (workspace *ServerWorkspace) PreviewFRPCTunnelImport(ctx context.Context, clientID, source string) (TunnelImportPreview, error) {
	if _, err := workspace.GetClient(ctx, clientID); err != nil {
		return TunnelImportPreview{}, err
	}
	imported, err := ParseFRPCTunnelImport(source)
	if err != nil {
		return TunnelImportPreview{}, err
	}
	return PreviewFRPCTunnelImport(imported), nil
}

func (workspace *ServerWorkspace) GetTunnel(ctx context.Context, tunnelID string) (ServerTunnel, error) {
	account, err := workspace.currentAccount(ctx)
	if err != nil {
		return ServerTunnel{}, err
	}
	if account.Role == AccountRoleAdmin {
		return workspace.controlPlane.GetTunnel(ctx, tunnelID)
	}
	return workspace.controlPlane.GetTunnelForOwner(ctx, tunnelID, account.ID)
}

func (workspace *ServerWorkspace) DeleteTunnel(ctx context.Context, tunnelID string) error {
	if _, err := workspace.GetTunnel(ctx, tunnelID); err != nil {
		return err
	}
	return workspace.controlPlane.DeleteTunnel(ctx, tunnelID)
}

func (workspace *ServerWorkspace) ChangeOwnPassword(ctx context.Context, currentPassword, replacementPassword string) (ServerAccount, error) {
	account, err := workspace.currentAccount(ctx)
	if err != nil {
		return ServerAccount{}, err
	}
	return workspace.sessions.ChangeOwnLocalAccountPassword(ctx, account.ID, currentPassword, replacementPassword)
}

func (workspace *ServerWorkspace) ListAccounts(ctx context.Context) ([]ServerAccountView, error) {
	if err := workspace.requireAdministrator(ctx); err != nil {
		return nil, err
	}
	return workspace.accounts.ListAccounts(ctx)
}

func (workspace *ServerWorkspace) CreateLocalAccount(ctx context.Context, username, password string, role AccountRole) (ServerAccount, error) {
	if err := workspace.requireAdministrator(ctx); err != nil {
		return ServerAccount{}, err
	}
	account, err := workspace.accounts.CreateLocalAccount(ctx, username, password, role)
	if err != nil {
		return ServerAccount{}, err
	}
	workspace.sessions.notifyAccountChanged()
	return account, nil
}

func (workspace *ServerWorkspace) ChangeLocalAccountRole(ctx context.Context, accountID string, role AccountRole) (ServerAccount, error) {
	if err := workspace.requireAdministrator(ctx); err != nil {
		return ServerAccount{}, err
	}
	return workspace.sessions.ChangeLocalAccountRole(ctx, accountID, role)
}

func (workspace *ServerWorkspace) ResetLocalAccountPassword(ctx context.Context, accountID, password string) (ServerAccount, error) {
	if err := workspace.requireAdministrator(ctx); err != nil {
		return ServerAccount{}, err
	}
	return workspace.sessions.ResetLocalAccountPassword(ctx, accountID, password)
}

func (workspace *ServerWorkspace) DeleteLocalAccount(ctx context.Context, accountID string) error {
	if err := workspace.requireAdministrator(ctx); err != nil {
		return err
	}
	return workspace.sessions.DeleteLocalAccount(ctx, accountID)
}

// ControlFRPS authorizes the fixed managed FRPS control set for an
// administrator. HTTP routing and response formatting remain separate.
func (workspace *ServerWorkspace) ControlFRPS(ctx context.Context, action ServerFRPSAction) error {
	if err := workspace.requireAdministrator(ctx); err != nil {
		return err
	}
	if workspace.frps == nil {
		return serverDomainError("FRPS_UNAVAILABLE", "Managed frps is unavailable")
	}
	switch action {
	case ServerFRPSActionStart:
		return workspace.frps.Start(ctx)
	case ServerFRPSActionStop:
		return workspace.frps.Stop()
	case ServerFRPSActionRestart:
		return workspace.frps.Restart(ctx)
	default:
		return errors.New("unsupported managed FRPS action")
	}
}

// ReadCustom404Page authorizes read-only custom-page access for an
// administrator without exposing the managed file path.
func (workspace *ServerWorkspace) ReadCustom404Page(ctx context.Context) (string, error) {
	if err := workspace.requireAdministrator(ctx); err != nil {
		return "", err
	}
	if workspace.custom404PageReader == nil {
		return "", serverDomainError("FRPS_UNAVAILABLE", "Managed frps is unavailable")
	}
	return workspace.custom404PageReader.ReadCustom404Page()
}

// WriteCustom404Page authorizes custom-page mutation for an administrator;
// page validation and publication stay owned by the managed configuration.
func (workspace *ServerWorkspace) WriteCustom404Page(ctx context.Context, content string) error {
	if err := workspace.requireAdministrator(ctx); err != nil {
		return err
	}
	if workspace.custom404PageWriter == nil {
		return serverDomainError("FRPS_UNAVAILABLE", "Managed frps is unavailable")
	}
	return workspace.custom404PageWriter.WriteCustom404Page(content)
}

func (workspace *ServerWorkspace) requireAdministrator(ctx context.Context) error {
	account, err := workspace.currentAccount(ctx)
	if err != nil {
		return err
	}
	if account.Role != AccountRoleAdmin {
		return serverDomainError("FORBIDDEN", "Administrator role is required")
	}
	return nil
}
