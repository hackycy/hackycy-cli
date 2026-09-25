package tunnelruntime

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

var (
	ErrProcessNotFound        = errors.New("process not found")
	ErrProcessIdentityUnclear = errors.New("process identity is unclear")
)

// ProcessSnapshot contains the portable process metadata needed to identify
// an FRP child. Metadata that is unavailable on a platform is represented by
// its zero value.
type ProcessSnapshot struct {
	PID              int
	CreateTimeUnixMs int64
	Executable       string
	Args             []string
}

// ProcessOwner is the small persisted identity record used by Node after a
// restart. It describes only an FRPS process started by this supervisor.
type ProcessOwner struct {
	PID              int
	CreateTimeUnixMs int64
	BinaryPath       string
	ConfigPath       string
}

// ProcessInspector is the seam between ownership logic and the platform
// process metadata implementation.
type ProcessInspector interface {
	Inspect(pid int) (ProcessSnapshot, error)
}

// DefaultProcessInspector returns the gopsutil-backed process inspector.
func DefaultProcessInspector() ProcessInspector {
	return gopsutilProcessInspector{}
}

// CaptureFRPSOwner reads and validates the identity of a newly started FRPS.
func CaptureFRPSOwner(inspector ProcessInspector, pid int, binaryPath, configPath string) (ProcessOwner, error) {
	if inspector == nil {
		inspector = DefaultProcessInspector()
	}
	owner := ProcessOwner{PID: pid, BinaryPath: binaryPath, ConfigPath: configPath}
	snapshot, err := inspector.Inspect(pid)
	if err != nil {
		return ProcessOwner{}, err
	}
	owner.CreateTimeUnixMs = snapshot.CreateTimeUnixMs
	if !owner.Matches(snapshot) {
		return ProcessOwner{}, ErrProcessIdentityUnclear
	}
	return owner, nil
}

// VerifyFRPSOwner confirms that the persisted PID still describes the same
// FRPS process. The returned snapshot is useful when the caller wants to
// distinguish a missing process from an ownership mismatch.
func VerifyFRPSOwner(inspector ProcessInspector, owner ProcessOwner) (ProcessSnapshot, error) {
	if inspector == nil {
		inspector = DefaultProcessInspector()
	}
	snapshot, err := inspector.Inspect(owner.PID)
	if err != nil {
		return ProcessSnapshot{}, err
	}
	if !owner.Matches(snapshot) {
		return ProcessSnapshot{}, ErrProcessIdentityUnclear
	}
	return snapshot, nil
}

// TerminateOwnedFRPS stops a previously verified FRPS. Unix uses its process
// group; a persisted Windows owner can only terminate the recorded PID.
func TerminateOwnedFRPS(owner ProcessOwner) error {
	return terminateOwnedFRPS(owner)
}

// Matches applies the intentionally lightweight FRPS ownership predicate.
// Creation time is checked when both sides provide it; executable and -c
// configuration arguments are always required.
func (owner ProcessOwner) Matches(snapshot ProcessSnapshot) bool {
	if owner.PID <= 0 || snapshot.PID != owner.PID {
		return false
	}
	if owner.CreateTimeUnixMs != 0 && snapshot.CreateTimeUnixMs != 0 && owner.CreateTimeUnixMs != snapshot.CreateTimeUnixMs {
		return false
	}
	if !sameProcessPath(snapshot.Executable, owner.BinaryPath) {
		return false
	}
	return matchesFRPSArgs(snapshot.Args, owner.BinaryPath, owner.ConfigPath)
}

func matchesFRPSArgs(args []string, binaryPath, configPath string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] != "" && !sameProcessPath(args[0], binaryPath) {
		return false
	}
	for index := 1; index+1 < len(args); index++ {
		if args[index] == "-c" && sameProcessPath(args[index+1], configPath) {
			return true
		}
	}
	return false
}

func sameProcessPath(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	left = canonicalProcessPath(left)
	right = canonicalProcessPath(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func canonicalProcessPath(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return filepath.Clean(path)
}

func invalidProcessPID(pid int) error {
	return fmt.Errorf("%w: invalid PID %d", ErrProcessNotFound, pid)
}
