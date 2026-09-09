package fs

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/logging"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalFSPresentationPreservesPlainAndAutomationLifecycle(t *testing.T) {
	startup := Startup{
		URLs: []StartupURL{
			{Label: "Local", URL: "http://localhost:43123"},
			{Label: "Network", URL: "http://192.168.1.50:43123"},
		},
		Directory:         "/workspace",
		BindingAddress:    "0.0.0.0",
		Port:              43123,
		ManagementEnabled: true,
		ChunkedUploads:    true,
		SafeHTML:          false,
		Authentication:    true,
		SessionDirectory:  "/workspace/.ycy/sessions",
	}
	want := "File Browser\n" +
		"Local: http://localhost:43123\n" +
		"Network: http://192.168.1.50:43123\n" +
		"Directory: /workspace\n" +
		"Bind: 0.0.0.0:43123\n" +
		"Management: true\n" +
		"Chunked uploads: true\n" +
		"HTML execution: true\n" +
		"Authentication: true\n" +
		"Session storage: /workspace/.ycy/sessions\n" +
		"File Browser stopped.\n"
	for _, session := range []terminalexperience.Capabilities{
		{Interaction: terminalexperience.PlainInteractive},
		{Interaction: terminalexperience.Automation},
	} {
		var output, diagnostics bytes.Buffer
		experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: session, Output: &output, Diagnostics: &diagnostics})
		run := experience.Open(context.Background())
		if err := run.ResultCheckpoint("fs-startup", terminalFSStartupDocument(startup)); err != nil {
			t.Fatalf("%v startup Present() error = %v", session.Interaction, err)
		}
		if err := run.ResultCheckpoint("fs-stopped", terminalFSStoppedDocument()); err != nil {
			t.Fatalf("%v stopped Present() error = %v", session.Interaction, err)
		}
		if err := run.Close(); err != nil {
			t.Fatalf("%v Close() error = %v", session.Interaction, err)
		}
		if output.String() != want {
			t.Fatalf("%v presentation = %q, want %q", session.Interaction, output.String(), want)
		}
		if terminaltest.ContainsTerminalControl(output.Bytes()) {
			t.Fatalf("%v lifecycle contains terminal control: %q", session.Interaction, output.String())
		}
		if diagnostics.Len() != 0 {
			t.Fatalf("%v lifecycle wrote stderr: %q", session.Interaction, diagnostics.String())
		}
	}
}

func TestTerminalFSPresentationUsesRichSemanticRoles(t *testing.T) {
	startup := Startup{
		URLs: []StartupURL{
			{Label: "Local", URL: "http://localhost:43123"},
			{Label: "Network", URL: "http://192.168.1.50:43123"},
		},
		Directory:         "/workspace",
		BindingAddress:    "0.0.0.0",
		Port:              43123,
		ManagementEnabled: true,
		ChunkedUploads:    true,
		Authentication:    true,
		SessionDirectory:  "/workspace/.ycy/sessions",
	}
	want := []terminalexperience.VisualRole{
		terminalexperience.VisualRoleActive,
		terminalexperience.VisualRoleActive,
		terminalexperience.VisualRoleActive,
		terminalexperience.VisualRoleMuted,
		terminalexperience.VisualRoleMuted,
		terminalexperience.VisualRoleMuted,
		terminalexperience.VisualRoleMuted,
		terminalexperience.VisualRoleMuted,
		terminalexperience.VisualRoleMuted,
		terminalexperience.VisualRoleMuted,
	}
	document := terminalFSStartupDocument(startup)
	if len(document.Blocks) != len(want) {
		t.Fatalf("document = %#v", document)
	}
	for index, role := range want {
		if document.Blocks[index].Role != role {
			t.Fatalf("block %d role = %v, want %v", index, document.Blocks[index].Role, role)
		}
	}
	stopped := terminalFSStoppedDocument()
	if len(stopped.Blocks) != 1 || stopped.Blocks[0].Role != terminalexperience.VisualRoleSuccess {
		t.Fatalf("stopped document = %#v", stopped)
	}
}

func TestTerminalFSRichServiceCheckpointsRemainOutsideAltScreen(t *testing.T) {
	var output bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: true},
		},
		Output: &output,
	})
	run := experience.Open(context.Background())
	startup := Startup{
		URLs:           []StartupURL{{Label: "Local", URL: "http://localhost:43124"}},
		Directory:      "/workspace",
		BindingAddress: "127.0.0.1",
		Port:           43124,
	}
	if err := run.ResultCheckpoint("fs-startup", terminalFSStartupDocument(startup)); err != nil {
		t.Fatalf("Rich startup checkpoint = %v", err)
	}
	if err := run.ResultCheckpoint("fs-stopped", terminalFSStoppedDocument()); err != nil {
		t.Fatalf("Rich stopped checkpoint = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if got := terminaltest.StripANSI(output.String()); !strings.Contains(got, "File Browser") || !strings.Contains(got, "File Browser stopped.") {
		t.Fatalf("Rich service checkpoints = %q", got)
	}
	for _, sequence := range []string{"\x1b[?1049h", "\x1b[?1049l", "\x1b[?1047h", "\x1b[?1047l", "\x1b[?47h", "\x1b[?47l"} {
		if strings.Contains(output.String(), sequence) {
			t.Fatalf("Rich service checkpoints entered AltScreen with %q: %q", sequence, output.String())
		}
	}
}

func TestRunFSRichServiceBoundaryKeepsLifecycleLogOutsideConsole(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &cancelAfterFirstFSWrite{cancel: cancel}
	var diagnostics bytes.Buffer
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{
			Interaction: terminalexperience.RichInteractive,
			Stdout:      terminalexperience.StreamCapability{Terminal: true, Color: true},
			Stderr:      terminalexperience.StreamCapability{Terminal: true, Color: true},
		},
		Output:      output,
		Diagnostics: &diagnostics,
	})
	logRuntime := logging.NewRuntime(logging.Options{
		Writer: experience.DiagnosticWriter(),
		Format: logging.JSONFormat,
		Color:  true,
	})

	err := runFS(&Options{
		Context:  ctx,
		Input:    Input{Directory: t.TempDir(), Address: "127.0.0.1", Port: 0},
		Terminal: experience,
		NetworkInterfaces: func() ([]NetworkInterface, error) {
			return nil, nil
		},
		Logger: logRuntime.Logger("fs"),
	})
	if err != nil {
		t.Fatalf("runFS() error = %v", err)
	}
	if output.writes != 2 {
		t.Fatalf("checkpoint writes = %d, want 2", output.writes)
	}
	if got := terminaltest.StripANSI(output.output.String()); !strings.Contains(got, "File Browser") || !strings.Contains(got, "File Browser stopped.") {
		t.Fatalf("FS service checkpoints = %q", got)
	}
	for _, sequence := range []string{"\x1b[?1049h", "\x1b[?1049l", "\x1b[?1047h", "\x1b[?1047l", "\x1b[?47h", "\x1b[?47l"} {
		if strings.Contains(output.output.String(), sequence) || strings.Contains(diagnostics.String(), sequence) {
			t.Fatalf("FS service boundary entered AltScreen with %q: stdout=%q stderr=%q", sequence, output.output.String(), diagnostics.String())
		}
	}
	if terminaltest.ContainsTerminalControl(diagnostics.Bytes()) {
		t.Fatalf("FS NDJSON Lifecycle Log contains terminal control: %q", diagnostics.String())
	}

	records := decodeFSLifecycleRecords(t, diagnostics.String())
	messages := fsLifecycleMessages(records)
	if len(messages) < 6 {
		t.Fatalf("FS Lifecycle Log messages = %#v", messages)
	}
	if want := []string{"File Browser started", "Browse root configured", "File Browser capabilities configured", "File Browser authentication configured"}; !equalStrings(messages[:len(want)], want) {
		t.Fatalf("FS Lifecycle Log startup = %#v, want %#v", messages[:len(want)], want)
	}
	if messages[len(messages)-2] != "File Browser stopping" || messages[len(messages)-1] != "File Browser stopped" {
		t.Fatalf("FS Lifecycle Log shutdown = %#v", messages[len(messages)-2:])
	}
	for _, record := range records {
		if record.Scope != "fs" {
			t.Fatalf("FS Lifecycle Log scope = %q, want fs", record.Scope)
		}
	}
}

func TestRunFSClosesTheOperationWhenStartupPresentationFails(t *testing.T) {
	output := &failingFSWriter{}
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       output,
	})
	err := runFS(&Options{
		Context:  context.Background(),
		Input:    Input{Directory: t.TempDir(), Address: "127.0.0.1", Port: 0},
		Terminal: experience,
		NetworkInterfaces: func() ([]NetworkInterface, error) {
			return nil, nil
		},
	})
	if !errors.Is(err, errFSStartupPresentation) {
		t.Fatalf("runFS() error = %v, want startup presentation error", err)
	}
	if strings.Contains(output.String(), "File Browser stopped.") {
		t.Fatalf("failed startup wrote a stopped result: %q", output.String())
	}
	endpoint := ""
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.HasPrefix(line, "Local: ") {
			endpoint = strings.TrimPrefix(line, "Local: ")
			break
		}
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" {
		t.Fatalf("startup endpoint = %q, parse error = %v", endpoint, err)
	}
	listener, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		t.Fatalf("startup listener remains bound at %s: %v", parsed.Host, err)
	}
	_ = listener.Close()
}

func TestRunFSReturnsFinalCheckpointFailureWithoutLifecycleFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &cancelAfterFirstFSWrite{cancel: cancel, err: errFSStoppedPresentation}
	var lifecycleOutput bytes.Buffer
	logRuntime := logging.NewRuntime(logging.Options{Writer: &lifecycleOutput, Format: logging.JSONFormat})
	experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{
		Capabilities: terminalexperience.Capabilities{Interaction: terminalexperience.Automation},
		Output:       output,
	})

	err := runFS(&Options{
		Context:  ctx,
		Input:    Input{Directory: t.TempDir(), Address: "127.0.0.1", Port: 0},
		Terminal: experience,
		NetworkInterfaces: func() ([]NetworkInterface, error) {
			return nil, nil
		},
		Logger: logRuntime.Logger("fs"),
	})
	if !errors.Is(err, errFSStoppedPresentation) {
		t.Fatalf("runFS() error = %v, want stopped checkpoint error", err)
	}
	if output.writes != 2 {
		t.Fatalf("checkpoint writes = %d, want 2", output.writes)
	}
	records := decodeFSLifecycleRecords(t, lifecycleOutput.String())
	if got := fsLifecycleMessages(records); len(got) < 2 || got[len(got)-2] != "File Browser stopping" || got[len(got)-1] != "File Browser stopped" || countLifecycleMessage(records, "File Browser failed") != 0 {
		t.Fatalf("lifecycle records = %#v", got)
	}
}

var errFSStartupPresentation = errors.New("FS startup presentation failed")
var errFSStoppedPresentation = errors.New("FS stopped presentation failed")

type failingFSWriter struct {
	output bytes.Buffer
}

func (writer *failingFSWriter) Write(value []byte) (int, error) {
	_, _ = writer.output.Write(value)
	return 0, errFSStartupPresentation
}

func (writer *failingFSWriter) String() string {
	return writer.output.String()
}

type cancelAfterFirstFSWrite struct {
	output bytes.Buffer
	cancel context.CancelFunc
	err    error
	writes int
}

func (writer *cancelAfterFirstFSWrite) Write(value []byte) (int, error) {
	writer.writes++
	_, _ = writer.output.Write(value)
	if writer.writes == 1 {
		writer.cancel()
		return len(value), nil
	}
	if writer.err == nil {
		return len(value), nil
	}
	return 0, writer.err
}
