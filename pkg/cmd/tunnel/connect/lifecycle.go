package connect

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"

	"github.com/hackycy/hackycy-cli/internal/logging"
)

const (
	clientEventStarting       = "client.starting"
	clientEventStarted        = "client.started"
	clientEventAuthenticated  = "control.authenticated"
	clientEventRestored       = "control.restored"
	clientEventStateApplied   = "state.applied"
	clientEventStateSkipped   = "state.skipped"
	clientEventStateFailed    = "state.apply_failed"
	clientEventStateRecovered = "state.recovered"
	clientEventDisconnected   = "control.disconnected"
	clientEventReconnect      = "reconnect.scheduled"
	clientEventRevoked        = "control.revoked"
	clientEventShutdown       = "shutdown.requested"
	clientEventFailed         = "client.failed"
	clientEventStopped        = "client.stopped"
)

// clientLifecycle serializes session-level presentation decisions while the
// control loop and FRP supervisor may report transitions concurrently.
type clientLifecycle struct {
	logger logging.Logger

	mu               sync.Mutex
	hasAuthenticated bool
	failureWindow    bool
	applyFailure     bool
	shutdownLogged   bool
	revokedReason    string
	terminalLogged   bool
}

func newClientLifecycle(logger logging.Logger) *clientLifecycle {
	return &clientLifecycle{logger: logger}
}

func (lifecycle *clientLifecycle) event(level logging.Level, id, message string, fields map[string]any) {
	if lifecycle == nil {
		return
	}
	lifecycle.logger.Event(level, id, message, fields)
}

func (lifecycle *clientLifecycle) starting(config ClientConfig) {
	fields := map[string]any{"addressClass": clientAddressClass(config.Server)}
	if config.Server != nil && config.Server.Port() != "" {
		fields["port"] = config.Server.Port()
	}
	lifecycle.event(logging.Info, clientEventStarting, "Tunnel client starting", fields)
}

func (lifecycle *clientLifecycle) started(statePresent bool) {
	lifecycle.event(logging.Info, clientEventStarted, "Tunnel client started", map[string]any{
		"lock":                "owned",
		"appliedStatePresent": statePresent,
	})
}

func (lifecycle *clientLifecycle) authenticated() {
	lifecycle.mu.Lock()
	wasAuthenticated := lifecycle.hasAuthenticated
	wasWindow := lifecycle.failureWindow
	lifecycle.hasAuthenticated = true
	lifecycle.failureWindow = false
	lifecycle.mu.Unlock()
	if wasAuthenticated && wasWindow {
		lifecycle.event(logging.Info, clientEventRestored, "Tunnel control restored", nil)
		return
	}
	lifecycle.event(logging.Info, clientEventAuthenticated, "Tunnel control authenticated", nil)
}

func (lifecycle *clientLifecycle) connectionFailure(err error) {
	lifecycle.mu.Lock()
	authenticated := lifecycle.hasAuthenticated
	alreadyWindow := lifecycle.failureWindow
	if authenticated {
		lifecycle.failureWindow = true
	}
	lifecycle.mu.Unlock()
	fields := map[string]any{"failureClass": clientFailureClass(err)}
	if authenticated && !alreadyWindow {
		lifecycle.event(logging.Warn, clientEventDisconnected, "Tunnel control disconnected", fields)
		return
	}
	// Before the first successful authentication, retries are intentionally
	// debug-only so a temporary outage cannot flood normal diagnostics.
	lifecycle.event(logging.Debug, "control.retry", "Tunnel control retry", fields)
}

func (lifecycle *clientLifecycle) apply(result ClientApplyResult, err error) {
	if err != nil {
		fields := map[string]any{
			"revision":     result.Revision,
			"failureClass": clientReconciliationFailureClass(err),
			"rollback":     clientRollbackStatus(err),
		}
		lifecycle.mu.Lock()
		lifecycle.applyFailure = true
		lifecycle.mu.Unlock()
		lifecycle.event(logging.Warn, clientEventStateFailed, "Tunnel desired state apply failed", fields)
		return
	}
	fields := map[string]any{
		"revision":     result.Revision,
		"tunnelCount":  result.TunnelCount,
		"enabledCount": result.EnabledCount,
		"state":        string(result.State),
	}
	if result.Skipped {
		fields["reason"] = result.SkipReason
		lifecycle.event(logging.Debug, clientEventStateSkipped, "Tunnel desired state skipped", fields)
		return
	}
	lifecycle.mu.Lock()
	recovered := lifecycle.applyFailure
	lifecycle.applyFailure = false
	lifecycle.mu.Unlock()
	if recovered {
		lifecycle.event(logging.Info, clientEventStateRecovered, "Tunnel desired state recovered", fields)
		return
	}
	lifecycle.event(logging.Info, clientEventStateApplied, "Tunnel desired state applied", fields)
}

func (lifecycle *clientLifecycle) revoked(reason string) {
	if reason == "" {
		reason = "deleted"
	}
	lifecycle.mu.Lock()
	lifecycle.revokedReason = reason
	lifecycle.mu.Unlock()
	lifecycle.event(logging.Warn, clientEventRevoked, "Tunnel control revoked", map[string]any{"reason": reason})
}

func (lifecycle *clientLifecycle) shutdown(ctx context.Context) {
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
	lifecycle.event(logging.Info, clientEventShutdown, "Tunnel client shutdown requested", map[string]any{"reason": "cancelled"})
}

func (lifecycle *clientLifecycle) failed(err error) {
	if err == nil {
		return
	}
	lifecycle.event(logging.Error, clientEventFailed, "Tunnel client failed", map[string]any{"failureClass": clientFailureClass(err)})
}

func (lifecycle *clientLifecycle) stopped(ctx context.Context, err error) {
	lifecycle.mu.Lock()
	if lifecycle.terminalLogged {
		lifecycle.mu.Unlock()
		return
	}
	lifecycle.terminalLogged = true
	revoked := lifecycle.revokedReason != ""
	lifecycle.mu.Unlock()
	outcome := "succeeded"
	if revoked {
		outcome = "revoked"
	} else if ctx != nil && ctx.Err() != nil {
		outcome = "cancelled"
	} else if err != nil {
		outcome = "failed"
	}
	lifecycle.event(logging.Info, clientEventStopped, "Tunnel client stopped", map[string]any{"outcome": outcome})
}

func clientFailureClass(err error) string {
	if err == nil {
		return "unknown"
	}
	switch {
	case errors.Is(err, ErrClientAuthentication):
		return "unauthorized"
	case errors.Is(err, ErrClientIncompatible):
		return "incompatible"
	case errors.Is(err, ErrClientProtocol):
		return "protocol"
	case errors.Is(err, errClientControlRevoked):
		return "revoked"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "transport"
	default:
		return "transport"
	}
}

func clientReconciliationFailureClass(err error) string {
	var reconciliation *ClientReconciliationError
	if errors.As(err, &reconciliation) {
		switch strings.ToUpper(reconciliation.Code) {
		case "CONFIGURATION_FAILED":
			return "configuration"
		case "ACTIVATION_FAILED":
			return "activation"
		case "APPLY_FAILED":
			return "activation"
		}
	}
	return clientFailureClass(err)
}

func clientRollbackStatus(err error) string {
	var reconciliation *ClientReconciliationError
	if errors.As(err, &reconciliation) && reconciliation.Rollback != "" {
		return reconciliation.Rollback
	}
	return "not-required"
}

func clientAddressClass(server *url.URL) string {
	if server == nil {
		return "unspecified"
	}
	host := server.Hostname()
	if strings.EqualFold(host, "localhost") {
		return "loopback"
	}
	if address := net.ParseIP(host); address != nil {
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
