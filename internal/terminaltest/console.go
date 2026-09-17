package terminaltest

import (
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/term"
)

// ReviewConsole reads a finished Rich console by scrolling its history before
// dismissing it. Tests must call it after completing all business interactions,
// before waiting for a result/child-process marker. It never submits a form.
func ReviewConsole(t testing.TB, process *PTYProcess, output interface{ String() string }) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		text := output.String()
		if strings.Contains(text, "\x1b[?1049l") {
			return // A non-Outcome run has already handed back the terminal.
		}
		plain := strings.Join(strings.Fields(StripANSI(text)), " ")
		// Differential rendering may reuse the already drawn "enter" prefix.
		if strings.Contains(plain, "close") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the console outcome")
		}
		time.Sleep(5 * time.Millisecond)
	}
	write := func(value string) {
		t.Helper()
		if _, err := io.WriteString(process.Terminal(), value); err != nil {
			t.Fatalf("browse console: %v", err)
		}
		// Give the renderer one frame for each scroll position; sending the
		// whole sequence at once would coalesce away the rows under test.
		time.Sleep(25 * time.Millisecond)
	}
	width, height, err := term.GetSize(int(process.Terminal().Fd()))
	if err != nil {
		t.Fatalf("read console size: %v", err)
	}
	redraw := func() {
		t.Helper()
		// Force a full frame so substring assertions do not mistake a reused
		// cell in the differential output for missing semantic content.
		if err := process.Resize(uint16(width+1), uint16(height)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
		if err := process.Resize(uint16(width), uint16(height)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	write("\x1b[H")
	redraw()
	lines := 1
	for _, match := range regexp.MustCompile(`/(\d+) [↓─]`).FindAllStringSubmatch(StripANSI(output.String()), -1) {
		n, err := strconv.Atoi(match[1])
		if err == nil {
			lines = max(lines, n)
		}
	}
	step := max(height/3, 1)
	for offset := 0; offset < lines; offset += step {
		write(strings.Repeat("\x1b[B", step))
		redraw()
	}
	write("\r")
}
