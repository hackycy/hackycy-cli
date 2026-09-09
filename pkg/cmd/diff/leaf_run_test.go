package diff

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestRunDiffClosesTheForegroundServerAfterPresentationCancellation(t *testing.T) {
	baseline := t.TempDir()
	target := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{Interaction: terminal.Automation},
		Output:       &cancelAfterWrite{Writer: &output, Cancel: cancel},
	})

	err := runDiff(&Options{
		Context: ctx,
		Input: Input{
			BaselineDirectory: baseline,
			TargetDirectory:   target,
			Port:              0,
		},
		Terminal: experience,
		NetworkInterfaces: func() ([]NetworkInterface, error) {
			return nil, nil
		},
	})
	if err != nil || !strings.Contains(output.String(), "Directory diff: http://127.0.0.1:") {
		t.Fatalf("runDiff() = (%v, %q)", err, output.String())
	}
}

func TestRunDiffRichServiceBoundaryKeepsLifecycleLogOutsideConsole(t *testing.T) {
	baseline := t.TempDir()
	target := t.TempDir()
	resolvedBaseline, err := filepath.EvalSymlinks(baseline)
	if err != nil {
		t.Fatalf("resolve baseline: %v", err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("resolve target: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var stdout, stderr bytes.Buffer
	experience := terminal.NewExperience(terminal.ExperienceOptions{
		Capabilities: terminal.Capabilities{
			Interaction: terminal.RichInteractive,
			Stdout:      terminal.StreamCapability{Terminal: true, Color: true},
			Stderr:      terminal.StreamCapability{Terminal: true, Color: true},
		},
		Output:      &cancelAfterWrite{Writer: &stdout, Cancel: cancel},
		Diagnostics: &stderr,
	})
	logRuntime := logging.NewRuntime(logging.Options{
		Writer: experience.DiagnosticWriter(),
		Format: logging.JSONFormat,
		Color:  true,
	})

	err = runDiff(&Options{
		Context: ctx,
		Input: Input{
			BaselineDirectory: baseline,
			TargetDirectory:   target,
			Port:              0,
		},
		Terminal: experience,
		NetworkInterfaces: func() ([]NetworkInterface, error) {
			return nil, nil
		},
		Logger: logRuntime.Logger("diff"),
	})
	if err != nil {
		t.Fatalf("runDiff() error = %v", err)
	}
	if got := terminaltest.StripANSI(stdout.String()); !strings.Contains(got, "Directory diff: http://127.0.0.1:") || !strings.Contains(got, "Baseline: "+resolvedBaseline) || !strings.Contains(got, "Target:   "+resolvedTarget) {
		t.Fatalf("diff startup result = %q", got)
	}
	for _, sequence := range []string{"\x1b[?1049h", "\x1b[?1049l", "\x1b[?1047h", "\x1b[?1047l", "\x1b[?47h", "\x1b[?47l"} {
		if strings.Contains(stdout.String(), sequence) || strings.Contains(stderr.String(), sequence) {
			t.Fatalf("diff service boundary entered AltScreen with %q: stdout=%q stderr=%q", sequence, stdout.String(), stderr.String())
		}
	}
	if terminaltest.ContainsTerminalControl(stderr.Bytes()) {
		t.Fatalf("diff NDJSON Lifecycle Log contains terminal control: %q", stderr.String())
	}

	records := decodeLifecycleTestRecords(t, stderr.String())
	messages := lifecycleTestMessages(records)
	if len(messages) < 6 {
		t.Fatalf("diff Lifecycle Log messages = %#v", messages)
	}
	if want := []string{"Directory diff started", "Diff endpoints available", "Comparison workspace configured", "Initial comparison refresh started"}; !equalStrings(messages[:len(want)], want) {
		t.Fatalf("diff Lifecycle Log startup = %#v, want %#v", messages[:len(want)], want)
	}
	if messages[len(messages)-2] != "Directory diff stopping" || messages[len(messages)-1] != "Directory diff stopped" {
		t.Fatalf("diff Lifecycle Log shutdown = %#v", messages[len(messages)-2:])
	}
	for _, record := range records {
		if record.Scope != "diff" {
			t.Fatalf("diff Lifecycle Log scope = %q, want diff", record.Scope)
		}
	}
}

type cancelAfterWrite struct {
	io.Writer
	Cancel context.CancelFunc
	once   sync.Once
}

func (writer *cancelAfterWrite) Write(contents []byte) (int, error) {
	written, err := writer.Writer.Write(contents)
	writer.once.Do(writer.Cancel)
	return written, err
}
