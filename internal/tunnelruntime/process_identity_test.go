package tunnelruntime

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

type staticProcessInspector struct {
	snapshot ProcessSnapshot
	err      error
}

func (inspector staticProcessInspector) Inspect(int) (ProcessSnapshot, error) {
	return inspector.snapshot, inspector.err
}

func TestGopsutilProcessInspectorReadsCurrentProcess(t *testing.T) {
	snapshot, err := DefaultProcessInspector().Inspect(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.PID != os.Getpid() || snapshot.Executable == "" || len(snapshot.Args) == 0 {
		t.Fatalf("incomplete current process snapshot: %+v", snapshot)
	}
}

func TestGopsutilProcessInspectorReportsMissingProcess(t *testing.T) {
	_, err := DefaultProcessInspector().Inspect(1 << 30)
	if !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("missing process error = %v", err)
	}
}

func TestCaptureAndVerifyFRPSOwnerUsesArgumentBoundaries(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "frp binary")
	config := filepath.Join(t.TempDir(), "config with spaces.toml")
	snapshot := ProcessSnapshot{
		PID:              42,
		CreateTimeUnixMs: 123,
		Executable:       binary,
		Args:             []string{binary, "-c", config},
	}
	inspector := staticProcessInspector{snapshot: snapshot}
	owner, err := CaptureFRPSOwner(inspector, snapshot.PID, binary, config)
	if err != nil {
		t.Fatal(err)
	}
	if owner.CreateTimeUnixMs != snapshot.CreateTimeUnixMs || !owner.Matches(snapshot) {
		t.Fatalf("owner = %+v, snapshot = %+v", owner, snapshot)
	}
	if _, err := VerifyFRPSOwner(inspector, ProcessOwner{PID: 42, CreateTimeUnixMs: 999, BinaryPath: binary, ConfigPath: config}); err != ErrProcessIdentityUnclear {
		t.Fatalf("create time mismatch = %v", err)
	}
	for _, change := range []struct {
		name     string
		snapshot ProcessSnapshot
	}{
		{name: "binary", snapshot: ProcessSnapshot{PID: 42, CreateTimeUnixMs: 123, Executable: binary + "-other", Args: []string{binary, "-c", config}}},
		{name: "config", snapshot: ProcessSnapshot{PID: 42, CreateTimeUnixMs: 123, Executable: binary, Args: []string{binary, "-c", config + "-other"}}},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, err := VerifyFRPSOwner(staticProcessInspector{snapshot: change.snapshot}, owner); !errors.Is(err, ErrProcessIdentityUnclear) {
				t.Fatalf("mismatch error = %v", err)
			}
		})
	}
}

func TestProcessOwnerAllowsUnavailableCreateTime(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "frps")
	config := filepath.Join(t.TempDir(), "frps.toml")
	owner := ProcessOwner{PID: 7, BinaryPath: binary, ConfigPath: config}
	snapshot := ProcessSnapshot{PID: 7, Executable: binary, Args: []string{binary, "-c", config}}
	if !owner.Matches(snapshot) {
		t.Fatal("owner with unavailable create time did not match")
	}
}

func TestProcessOwnerNormalizesExecutableSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions vary on Windows")
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "frps")
	link := filepath.Join(directory, "frps-link")
	if err := os.WriteFile(target, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(directory, "config.toml")
	owner := ProcessOwner{PID: 7, BinaryPath: link, ConfigPath: config}
	snapshot := ProcessSnapshot{PID: 7, Executable: target, Args: []string{target, "-c", config}}
	if !owner.Matches(snapshot) {
		t.Fatal("owner did not normalize executable symlink")
	}
}
