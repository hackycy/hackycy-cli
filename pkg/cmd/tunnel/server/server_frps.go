package server

import (
	"context"
	"errors"
	"fmt"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
)

const (
	maxCustom404PageBytes                = 512 * 1024
	frpsConfigurationVerificationTimeout = 10 * time.Second
)

var (
	ErrManagedFRPSConfiguration             = errors.New("managed FRPS configuration is invalid")
	ErrFRPSConfigurationVerificationTimeout = errors.New("FRPS configuration verification timed out")
)

// ManagedFRPSOptions composes the private FRPS configuration with the safe
// server-state projection consumed by the HTTP adapter.
type ManagedFRPSOptions struct {
	Settings         ServerHTTPServerSettings
	InternalFRPToken string
	Supervisor       *tunnelruntime.FRPSupervisor
	Prepare          func(context.Context) error
	LifecycleLogger  logging.Logger
}

// ManagedFRPS owns the static server-side FRPS configuration. Process control
// and custom-page mutation remain separate responsibilities.
type ManagedFRPS struct {
	settings         ServerHTTPServerSettings
	internalFRPToken string
	supervisor       *tunnelruntime.FRPSupervisor
	prepare          func(context.Context) error
	lifecycleLogger  logging.Logger
	configuration    string
	custom404Page    string
	operations       sync.Mutex
	configurationMu  sync.Mutex
	custom404Mu      sync.Mutex
	observersMu      sync.Mutex
	observers        map[uint64]func()
	nextObserver     uint64
}

func NewManagedFRPS(options ManagedFRPSOptions) (*ManagedFRPS, error) {
	if options.Supervisor == nil {
		return nil, fmt.Errorf("%w: supervisor is required", ErrManagedFRPSConfiguration)
	}
	if strings.TrimSpace(options.Settings.DataDir) == "" {
		return nil, fmt.Errorf("%w: data directory is required", ErrManagedFRPSConfiguration)
	}
	if strings.TrimSpace(options.InternalFRPToken) == "" {
		return nil, fmt.Errorf("%w: internal FRP token is required", ErrManagedFRPSConfiguration)
	}
	dataDirectory, err := filepath.Abs(options.Settings.DataDir)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve data directory: %v", ErrManagedFRPSConfiguration, err)
	}
	settings := cloneServerHTTPServerSettings(options.Settings)
	settings.DataDir = dataDirectory
	managed := &ManagedFRPS{
		settings:         settings,
		internalFRPToken: options.InternalFRPToken,
		supervisor:       options.Supervisor,
		prepare:          options.Prepare,
		lifecycleLogger:  options.LifecycleLogger,
		configuration:    filepath.Join(dataDirectory, "frps.toml"),
		custom404Page:    filepath.Join(dataDirectory, "404.html"),
		observers:        make(map[uint64]func()),
	}
	managed.supervisor.Observe(func(tunnelruntime.FRPSupervisorState) {
		managed.notifyFRPSChanges()
	})
	return managed, nil
}

func (managed *ManagedFRPS) ConfigurationPath() string {
	return managed.configuration
}

func (managed *ManagedFRPS) Custom404PagePath() string {
	return managed.custom404Page
}

func (managed *ManagedFRPS) ReadCustom404Page() (string, error) {
	contents, err := os.ReadFile(managed.custom404Page)
	if errors.Is(err, os.ErrNotExist) && isMissingManagedFRPSPath(managed.custom404Page) {
		return "", nil
	}
	if err != nil {
		return "", serverDomainError("CONFIGURATION_FAILED", fmt.Sprintf("Could not read custom 404 page: %v", err))
	}
	return string(contents), nil
}

// isMissingManagedFRPSPath distinguishes an absent page from a Windows path
// error that is also reported as os.ErrNotExist when a parent is not a folder.
func isMissingManagedFRPSPath(path string) bool {
	if _, err := os.Stat(path); err == nil {
		return false
	} else if !errors.Is(err, os.ErrNotExist) {
		return false
	}
	parentInfo, err := os.Stat(filepath.Dir(path))
	if err == nil {
		return parentInfo.IsDir()
	}
	return errors.Is(err, os.ErrNotExist)
}

func (managed *ManagedFRPS) WriteCustom404Page(content string) error {
	if len(content) > maxCustom404PageBytes {
		return serverDomainError("INVALID_CUSTOM_404_PAGE", "Custom 404 page must not exceed 512 KiB")
	}
	managed.custom404Mu.Lock()
	var err error
	if content == "" {
		if removeErr := os.Remove(managed.custom404Page); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = removeErr
		}
	} else {
		err = writeManagedFRPSFile(managed.custom404Page, content)
	}
	managed.custom404Mu.Unlock()
	if err != nil {
		return custom404WriteError(err)
	}
	managed.notifyFRPSChanges()
	return nil
}

// ObserveFRPSChanges receives later runtime-state and custom-page changes.
// The initial state remains available through State and does not invalidate a
// newly connected EventSource twice.
func (managed *ManagedFRPS) ObserveFRPSChanges(listener func()) func() {
	if listener == nil {
		return func() {}
	}
	managed.observersMu.Lock()
	id := managed.nextObserver
	managed.nextObserver++
	managed.observers[id] = listener
	managed.observersMu.Unlock()
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			managed.observersMu.Lock()
			delete(managed.observers, id)
			managed.observersMu.Unlock()
		})
	}
}

func (managed *ManagedFRPS) notifyFRPSChanges() {
	managed.observersMu.Lock()
	observers := make([]func(), 0, len(managed.observers))
	for _, observer := range managed.observers {
		observers = append(observers, observer)
	}
	managed.observersMu.Unlock()
	for _, observer := range observers {
		observer()
	}
}

func custom404WriteError(cause error) error {
	if cause == nil {
		return nil
	}
	return serverDomainError("CONFIGURATION_FAILED", fmt.Sprintf("Could not write custom 404 page: %v", cause))
}

func (managed *ManagedFRPS) PublishConfiguration() error {
	contents, err := managed.RenderConfiguration()
	if err != nil {
		return serverDomainError("CONFIGURATION_FAILED", fmt.Sprintf("Could not render frps configuration: %v", err))
	}
	managed.configurationMu.Lock()
	defer managed.configurationMu.Unlock()
	if err := writeManagedFRPSFile(managed.configuration, contents); err != nil {
		return serverDomainError("CONFIGURATION_FAILED", fmt.Sprintf("Could not write frps configuration: %v", err))
	}
	return nil
}

// VerifyPublishedConfiguration asks the managed FRPS binary to validate the
// already-published configuration without changing the running process.
func (managed *ManagedFRPS) VerifyPublishedConfiguration(ctx context.Context) error {
	return verifyFRPSConfiguration(ctx, managed.supervisor.BinaryPath(), managed.configuration, frpsConfigurationVerificationTimeout)
}

// Start replaces any prior FRPS child only after publishing and verifying the
// managed configuration. The resulting failure state stays observable through
// State while callers retain the domain failure that caused it.
func (managed *ManagedFRPS) Start(ctx context.Context) error {
	managed.operations.Lock()
	defer managed.operations.Unlock()
	managed.lifecycleLogger.Event(logging.Debug, "frps.start_requested", "Managed FRPS start requested", nil)
	return managed.start(ctx)
}

func (managed *ManagedFRPS) start(ctx context.Context) error {
	if managed.prepare != nil {
		if err := managed.prepare(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return managed.recordFRPSConfigurationFailure(err)
		}
	}
	if err := managed.supervisor.Stop(); err != nil {
		return managed.recordFRPSActivationFailure(err)
	}
	if err := managed.PublishConfiguration(); err != nil {
		return managed.recordFRPSConfigurationFailure(err)
	}
	if err := managed.VerifyPublishedConfiguration(ctx); err != nil {
		if ctx.Err() != nil {
			return err
		}
		return managed.recordFRPSConfigurationFailure(err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := managed.supervisor.Start(managed.configuration); err != nil {
		return managed.recordFRPSActivationFailure(err)
	}
	return nil
}

// Restart re-runs managed publication and verification before replacing the
// active FRPS child.
func (managed *ManagedFRPS) Restart(ctx context.Context) error {
	managed.operations.Lock()
	defer managed.operations.Unlock()
	managed.lifecycleLogger.Event(logging.Debug, "frps.restart_requested", "Managed FRPS restart requested", nil)
	if err := managed.start(ctx); err != nil {
		return err
	}
	managed.lifecycleLogger.Event(logging.Info, "frps.restarted", "Managed FRPS restarted", nil)
	return nil
}

// Stop serializes manual shutdown with activation without changing managed
// configuration or starting a replacement child.
func (managed *ManagedFRPS) Stop() error {
	managed.operations.Lock()
	defer managed.operations.Unlock()
	managed.lifecycleLogger.Event(logging.Debug, "frps.stop_requested", "Managed FRPS stop requested", nil)
	return managed.supervisor.Stop()
}

func (managed *ManagedFRPS) recordFRPSConfigurationFailure(cause error) error {
	var failure *ServerDomainError
	if !errors.As(cause, &failure) || failure.Code != "CONFIGURATION_FAILED" {
		cause = serverDomainError("CONFIGURATION_FAILED", fmt.Sprintf("Could not prepare managed frps configuration: %v", cause))
		_ = errors.As(cause, &failure)
	}
	_ = managed.supervisor.ConfigurationFailed(tunnelruntime.StructuredRuntimeError{Code: failure.Code, Message: failure.Message})
	return cause
}

func (managed *ManagedFRPS) recordFRPSActivationFailure(cause error) error {
	message := fmt.Sprintf("Managed frps failed to start for FRP bind %s:%d or HTTP vhost %s:%d: %v. Stop any existing frps or other process listening on these ports before starting ycy. Inspect listeners with lsof -nP -iTCP:%d -sTCP:LISTEN and lsof -nP -iTCP:%d -sTCP:LISTEN, or ss -ltnp 'sport = :%d' and ss -ltnp 'sport = :%d'.", managed.settings.Address, managed.settings.FRPPort, managed.settings.Address, managed.settings.HTTPPort, cause, managed.settings.FRPPort, managed.settings.HTTPPort, managed.settings.FRPPort, managed.settings.HTTPPort)
	failure := serverDomainError("ACTIVATION_FAILED", message)
	_ = managed.supervisor.ConfigurationFailed(tunnelruntime.StructuredRuntimeError{Code: "ACTIVATION_FAILED", Message: message})
	return failure
}

func verifyFRPSConfiguration(ctx context.Context, binaryPath, configurationPath string, timeout time.Duration) error {
	verificationContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := exec.CommandContext(verificationContext, binaryPath, "verify", "-c", configurationPath).CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	failure := frpsConfigurationVerificationFailure(output, err)
	if errors.Is(verificationContext.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", ErrFRPSConfigurationVerificationTimeout, failure)
	}
	return failure
}

func frpsConfigurationVerificationFailure(output []byte, cause error) error {
	if len(output) != 0 {
		return serverDomainError("CONFIGURATION_FAILED", fmt.Sprintf("Could not verify frps configuration: %s", output))
	}
	return serverDomainError("CONFIGURATION_FAILED", fmt.Sprintf("Could not verify frps configuration: %v", cause))
}

func writeManagedFRPSFile(target, contents string) error {
	directory := filepath.Dir(target)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	candidate, err := os.CreateTemp(directory, filepath.Base(target)+".candidate-*")
	if err != nil {
		return err
	}
	candidatePath := candidate.Name()
	defer func() { _ = os.Remove(candidatePath) }()
	if err := tunnelruntime.ProtectPrivateFile(candidatePath, 0o600); err != nil {
		_ = candidate.Close()
		return err
	}
	if _, err := candidate.WriteString(contents); err != nil {
		_ = candidate.Close()
		return err
	}
	if err := candidate.Close(); err != nil {
		return err
	}
	return os.Rename(candidatePath, target)
}

func (managed *ManagedFRPS) RenderConfiguration() (string, error) {
	return tunnelruntime.RenderFRPSConfig(tunnelruntime.FRPServerConfiguration{
		BindAddress:      managed.settings.Address,
		BindPort:         int64(managed.settings.FRPPort),
		VhostHTTPPort:    int64(managed.settings.HTTPPort),
		Custom404Page:    managed.custom404Page,
		InternalFRPToken: managed.internalFRPToken,
		PortRangeStart:   int64(managed.settings.PortRange.Start),
		PortRangeEnd:     int64(managed.settings.PortRange.End),
	})
}

// State implements ServerHTTPStateProvider without admitting deployment
// secrets into the HTTP state model.
func (managed *ManagedFRPS) State() ServerHTTPState {
	return ServerHTTPState{
		FRPS:     managed.supervisor.State(),
		Settings: cloneServerHTTPServerSettings(managed.settings),
	}
}

// FRPSState exposes only process availability to the agent admission path.
func (managed *ManagedFRPS) FRPSState() tunnelruntime.FRPSupervisorState {
	return managed.supervisor.State()
}

// AgentWelcomeSettings exposes the endpoint and Internal FRP Token only to
// the authenticated agent-gateway boundary, never to the HTTP state model.
func (managed *ManagedFRPS) AgentWelcomeSettings(requestHost string) ServerAgentWelcomeSettings {
	host := requestHost
	port := int64(managed.settings.FRPPort)
	if managed.settings.AdvertiseFRPAddr != nil {
		host = managed.settings.AdvertiseFRPAddr.Host
		port = int64(managed.settings.AdvertiseFRPAddr.Port)
	}
	return ServerAgentWelcomeSettings{
		AdvertisedFRPHost: host,
		AdvertisedFRPPort: port,
		InternalFRPToken:  managed.internalFRPToken,
	}
}

func cloneServerHTTPServerSettings(settings ServerHTTPServerSettings) ServerHTTPServerSettings {
	clone := settings
	if settings.AdvertiseFRPAddr != nil {
		address := *settings.AdvertiseFRPAddr
		clone.AdvertiseFRPAddr = &address
	}
	return clone
}
