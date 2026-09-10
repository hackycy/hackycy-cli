package tunnelruntime

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
)

const (
	frpcActivationGrace = 250 * time.Millisecond
	frpsActivationGrace = 3 * time.Second
	frpStableAfter      = time.Minute
	frpStopTimeout      = 5 * time.Second
)

var (
	ErrFRPSupervisorConfiguration = errors.New("FRP supervisor configuration is invalid")
	ErrFRPSupervisorStopped       = errors.New("FRP supervisor has no applied configuration")
)

var defaultFRPRecoveryBackoff = []time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	15 * time.Second,
	30 * time.Second,
}

// DefaultFRPRecoveryBackoff returns a fresh copy of the shared FRP retry
// schedule so a caller cannot mutate the supervisor default.
func DefaultFRPRecoveryBackoff() []time.Duration {
	return append([]time.Duration(nil), defaultFRPRecoveryBackoff...)
}

type FRPRole string

const (
	FRPRoleClient FRPRole = "frpc"
	FRPRoleServer FRPRole = "frps"
)

// FRPSupervisorOptions configures one Tunnel-owned FRP child lifecycle.
type FRPSupervisorOptions struct {
	BinaryPath      string
	Role            FRPRole
	ActivationGrace time.Duration
	StableAfter     time.Duration
	StopTimeout     time.Duration
	Backoff         []time.Duration
	Logger          logging.Logger
}

// FRPSupervisorState is the local runtime state reported to a future owner.
type FRPSupervisorState struct {
	State FRPProcessState
	PID   *int
	Error *StructuredRuntimeError
}

type frpChild interface {
	PID() int
	Stdout() io.Reader
	Stderr() io.Reader
	Done() <-chan struct{}
	WaitError() error
	Terminate() error
	Kill() error
	Release() error
}

// FRPSupervisor serializes ownership of one frpc or frps child tree.
type FRPSupervisor struct {
	options FRPSupervisorOptions

	operations sync.Mutex
	mu         sync.Mutex

	child          frpChild
	configPath     string
	desiredRunning bool
	failureCount   int
	retryTimer     *time.Timer
	stableTimer    *time.Timer
	state          FRPSupervisorState
	observers      map[uint64]func(FRPSupervisorState)
	nextObserver   uint64
}

// NewFRPSupervisor constructs an owner-local supervisor. It does not resolve
// binaries, render TOML, or compose a command lifecycle.
func NewFRPSupervisor(options FRPSupervisorOptions) (*FRPSupervisor, error) {
	if strings.TrimSpace(options.BinaryPath) == "" {
		return nil, fmt.Errorf("%w: FRP binary path is required", ErrFRPSupervisorConfiguration)
	}
	if options.Role != FRPRoleClient && options.Role != FRPRoleServer {
		return nil, fmt.Errorf("%w: unsupported FRP role %q", ErrFRPSupervisorConfiguration, options.Role)
	}
	if options.ActivationGrace == 0 {
		if options.Role == FRPRoleClient {
			options.ActivationGrace = frpcActivationGrace
		} else {
			options.ActivationGrace = frpsActivationGrace
		}
	}
	if options.ActivationGrace < 0 || options.StableAfter < 0 || options.StopTimeout < 0 {
		return nil, fmt.Errorf("%w: FRP durations must not be negative", ErrFRPSupervisorConfiguration)
	}
	if options.StableAfter == 0 {
		options.StableAfter = frpStableAfter
	}
	if options.StopTimeout == 0 {
		options.StopTimeout = frpStopTimeout
	}
	if len(options.Backoff) == 0 {
		options.Backoff = append([]time.Duration(nil), defaultFRPRecoveryBackoff...)
	} else {
		options.Backoff = append([]time.Duration(nil), options.Backoff...)
	}
	for _, delay := range options.Backoff {
		if delay < 0 {
			return nil, fmt.Errorf("%w: FRP recovery delay must not be negative", ErrFRPSupervisorConfiguration)
		}
	}
	return &FRPSupervisor{
		options:   options,
		state:     FRPSupervisorState{State: FRPProcessStopped},
		observers: make(map[uint64]func(FRPSupervisorState)),
	}, nil
}

func (supervisor *FRPSupervisor) State() FRPSupervisorState {
	supervisor.mu.Lock()
	defer supervisor.mu.Unlock()
	return cloneFRPSupervisorState(supervisor.state)
}

// BinaryPath returns the fixed executable path configured for the supervisor.
func (supervisor *FRPSupervisor) BinaryPath() string {
	if supervisor == nil {
		return ""
	}
	return supervisor.options.BinaryPath
}

// Role returns whether the supervisor owns frpc or frps.
func (supervisor *FRPSupervisor) Role() FRPRole {
	if supervisor == nil {
		return ""
	}
	return supervisor.options.Role
}

// CloneFRPSupervisorState returns an independent state value, including its
// optional process ID and structured error.
func CloneFRPSupervisorState(state FRPSupervisorState) FRPSupervisorState {
	return cloneFRPSupervisorState(state)
}

// Observe receives the current state immediately and all later transitions.
func (supervisor *FRPSupervisor) Observe(listener func(FRPSupervisorState)) func() {
	if supervisor == nil || listener == nil {
		return func() {}
	}
	supervisor.mu.Lock()
	id := supervisor.nextObserver
	supervisor.nextObserver++
	supervisor.observers[id] = listener
	state := cloneFRPSupervisorState(supervisor.state)
	supervisor.mu.Unlock()
	listener(state)
	return func() {
		supervisor.mu.Lock()
		delete(supervisor.observers, id)
		supervisor.mu.Unlock()
	}
}

// Start records configPath and ensures exactly one running FRP child.
func (supervisor *FRPSupervisor) Start(configPath string) error {
	supervisor.operations.Lock()
	defer supervisor.operations.Unlock()
	return supervisor.start(configPath)
}

func (supervisor *FRPSupervisor) start(configPath string) error {
	supervisor.mu.Lock()
	changed := false
	if configPath != "" {
		changed = supervisor.configPath != configPath
		supervisor.configPath = configPath
	}
	if supervisor.configPath == "" {
		supervisor.mu.Unlock()
		return ErrFRPSupervisorStopped
	}
	supervisor.desiredRunning = true
	child := supervisor.child
	if child != nil && !changed {
		supervisor.mu.Unlock()
		return nil
	}
	supervisor.mu.Unlock()

	if child != nil {
		if err := supervisor.stopChild(); err != nil {
			return err
		}
	}
	child, err := supervisor.spawn()
	if err != nil {
		supervisor.stopAfterStartFailure(err)
		return err
	}
	return supervisor.confirmActivation(child)
}

// Stop suppresses recovery and releases the current owner-local child tree.
func (supervisor *FRPSupervisor) Stop() error {
	supervisor.operations.Lock()
	defer supervisor.operations.Unlock()
	supervisor.mu.Lock()
	supervisor.desiredRunning = false
	supervisor.failureCount = 0
	supervisor.mu.Unlock()
	stopErr := supervisor.stopChild()
	supervisor.publish(FRPSupervisorState{State: FRPProcessStopped})
	return stopErr
}

// Restart stops the owned child and starts the last applied configuration.
func (supervisor *FRPSupervisor) Restart() error {
	supervisor.operations.Lock()
	defer supervisor.operations.Unlock()
	supervisor.mu.Lock()
	if supervisor.configPath == "" {
		supervisor.mu.Unlock()
		return ErrFRPSupervisorStopped
	}
	supervisor.desiredRunning = true
	supervisor.mu.Unlock()
	if err := supervisor.stopChild(); err != nil {
		return err
	}
	child, err := supervisor.spawn()
	if err != nil {
		supervisor.stopAfterStartFailure(err)
		return err
	}
	return supervisor.confirmActivation(child)
}

// ConfigurationFailed stops the child and leaves a deterministic failure
// visible until a later Start or Restart call.
func (supervisor *FRPSupervisor) ConfigurationFailed(runtimeError StructuredRuntimeError) error {
	supervisor.operations.Lock()
	defer supervisor.operations.Unlock()
	supervisor.mu.Lock()
	supervisor.desiredRunning = false
	supervisor.failureCount = 0
	supervisor.mu.Unlock()
	stopErr := supervisor.stopChild()
	supervisor.publish(FRPSupervisorState{State: FRPProcessConfigurationFailed, Error: &runtimeError})
	return stopErr
}

func (supervisor *FRPSupervisor) spawn() (frpChild, error) {
	supervisor.mu.Lock()
	configPath := supervisor.configPath
	supervisor.mu.Unlock()
	child, err := startFRPChild(supervisor.options.BinaryPath, configPath)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", supervisor.options.Role, err)
	}

	supervisor.mu.Lock()
	supervisor.clearTimersLocked()
	supervisor.child = child
	publish := supervisor.setStateLocked(FRPSupervisorState{State: FRPProcessRunning, PID: pointer(child.PID())})
	supervisor.stableTimer = time.AfterFunc(supervisor.options.StableAfter, func() {
		supervisor.mu.Lock()
		if supervisor.child == child {
			supervisor.failureCount = 0
		}
		supervisor.mu.Unlock()
	})
	supervisor.mu.Unlock()
	publish()
	supervisor.consumeOutput(child.Stdout(), "stdout")
	supervisor.consumeOutput(child.Stderr(), "stderr")
	go supervisor.watchExit(child)
	return child, nil
}

func (supervisor *FRPSupervisor) confirmActivation(child frpChild) error {
	timer := time.NewTimer(supervisor.options.ActivationGrace)
	defer timer.Stop()
	select {
	case <-timer.C:
		select {
		case <-child.Done():
			return supervisor.activationExited(child)
		default:
			return nil
		}
	case <-child.Done():
		return supervisor.activationExited(child)
	}
}

func (supervisor *FRPSupervisor) activationExited(child frpChild) error {
	exitErr := child.WaitError()
	supervisor.mu.Lock()
	if supervisor.child != child {
		supervisor.mu.Unlock()
		supervisor.releaseChild(child)
		return fmt.Errorf("%s exited during startup: %w", supervisor.options.Role, exitErr)
	}
	supervisor.child = nil
	supervisor.desiredRunning = false
	supervisor.failureCount = 0
	supervisor.clearTimersLocked()
	publish := supervisor.setStateLocked(FRPSupervisorState{State: FRPProcessStopped})
	supervisor.mu.Unlock()
	supervisor.releaseChild(child)
	publish()
	return fmt.Errorf("%s exited during startup: %w", supervisor.options.Role, exitErr)
}

func (supervisor *FRPSupervisor) stopAfterStartFailure(startErr error) {
	supervisor.mu.Lock()
	supervisor.desiredRunning = false
	supervisor.failureCount = 0
	supervisor.clearTimersLocked()
	publish := supervisor.setStateLocked(FRPSupervisorState{
		State: FRPProcessStopped,
		Error: &StructuredRuntimeError{Code: "FRP_START_FAILED", Message: startErr.Error()},
	})
	supervisor.mu.Unlock()
	publish()
}

func (supervisor *FRPSupervisor) watchExit(child frpChild) {
	<-child.Done()
	supervisor.operations.Lock()
	defer supervisor.operations.Unlock()

	supervisor.mu.Lock()
	if supervisor.child != child {
		supervisor.mu.Unlock()
		return
	}
	supervisor.child = nil
	supervisor.stopStableTimerLocked()
	if !supervisor.desiredRunning {
		publish := supervisor.setStateLocked(FRPSupervisorState{State: FRPProcessStopped})
		supervisor.mu.Unlock()
		supervisor.releaseChild(child)
		publish()
		return
	}
	delay := supervisor.options.Backoff[min(supervisor.failureCount, len(supervisor.options.Backoff)-1)]
	supervisor.failureCount++
	publish := supervisor.setStateLocked(FRPSupervisorState{
		State: FRPProcessRecovering,
		Error: &StructuredRuntimeError{Code: "FRP_EXITED", Message: frpExitMessage(supervisor.options.Role, child.WaitError())},
	})
	supervisor.scheduleRecoveryLocked(delay)
	supervisor.mu.Unlock()
	supervisor.releaseChild(child)
	publish()
}

func (supervisor *FRPSupervisor) scheduleRecoveryLocked(delay time.Duration) {
	var timer *time.Timer
	timer = time.AfterFunc(delay, func() {
		supervisor.operations.Lock()
		defer supervisor.operations.Unlock()
		supervisor.mu.Lock()
		if supervisor.retryTimer != timer || !supervisor.desiredRunning || supervisor.child != nil {
			supervisor.mu.Unlock()
			return
		}
		supervisor.retryTimer = nil
		supervisor.mu.Unlock()
		if _, err := supervisor.spawn(); err != nil {
			supervisor.stopAfterStartFailure(err)
		}
	})
	supervisor.retryTimer = timer
}

func (supervisor *FRPSupervisor) stopChild() error {
	supervisor.mu.Lock()
	supervisor.clearTimersLocked()
	child := supervisor.child
	supervisor.child = nil
	supervisor.mu.Unlock()
	if child == nil {
		return nil
	}
	if terminateErr := child.Terminate(); terminateErr != nil {
		killErr := child.Kill()
		if killErr == nil {
			<-child.Done()
		}
		return errors.Join(terminateErr, killErr, child.Release())
	}
	timer := time.NewTimer(supervisor.options.StopTimeout)
	defer timer.Stop()
	select {
	case <-child.Done():
		return child.Release()
	case <-timer.C:
		killErr := child.Kill()
		if killErr == nil {
			<-child.Done()
		}
		return errors.Join(killErr, child.Release())
	}
}

func (supervisor *FRPSupervisor) clearTimersLocked() {
	if supervisor.retryTimer != nil {
		supervisor.retryTimer.Stop()
		supervisor.retryTimer = nil
	}
	supervisor.stopStableTimerLocked()
}

func (supervisor *FRPSupervisor) stopStableTimerLocked() {
	if supervisor.stableTimer != nil {
		supervisor.stableTimer.Stop()
		supervisor.stableTimer = nil
	}
}

func (supervisor *FRPSupervisor) publish(state FRPSupervisorState) {
	supervisor.mu.Lock()
	publish := supervisor.setStateLocked(state)
	supervisor.mu.Unlock()
	publish()
}

func (supervisor *FRPSupervisor) setStateLocked(state FRPSupervisorState) func() {
	supervisor.state = cloneFRPSupervisorState(state)
	listeners := make([]func(FRPSupervisorState), 0, len(supervisor.observers))
	for _, listener := range supervisor.observers {
		listeners = append(listeners, listener)
	}
	state = cloneFRPSupervisorState(state)
	return func() {
		for _, listener := range listeners {
			listener(cloneFRPSupervisorState(state))
		}
	}
}

func (supervisor *FRPSupervisor) consumeOutput(stream io.Reader, streamName string) {
	if stream == nil {
		return
	}
	go func() {
		scanner := bufio.NewScanner(stream)
		scanner.Buffer(make([]byte, 256), 4096)
		for scanner.Scan() {
			if line := scanner.Text(); line != "" {
				supervisor.options.Logger.Event(logging.Debug, "frp.child_output", "FRP child output", map[string]any{
					"role":   supervisor.options.Role,
					"stream": streamName,
					"detail": safeFRPChildLine(line),
				})
			}
		}
		if err := scanner.Err(); err != nil {
			supervisor.options.Logger.Event(logging.Warn, "frp.child_output_warning", "FRP child output warning", map[string]any{
				"role":   supervisor.options.Role,
				"reason": "scanner",
			})
		}
	}()
}

func (supervisor *FRPSupervisor) releaseChild(child frpChild) {
	if err := child.Release(); err != nil {
		supervisor.options.Logger.Event(logging.Warn, "frp.child_release_warning", "FRP child ownership warning", map[string]any{
			"role":   supervisor.options.Role,
			"reason": "ownership",
		})
	}
}

func safeFRPChildLine(line string) string {
	line = logging.RedactDiagnostic(line)
	line = strings.TrimSpace(line)
	if line == "" {
		return "suppressed"
	}
	lower := strings.ToLower(line)
	for _, marker := range []string{"authorization", "bearer ", "token", "password", "secret", "credential", "private key", "http://", "https://", ".toml", "config/", "\\"} {
		if strings.Contains(lower, marker) {
			return "suppressed"
		}
	}
	if len(line) > 240 {
		line = line[:240] + "..."
	}
	return line
}

func frpExitMessage(role FRPRole, exitErr error) string {
	if exitErr == nil {
		return fmt.Sprintf("%s exited successfully", role)
	}
	var exitError *exec.ExitError
	if errors.As(exitErr, &exitError) {
		return fmt.Sprintf("%s exited with code %d", role, exitError.ExitCode())
	}
	return fmt.Sprintf("%s exited: %v", role, exitErr)
}

func cloneFRPSupervisorState(state FRPSupervisorState) FRPSupervisorState {
	clone := state
	if state.PID != nil {
		clone.PID = pointer(*state.PID)
	}
	if state.Error != nil {
		errorClone := *state.Error
		clone.Error = &errorClone
	}
	return clone
}

func pointer(value int) *int {
	return &value
}
