package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
	webassets "github.com/hackycy/hackycy-cli/web"
)

const serverInternalFRPTokenMetaKey = "internal_frp_token"

// ServerRuntimeOptions identifies the private resources needed by one Tunnel
// server process. Listener and CLI ownership remain outside this composition.
type ServerRuntimeOptions struct {
	Settings            ServerHTTPServerSettings
	AdminPassword       string
	SessionIdleLifetime time.Duration
	FRPToken            string
	FRPSLogger          logging.Logger
	LifecycleLogger     logging.Logger

	frpArtifact         *tunnelruntime.FRPArtifact
	frpRuntimeDirectory string
	ensureFRPRuntime    serverFRPRuntimeEnsurer
}

type serverFRPRuntimeEnsurer func(context.Context, string, tunnelruntime.FRPArtifact) (tunnelruntime.FRPRuntimePaths, error)

// ServerRuntime owns the complete unregistered Tunnel server resource graph.
// It does not bind a listener or start managed FRPS.
type ServerRuntime struct {
	lock         *tunnelruntime.StateDirectoryLock
	state        *State
	accounts     *ServerAccounts
	sessions     *ServerSessions
	controlPlane *ServerControlPlane
	supervisor   *tunnelruntime.FRPSupervisor
	frps         *ManagedFRPS
	gateway      *ServerAgentGateway
	handler      http.Handler
	frpArtifact  tunnelruntime.FRPArtifact
	frpDirectory string
	ensureFRP    serverFRPRuntimeEnsurer

	close    sync.Once
	closeErr error
}

// NewServerRuntime composes all private server services after taking the
// state-directory lock. A construction failure releases every earlier owner.
func NewServerRuntime(ctx context.Context, options ServerRuntimeOptions) (*ServerRuntime, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(options.Settings.DataDir) == "" {
		return nil, errors.New("Tunnel server data directory is required")
	}
	dataDirectory, err := filepath.Abs(options.Settings.DataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve Tunnel server data directory: %w", err)
	}
	options.Settings.DataDir = dataDirectory

	lock, err := tunnelruntime.AcquireStateDirectoryLock(dataDirectory)
	if err != nil {
		return nil, err
	}
	runtime := &ServerRuntime{lock: lock}
	fail := func(cause error) (*ServerRuntime, error) {
		return nil, errors.Join(cause, runtime.Close())
	}

	runtime.state, err = OpenState(StateOptions{
		DataDirectory:       dataDirectory,
		SessionIdleLifetime: options.SessionIdleLifetime,
	})
	if err != nil {
		return fail(err)
	}
	runtime.accounts, err = NewServerAccounts(ctx, ServerAccountsOptions{
		Database:      runtime.state.database,
		AdminUsername: options.Settings.AdminUser,
		AdminPassword: options.AdminPassword,
	})
	if err != nil {
		return fail(err)
	}
	runtime.sessions, err = NewServerSessions(runtime.accounts, runtime.state.sessions)
	if err != nil {
		return fail(err)
	}
	runtime.controlPlane, err = NewServerControlPlane(ServerControlPlaneOptions{
		Database: runtime.state.database,
		PortRange: ServerPortRange{
			Start: int64(options.Settings.PortRange.Start),
			End:   int64(options.Settings.PortRange.End),
		},
	})
	if err != nil {
		return fail(err)
	}
	internalFRPToken, err := resolveServerInternalFRPToken(ctx, runtime.state.database, options.FRPToken)
	if err != nil {
		return fail(err)
	}
	runtime.frpArtifact, runtime.frpDirectory, runtime.ensureFRP, err = resolveServerFRPRuntime(options)
	if err != nil {
		return fail(err)
	}
	frpPaths := tunnelruntime.FRPRuntimePathsFor(runtime.frpDirectory, runtime.frpArtifact.Target)
	runtime.supervisor, err = tunnelruntime.NewFRPSupervisor(tunnelruntime.FRPSupervisorOptions{
		BinaryPath: frpPaths.FRPS,
		Role:       tunnelruntime.FRPRoleServer,
		Logger:     options.FRPSLogger,
	})
	if err != nil {
		return fail(err)
	}
	runtime.frps, err = NewManagedFRPS(ManagedFRPSOptions{
		Settings:         options.Settings,
		InternalFRPToken: internalFRPToken,
		Supervisor:       runtime.supervisor,
		Prepare:          runtime.ensureManagedFRPRuntime,
		LifecycleLogger:  options.LifecycleLogger,
	})
	if err != nil {
		return fail(err)
	}
	runtime.gateway, err = NewServerAgentGateway(ServerAgentGatewayOptions{
		ControlPlane:  runtime.controlPlane,
		FRPS:          runtime.frps,
		WelcomeSource: runtime.frps,
		Logger:        options.LifecycleLogger,
	})
	if err != nil {
		return fail(err)
	}
	adapter, err := NewServerHTTPHandler(ServerHTTPOptions{
		Sessions:            runtime.sessions,
		Accounts:            runtime.accounts,
		ControlPlane:        runtime.controlPlane,
		FRPS:                runtime.frps,
		Custom404PageReader: runtime.frps,
		Custom404PageWriter: runtime.frps,
		FRPSChanges:         runtime.frps,
		AgentGateway:        runtime.gateway,
		ServerState:         runtime.frps,
	})
	if err != nil {
		return fail(err)
	}
	runtime.handler, err = webassets.NewTunnelProductionHandler(adapter)
	if err != nil {
		return fail(err)
	}
	return runtime, nil
}

func resolveServerFRPRuntime(options ServerRuntimeOptions) (tunnelruntime.FRPArtifact, string, serverFRPRuntimeEnsurer, error) {
	artifact := tunnelruntime.FRPArtifact{}
	if options.frpArtifact != nil {
		artifact = *options.frpArtifact
	} else {
		resolved, err := tunnelruntime.CurrentFRPArtifact()
		if err != nil {
			return tunnelruntime.FRPArtifact{}, "", nil, err
		}
		artifact = resolved
	}
	directory := options.frpRuntimeDirectory
	if directory == "" {
		resolved, err := tunnelruntime.DefaultFRPRuntimeDirectory()
		if err != nil {
			return tunnelruntime.FRPArtifact{}, "", nil, err
		}
		directory = resolved
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return tunnelruntime.FRPArtifact{}, "", nil, fmt.Errorf("resolve managed FRP runtime directory: %w", err)
	}
	ensure := options.ensureFRPRuntime
	if ensure == nil {
		ensure = tunnelruntime.EnsureFRPRuntimeAt
	}
	return artifact, directory, ensure, nil
}

func (runtime *ServerRuntime) ensureManagedFRPRuntime(ctx context.Context) error {
	if runtime == nil || runtime.ensureFRP == nil {
		return errors.New("Tunnel server FRP runtime is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	paths, err := runtime.ensureFRP(ctx, runtime.frpDirectory, runtime.frpArtifact)
	if err != nil {
		return err
	}
	expected := tunnelruntime.FRPRuntimePathsFor(runtime.frpDirectory, runtime.frpArtifact.Target)
	if paths != expected {
		return errors.New("managed FRP preparation returned paths outside the pinned runtime directory")
	}
	return nil
}

// Handler returns the composed production HTTP surface for the later listener
// owner.
func (runtime *ServerRuntime) Handler() http.Handler {
	if runtime == nil {
		return nil
	}
	return runtime.handler
}

// Close stops managed FRPS before releasing fresh-Go state and the server
// state-directory lock. It is safe to call more than once.
func (runtime *ServerRuntime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.close.Do(func() {
		var result error
		if runtime.frps != nil {
			result = errors.Join(result, runtime.frps.Stop())
		}
		if runtime.state != nil {
			result = errors.Join(result, runtime.state.Close())
		}
		if runtime.lock != nil {
			result = errors.Join(result, runtime.lock.Release())
		}
		runtime.closeErr = result
	})
	return runtime.closeErr
}

func resolveServerInternalFRPToken(ctx context.Context, database *sql.DB, configured string) (string, error) {
	if database == nil {
		return "", errors.New("Tunnel server database is required")
	}
	if configured != "" {
		token := strings.TrimSpace(configured)
		if token == "" {
			return "", errors.New("Tunnel server FRP token must not be empty")
		}
		return token, nil
	}
	var token string
	err := database.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, serverInternalFRPTokenMetaKey).Scan(&token)
	if err == nil && token != "" {
		return token, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read Tunnel server Internal FRP Token: %w", err)
	}
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate Tunnel server Internal FRP Token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(bytes)
	if _, err := database.ExecContext(ctx, `
		INSERT INTO meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, serverInternalFRPTokenMetaKey, token); err != nil {
		return "", fmt.Errorf("persist Tunnel server Internal FRP Token: %w", err)
	}
	return token, nil
}
