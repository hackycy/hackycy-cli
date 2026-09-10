package server

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"

	"github.com/hackycy/hackycy-cli/internal/logging"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

const (
	serverEventStarting            = "server.starting"
	serverEventStateOpened         = "state.opened"
	serverEventListening           = "control.listening"
	serverEventStarted             = "server.started"
	serverEventFrpsPreparing       = "frps.preparing"
	serverEventFrpsRunning         = "frps.running"
	serverEventFrpsStopped         = "frps.stopped"
	serverEventFrpsRecovering      = "frps.recovering"
	serverEventFrpsRecovered       = "frps.recovered"
	serverEventFrpsFailed          = "frps.failed"
	serverEventAgentConnected      = "agent.connected"
	serverEventAgentDisconnected   = "agent.disconnected"
	serverEventAgentRestored       = "agent.restored"
	serverEventAgentRevoked        = "agent.revoked"
	serverEventAgentWarning        = "agent.warning"
	serverEventAgentStateChanged   = "agent.state_changed"
	serverEventAgentStateRecovered = "agent.state_recovered"
	serverEventAgentStateWarning   = "agent.state_warning"
	serverEventAgentProcessState   = "agent.process_state"
	serverEventControlChange       = "control.change"
	serverEventShutdown            = "shutdown.requested"
	serverEventFailed              = "server.failed"
	serverEventStopped             = "server.stopped"
)

type serverLifecycle struct {
	logger logging.Logger

	mu              sync.Mutex
	frpsInitialized bool
	lastFRPSState   tunnelruntime.FRPProcessState
	shutdownLogged  bool
	failureLogged   bool
	terminalLogged  bool
}

func newServerLifecycle(logger logging.Logger) *serverLifecycle {
	return &serverLifecycle{logger: logger, lastFRPSState: tunnelruntime.FRPProcessStopped}
}

func (lifecycle *serverLifecycle) event(level logging.Level, id, message string, fields map[string]any) {
	if lifecycle == nil {
		return
	}
	lifecycle.logger.Event(level, id, message, fields)
}

func (lifecycle *serverLifecycle) starting(config ServerConfig) {
	settings := config.Settings
	fields := map[string]any{
		"addressClass":          serverAddressClass(settings.Address),
		"controlPort":           settings.ControlPort,
		"frpPort":               settings.FRPPort,
		"httpVhostPort":         settings.HTTPPort,
		"portPoolStart":         settings.PortRange.Start,
		"portPoolEnd":           settings.PortRange.End,
		"advertisedConfigured":  settings.AdvertiseFRPAddr != nil,
		"sessionIdleDurationMs": config.SessionIdleLifetime.Milliseconds(),
		"frpTokenSource":        serverFRPTokenSource(config.FRPToken),
	}
	if settings.AdvertiseFRPAddr != nil {
		fields["advertisedAddressClass"] = serverAddressClass(settings.AdvertiseFRPAddr.Host)
		fields["advertisedPort"] = settings.AdvertiseFRPAddr.Port
	}
	lifecycle.event(logging.Info, serverEventStarting, "Tunnel server starting", fields)
}

func (lifecycle *serverLifecycle) stateOpened(config ServerConfig) {
	lifecycle.event(logging.Info, serverEventStateOpened, "Tunnel server state opened", map[string]any{
		"addressClass":   serverAddressClass(config.Settings.Address),
		"frpTokenSource": serverFRPTokenSource(config.FRPToken),
	})
}

func (lifecycle *serverLifecycle) listening(port int) {
	lifecycle.event(logging.Info, serverEventListening, "Tunnel control listening", map[string]any{"port": port})
}

func (lifecycle *serverLifecycle) started() {
	lifecycle.event(logging.Info, serverEventStarted, "Tunnel control plane started", nil)
}

func (lifecycle *serverLifecycle) frpsPreparing() {
	lifecycle.event(logging.Debug, serverEventFrpsPreparing, "Managed FRPS preparing", nil)
}

func (lifecycle *serverLifecycle) frpsState(state tunnelruntime.FRPSupervisorState) {
	lifecycle.mu.Lock()
	previous := lifecycle.lastFRPSState
	initialized := lifecycle.frpsInitialized
	shuttingDown := lifecycle.shutdownLogged
	lifecycle.lastFRPSState = state.State
	lifecycle.frpsInitialized = true
	lifecycle.mu.Unlock()
	if !initialized || previous == state.State {
		return
	}
	switch state.State {
	case tunnelruntime.FRPProcessRunning:
		if previous == tunnelruntime.FRPProcessRecovering {
			lifecycle.event(logging.Info, serverEventFrpsRecovered, "Managed FRPS recovered", nil)
		} else {
			lifecycle.event(logging.Info, serverEventFrpsRunning, "Managed FRPS running", nil)
		}
	case tunnelruntime.FRPProcessRecovering:
		lifecycle.event(logging.Warn, serverEventFrpsRecovering, "Managed FRPS recovering", map[string]any{"failureClass": "frps", "reason": "unexpected-exit"})
	case tunnelruntime.FRPProcessConfigurationFailed:
		lifecycle.event(logging.Error, serverEventFrpsFailed, "Managed FRPS failed", map[string]any{"failureClass": "configuration", "reason": "configuration"})
	case tunnelruntime.FRPProcessStopped:
		reason := "admin"
		if shuttingDown {
			reason = "shutdown"
		}
		lifecycle.event(logging.Info, serverEventFrpsStopped, "Managed FRPS stopped", map[string]any{"reason": reason})
	}
}

func (lifecycle *serverLifecycle) shutdown(ctx context.Context, reason string) {
	if ctx == nil || ctx.Err() == nil {
		return
	}
	lifecycle.mu.Lock()
	if lifecycle.shutdownLogged {
		lifecycle.mu.Unlock()
		return
	}
	lifecycle.shutdownLogged = true
	lifecycle.mu.Unlock()
	if reason == "" {
		reason = "cancelled"
	}
	lifecycle.event(logging.Info, serverEventShutdown, "Tunnel server stopping (shutdown requested)", map[string]any{"reason": reason})
}

func (lifecycle *serverLifecycle) failed(stage string, err error) {
	if err == nil {
		return
	}
	lifecycle.mu.Lock()
	if lifecycle.terminalLogged || lifecycle.failureLogged {
		lifecycle.mu.Unlock()
		return
	}
	lifecycle.failureLogged = true
	lifecycle.mu.Unlock()
	lifecycle.event(logging.Error, serverEventFailed, "Tunnel server failed", map[string]any{
		"phase":        stage,
		"failureClass": serverFailureClass(stage, err),
	})
}

func (lifecycle *serverLifecycle) stopped(ctx context.Context, err error) {
	lifecycle.mu.Lock()
	if lifecycle.terminalLogged {
		lifecycle.mu.Unlock()
		return
	}
	lifecycle.terminalLogged = true
	lifecycle.mu.Unlock()
	outcome := "succeeded"
	if err != nil {
		outcome = "failed"
	} else if ctx != nil && ctx.Err() != nil {
		outcome = "cancelled"
	}
	lifecycle.event(logging.Info, serverEventStopped, "Tunnel server stopped", map[string]any{"outcome": outcome})
}

func serverFRPTokenSource(token string) string {
	if strings.TrimSpace(token) == "" {
		return "generated/reused"
	}
	return "explicit"
}

func serverAddressClass(value string) string {
	host := strings.TrimSpace(value)
	if host == "" {
		return "unspecified"
	}
	if strings.EqualFold(host, "localhost") {
		return "loopback"
	}
	if address := net.ParseIP(host); address != nil {
		if address.IsUnspecified() {
			return "unspecified"
		}
		if address.IsLoopback() {
			return "loopback"
		}
		if address.IsPrivate() {
			return "private"
		}
		return "public"
	}
	if strings.HasSuffix(strings.ToLower(host), ".local") {
		return "private"
	}
	return "public"
}

func serverFailureClass(stage string, err error) string {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "transport"
	}
	switch stage {
	case "state.open_failed":
		return "database"
	case "control.bind_failed":
		return "bind"
	case "control.listener_failed":
		return "transport"
	case "frps.preparation_failed":
		return "frps"
	default:
		return "composition"
	}
}
