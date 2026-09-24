package connect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

var ErrClientReconcilerStopped = errors.New("Tunnel client is stopping")

// ClientFRPRuntime is the narrow client-owned process surface needed to apply
// one verified FRPC configuration. Binary acquisition and supervision bind to
// it in the following slice.
type ClientFRPRuntime interface {
	Verify(context.Context, string) error
	Start(string) error
	Stop() error
}

// ClientReconcilerOptions supplies one instance's cache, typed renderer, and
// future FRPC lifecycle dependency.
type ClientReconcilerOptions struct {
	StateDirectory string
	Runtime        ClientFRPRuntime
	LogLevel       string
}

// ClientReconciler serializes desired-state application and owns rollback of
// its instance's active configuration and child process.
type ClientReconciler struct {
	stateDirectory string
	runtime        ClientFRPRuntime
	logLevel       string
	status         *clientFRPCStatusEndpoint

	operations sync.Mutex
	activated  bool
	stopped    bool
}

// ClientReconciliationError retains the protocol-visible failure class while
// preserving its local cause for diagnostics and process-state reporting.
type ClientReconciliationError struct {
	Code     string
	Cause    error
	Rollback string
}

// ClientApplyResult is the safe local outcome used by the Lifecycle Log. It
// contains no rendered configuration, addresses, credentials, or filesystem
// paths.
type ClientApplyResult struct {
	Revision     int64
	TunnelCount  int
	EnabledCount int
	State        tunnelruntime.FRPProcessState
	Skipped      bool
	SkipReason   string
	Rollback     string
}

func (err *ClientReconciliationError) Error() string {
	if err == nil || err.Cause == nil {
		return "Tunnel client reconciliation failed"
	}
	return err.Cause.Error()
}

func (err *ClientReconciliationError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

// NewClientReconciler creates a per-instance transaction owner without
// reading cache, starting a child, or authorizing cold activation.
func NewClientReconciler(options ClientReconcilerOptions) (*ClientReconciler, error) {
	if strings.TrimSpace(options.StateDirectory) == "" {
		return nil, fmt.Errorf("Tunnel client state directory is required")
	}
	if options.Runtime == nil {
		return nil, fmt.Errorf("Tunnel client FRP runtime is required")
	}
	stateDirectory, err := filepath.Abs(options.StateDirectory)
	if err != nil {
		return nil, fmt.Errorf("resolve Tunnel client state directory: %w", err)
	}
	reconciler := &ClientReconciler{stateDirectory: stateDirectory, runtime: options.Runtime, logLevel: options.LogLevel}
	if provider, ok := options.Runtime.(interface {
		FRPCStatusEndpoint() *clientFRPCStatusEndpoint
	}); ok {
		reconciler.status = provider.FRPCStatusEndpoint()
	}
	return reconciler, nil
}

// Apply reconciles one complete desired snapshot. It never starts a cached
// configuration until the current authenticated welcome has reached it.
func (reconciler *ClientReconciler) Apply(ctx context.Context, desired ClientDesiredConfiguration) error {
	_, err := reconciler.ApplyWithResult(ctx, desired)
	return err
}

// ApplyWithResult reconciles one snapshot and returns a presentation-safe
// disposition while retaining Apply's original error and side-effect contract.
func (reconciler *ClientReconciler) ApplyWithResult(ctx context.Context, desired ClientDesiredConfiguration) (ClientApplyResult, error) {
	if reconciler == nil {
		return ClientApplyResult{}, fmt.Errorf("Tunnel client reconciler is unavailable")
	}
	reconciler.operations.Lock()
	defer reconciler.operations.Unlock()
	if reconciler.stopped {
		return ClientApplyResult{}, ErrClientReconcilerStopped
	}
	result := clientApplyResult(desired)
	if err := validateClientDesiredConfiguration(desired); err != nil {
		return result, clientReconciliationError("CONFIGURATION_FAILED", err)
	}
	desired = clientDesiredConfigurationFromRuntime(desired.normalizedRuntime())
	current, hasCurrent := ReadClientAppliedState(reconciler.stateDirectory)
	if hasCurrent && runtimeIsOlder(desired.normalizedRuntime(), current.normalizedRuntime()) {
		return result, clientReconciliationError("PROTOCOL_FAILED", fmt.Errorf("desired runtime revision %d is below last applied revision", desired.normalizedRuntime().Revision))
	}
	highestAccepted, hasHighestAccepted, err := loadClientAcceptedState(reconciler.stateDirectory)
	if err != nil {
		return result, clientReconciliationError("STATE_CORRUPT", err)
	}
	if hasHighestAccepted && runtimeIsOlder(desired.normalizedRuntime(), highestAccepted.normalizedRuntime()) {
		return result, clientReconciliationError("PROTOCOL_FAILED", fmt.Errorf("desired runtime revision %d is below highest accepted revision", desired.normalizedRuntime().Revision))
	}
	if hasHighestAccepted && desired.normalizedRuntime().Revision == highestAccepted.normalizedRuntime().Revision && !runtimeIsEqual(desired.normalizedRuntime(), highestAccepted.normalizedRuntime()) {
		return result, clientReconciliationError("PROTOCOL_FAILED", fmt.Errorf("desired runtime digest changed at revision %d", desired.normalizedRuntime().Revision))
	}
	if hasCurrent && desired.normalizedRuntime().Revision == current.normalizedRuntime().Revision && !runtimeIsEqual(desired.normalizedRuntime(), current.normalizedRuntime()) {
		return result, clientReconciliationError("PROTOCOL_FAILED", fmt.Errorf("desired runtime digest changed at revision %d", desired.normalizedRuntime().Revision))
	}
	crossNode := hasCurrent && desired.normalizedRuntime().NodeID != current.normalizedRuntime().NodeID
	if !hasHighestAccepted || !runtimeIsEqual(desired.normalizedRuntime(), highestAccepted.normalizedRuntime()) {
		accepted := ClientAppliedState{ClientDesiredConfiguration: desired, Revision: desired.normalizedRuntime().Revision}
		if err := WriteClientAcceptedState(reconciler.stateDirectory, accepted); err != nil {
			if crossNode {
				err = errors.Join(err, reconciler.runtime.Stop())
				reconciler.activated = false
			}
			return result, clientReconciliationError("STATE_FAILED", fmt.Errorf("persist highest accepted runtime: %w", err))
		}
	}
	if reconciler.activated && hasCurrent && runtimeIsEqual(desired.normalizedRuntime(), current.normalizedRuntime()) {
		result.Skipped = true
		result.SkipReason = "duplicate-revision"
		return result, nil
	}
	if crossNode {
		if err := reconciler.runtime.Stop(); err != nil {
			return result, clientReconciliationError("ACTIVATION_FAILED", fmt.Errorf("stop previous Node frpc: %w", err))
		}
		reconciler.activated = false
	}

	runtime := desired.normalizedRuntime()
	configuration, err := tunnelruntime.RenderFRPCConfig(tunnelruntime.FRPClientConfiguration{
		AdvertisedFRPHost: runtime.AdvertisedFRPHost,
		AdvertisedFRPPort: runtime.AdvertisedFRPPort,
		InternalFRPToken:  runtime.FRPToken,
		Snapshot:          tunnelruntime.TunnelSnapshot{ClientKey: runtime.ClientKey, Revision: runtime.Revision, Tunnels: runtime.Tunnels},
		LogLevel:          reconciler.logLevel,
		WebServer:         clientFRPCWebServer(reconciler.status),
	})
	if err != nil {
		return result, clientReconciliationError("CONFIGURATION_FAILED", err)
	}
	candidatePath := filepath.Join(reconciler.stateDirectory, fmt.Sprintf("frpc.revision-%d.candidate.toml", runtime.Revision))
	if err := writeClientFileAtomically(candidatePath, []byte(configuration)); err != nil {
		return result, clientReconciliationError("ACTIVATION_FAILED", err)
	}
	defer func() { _ = os.Remove(candidatePath) }()

	enabled := clientDesiredStateHasEnabledTunnel(desired)
	if enabled {
		if err := reconciler.runtime.Verify(ctx, candidatePath); err != nil {
			return result, clientReconciliationError("CONFIGURATION_FAILED", fmt.Errorf("Could not verify frpc configuration: %w", err))
		}
	}

	activePath := clientActiveFRPCConfigPath(reconciler.stateDirectory)
	previousConfiguration, hasPreviousConfiguration, err := optionalClientFile(activePath)
	if err != nil {
		return result, clientReconciliationError("ACTIVATION_FAILED", err)
	}
	if !crossNode {
		if err := reconciler.runtime.Stop(); err != nil {
			return result, clientReconciliationError("ACTIVATION_FAILED", fmt.Errorf("stop previous frpc: %w", err))
		}
	}
	if err := writeClientFileAtomically(activePath, []byte(configuration)); err != nil {
		if crossNode {
			return result, clientReconciliationError("ACTIVATION_FAILED", err)
		}
		return result, reconciler.rollbackActivation(current, hasCurrent, previousConfiguration, hasPreviousConfiguration, clientReconciliationError("ACTIVATION_FAILED", err))
	}
	if enabled {
		if err := reconciler.runtime.Start(activePath); err != nil {
			if crossNode {
				return result, clientReconciliationError("ACTIVATION_FAILED", errors.Join(fmt.Errorf("start frpc: %w", err), reconciler.runtime.Stop()))
			}
			return result, reconciler.rollbackActivation(current, hasCurrent, previousConfiguration, hasPreviousConfiguration, clientReconciliationError("ACTIVATION_FAILED", fmt.Errorf("start frpc: %w", err)))
		}
	}
	state := ClientAppliedState{ClientDesiredConfiguration: desired, Revision: runtime.Revision}
	if err := WriteClientAppliedState(reconciler.stateDirectory, state); err != nil {
		if crossNode {
			return result, clientReconciliationError("ACTIVATION_FAILED", errors.Join(err, reconciler.runtime.Stop()))
		}
		return result, reconciler.rollbackActivation(current, hasCurrent, previousConfiguration, hasPreviousConfiguration, clientReconciliationError("ACTIVATION_FAILED", err))
	}
	reconciler.activated = true
	return result, nil
}

func clientFRPCWebServer(endpoint *clientFRPCStatusEndpoint) *tunnelruntime.FRPClientWebServer {
	if endpoint == nil {
		return nil
	}
	configuration := endpoint.config
	return &configuration
}

func clientApplyResult(desired ClientDesiredConfiguration) ClientApplyResult {
	runtime := desired.normalizedRuntime()
	tunnelCount := len(runtime.Tunnels)
	enabledCount := 0
	for _, tunnel := range runtime.Tunnels {
		if tunnel.Enabled {
			enabledCount++
		}
	}
	state := tunnelruntime.FRPProcessStopped
	if enabledCount > 0 {
		state = tunnelruntime.FRPProcessRunning
	}
	return ClientApplyResult{Revision: runtime.Revision, TunnelCount: tunnelCount, EnabledCount: enabledCount, State: state, Rollback: "not-required"}
}

// Restart delegates only an already-applied enabled snapshot to a runtime
// that supports an imperative frpc restart frame.
func (reconciler *ClientReconciler) Restart() error {
	if reconciler == nil {
		return fmt.Errorf("Tunnel client reconciler is unavailable")
	}
	reconciler.operations.Lock()
	defer reconciler.operations.Unlock()
	if reconciler.stopped {
		return ErrClientReconcilerStopped
	}
	current, found := ReadClientAppliedState(reconciler.stateDirectory)
	if !found || !clientDesiredStateHasEnabledTunnel(current.ClientDesiredConfiguration) {
		return nil
	}
	accepted, found, err := loadClientAcceptedState(reconciler.stateDirectory)
	if err != nil {
		return clientReconciliationError("STATE_CORRUPT", err)
	}
	if found && !runtimeIsEqual(current.normalizedRuntime(), accepted.normalizedRuntime()) {
		return clientReconciliationError("ACTIVATION_FAILED", fmt.Errorf("last applied runtime is behind the accepted target"))
	}
	restarter, supported := reconciler.runtime.(interface{ Restart() error })
	if !supported {
		return clientReconciliationError("ACTIVATION_FAILED", fmt.Errorf("Tunnel client FRP runtime cannot restart"))
	}
	if err := restarter.Restart(); err != nil {
		if clientReconciliationErrorCode(err) != "" {
			return err
		}
		return clientReconciliationError("ACTIVATION_FAILED", fmt.Errorf("restart frpc: %w", err))
	}
	return nil
}

// Stop prevents later desired-state work and releases the owned frpc child.
func (reconciler *ClientReconciler) Stop() error {
	if reconciler == nil {
		return nil
	}
	reconciler.operations.Lock()
	defer reconciler.operations.Unlock()
	reconciler.stopped = true
	reconciler.activated = false
	return reconciler.runtime.Stop()
}

func (reconciler *ClientReconciler) rollbackActivation(previous *ClientAppliedState, hasPrevious bool, previousConfiguration []byte, hasPreviousConfiguration bool, cause error) error {
	rollbackErr := reconciler.runtime.Stop()
	if hasPreviousConfiguration {
		rollbackErr = errors.Join(rollbackErr, writeClientFileAtomically(clientActiveFRPCConfigPath(reconciler.stateDirectory), previousConfiguration))
	} else {
		removeErr := os.Remove(clientActiveFRPCConfigPath(reconciler.stateDirectory))
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			rollbackErr = errors.Join(rollbackErr, removeErr)
		}
	}
	if hasPrevious && previous != nil && clientDesiredStateHasEnabledTunnel(previous.ClientDesiredConfiguration) && hasPreviousConfiguration {
		rollbackErr = errors.Join(rollbackErr, reconciler.runtime.Start(clientActiveFRPCConfigPath(reconciler.stateDirectory)))
	}
	if rollbackErr == nil {
		if reconciliation, ok := cause.(*ClientReconciliationError); ok {
			reconciliation.Rollback = "restored"
		}
		return cause
	}
	failure := clientReconciliationError("ACTIVATION_FAILED", errors.Join(cause, fmt.Errorf("restore previous frpc state: %w", rollbackErr)))
	if reconciliation, ok := failure.(*ClientReconciliationError); ok {
		reconciliation.Rollback = "failed"
	}
	return failure
}

func validateClientDesiredConfiguration(desired ClientDesiredConfiguration) error {
	runtime := desired.normalizedRuntime()
	if strings.TrimSpace(runtime.NodeID) == "" || strings.TrimSpace(runtime.AdvertisedFRPHost) == "" || runtime.AdvertisedFRPPort < 1 || runtime.AdvertisedFRPPort > 65535 || strings.TrimSpace(runtime.FRPToken) == "" || strings.TrimSpace(runtime.ClientKey) == "" || strings.TrimSpace(runtime.Digest) == "" {
		return fmt.Errorf("desired FRPC configuration is incomplete")
	}
	if runtime.Revision < 0 || runtime.Revision > clientMaximumSafeInteger {
		return fmt.Errorf("desired revision is invalid")
	}
	digest, err := tunnelruntime.RuntimeDigest(runtime)
	if err != nil || digest != runtime.Digest {
		return fmt.Errorf("desired runtime digest is invalid")
	}
	return nil
}

func runtimeIsOlder(next, current tunnelruntime.ClientRuntime) bool {
	return next.Revision < current.Revision
}

func runtimeIsEqual(left, right tunnelruntime.ClientRuntime) bool {
	return left.Revision == right.Revision && left.NodeID == right.NodeID && left.Digest == right.Digest
}

func clientDesiredStateHasEnabledTunnel(desired ClientDesiredConfiguration) bool {
	for _, definition := range desired.Runtime.Tunnels {
		if definition.Enabled {
			return true
		}
	}
	return false
}

func optionalClientFile(path string) ([]byte, bool, error) {
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read prior Tunnel client configuration: %w", err)
	}
	return contents, true, nil
}

func clientReconciliationError(code string, cause error) error {
	return &ClientReconciliationError{Code: code, Cause: cause}
}

func clientReconciliationErrorCode(err error) string {
	var reconciliationError *ClientReconciliationError
	if errors.As(err, &reconciliationError) {
		return reconciliationError.Code
	}
	return ""
}
