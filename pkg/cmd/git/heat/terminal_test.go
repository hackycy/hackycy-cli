package heat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/internal/terminaltest"
)

func TestTerminalGitHeatPresentationPreservesPlainAndAutomationResults(t *testing.T) {
	report := terminalGitHeatTestReport()
	for _, testCase := range []struct {
		name   string
		result Result
		want   string
	}{
		{
			name:   "report",
			result: Result{Report: report},
			want:   "HACKYCY CLI\nrepo | last 1 commits | 1 file\n#\tChanged at\tM A D R C\tFile\n1\t2024-01-01 00:00:00\t- - - - -\tfile.txt\nLegend: latest, earliest, M modified, A added, D deleted, R renamed, C copied\n",
		},
		{
			name:   "empty",
			result: Result{Report: Report{Target: TargetFiles}},
			want:   "No changed files found in the selected range.\n",
		},
	} {
		for _, session := range []terminalexperience.Capabilities{
			{Interaction: terminalexperience.PlainInteractive},
			{Interaction: terminalexperience.Automation},
		} {
			var output bytes.Buffer
			experience := terminalexperience.NewExperience(terminalexperience.ExperienceOptions{Capabilities: session, Output: &output})
			run := experience.Open(context.Background())
			if err := run.Result(terminalGitHeatDocument(testCase.result)); err != nil {
				t.Fatalf("%s Present() error = %v", testCase.name, err)
			}
			if err := run.Close(); err != nil {
				t.Fatalf("%s Close() error = %v", testCase.name, err)
			}
			if got := output.String(); got != testCase.want {
				t.Fatalf("%s %v output = %q, want %q", testCase.name, session.Interaction, got, testCase.want)
			}
			if terminaltest.ContainsTerminalControl(output.Bytes()) {
				t.Fatalf("%s %v output contains terminal control: %q", testCase.name, session.Interaction, output.String())
			}
		}
	}
}

func TestTerminalGitHeatPresentationUsesRichSemanticRoles(t *testing.T) {
	result := Result{Report: terminalGitHeatTestReport(), Now: time.Time{}}
	document := terminalGitHeatDocument(result)
	want := []terminalexperience.VisualRole{
		terminalexperience.VisualRoleTitle,
		terminalexperience.VisualRoleActive,
		terminalexperience.VisualRoleMuted,
		terminalexperience.VisualRoleActive,
		terminalexperience.VisualRoleMuted,
	}
	if len(document.Blocks) != len(want) {
		t.Fatalf("blocks = %#v", document.Blocks)
	}
	for index, role := range want {
		if document.Blocks[index].Role != role {
			t.Fatalf("block %d role = %v, want %v", index, document.Blocks[index].Role, role)
		}
	}
}

func TestGitHeatFinishRequestUsesOnlySafeOutcomeSummary(t *testing.T) {
	result := Result{Report: Report{
		RepositoryName: "/private/workspace",
		RangeLabel:     "last 20 commits",
		Target:         TargetFiles,
		Query:          "secret-query",
		CommitCount:    1,
		Files:          []PathHeat{{Path: "secret/path.txt"}},
	}}
	succeeded := terminalGitHeatFinishRequest(terminalexperience.Succeeded, result)
	if got, want := terminalexperience.RenderPlain(succeeded.Summary), "Ranked 1 files from last 20 commits\n"; got != want {
		t.Fatalf("success Finish summary = %q, want %q", got, want)
	}
	for _, forbidden := range []string{"/private", "secret-query", "secret/path"} {
		if strings.Contains(terminalexperience.RenderPlain(succeeded.Summary), forbidden) {
			t.Fatalf("success Finish summary leaked %q: %q", forbidden, terminalexperience.RenderPlain(succeeded.Summary))
		}
	}
	failed := terminalGitHeatFinishRequest(terminalexperience.Failed, Result{})
	if got, want := terminalexperience.RenderPlain(failed.Summary), "Unable to complete repository heat\n"; got != want {
		t.Fatalf("failed Finish summary = %q, want %q", got, want)
	}
	cancelled := terminalGitHeatFinishRequest(terminalexperience.Cancelled, Result{})
	if got, want := terminalexperience.RenderPlain(cancelled.Summary), "Repository heat cancelled\n"; got != want {
		t.Fatalf("cancelled Finish summary = %q, want %q", got, want)
	}
}

func TestTerminalGitHeatRichWideLayoutUsesStableColumns(t *testing.T) {
	report := Report{
		RepositoryName: "hackycy-cli",
		RangeLabel:     "last 20 commits",
		Target:         TargetDirectories,
		Sort:           SortPath,
		Query:          "terminal",
		CommitCount:    20,
		Files: []PathHeat{{
			Path:           "internal/terminal",
			ChangedAt:      "2026-08-31 16:49:24",
			ChangedAtEpoch: 1,
			Counts:         Counts{Modified: 1, Total: 1},
		}, {
			Path:           ".scratch/terminal-experience-modernization",
			ChangedAt:      "2026-09-02 19:54:53",
			ChangedAtEpoch: 2,
			Counts:         Counts{Modified: 1, Added: 1, Total: 2},
		}},
		Directories: []PathHeat{{
			Path:           "internal/terminal",
			ChangedAt:      "2026-08-31 16:49:24",
			ChangedAtEpoch: 1,
			Counts:         Counts{Modified: 1, Total: 1},
		}, {
			Path:           ".scratch/terminal-experience-modernization",
			ChangedAt:      "2026-09-02 19:54:53",
			ChangedAtEpoch: 2,
			Counts:         Counts{Modified: 1, Added: 1, Total: 2},
		}},
	}
	result := Result{Report: report}
	document := terminalGitHeatRichDocumentForWidth(result, 120)
	var output bytes.Buffer
	if err := terminalexperience.WriteRich(&output, document, terminalexperience.RichOptions{Width: 120}); err != nil {
		t.Fatalf("WriteRich() error = %v", err)
	}
	text := strings.TrimSuffix(output.String(), "\n")
	if strings.Contains(text, "\t") {
		t.Fatalf("wide Rich output contains a tab: %q", text)
	}
	lines := strings.Split(text, "\n")
	header := ""
	rows := make([]string, 0, 2)
	for _, line := range lines {
		if strings.Contains(line, "Changed at") && strings.Contains(line, "M A D R C") {
			header = line
		}
		plainLine := strings.NewReplacer("⟦", "", "⟧", "").Replace(line)
		if strings.Contains(plainLine, "internal/terminal") || strings.Contains(plainLine, ".scratch/terminal-experience-modernization") {
			rows = append(rows, line)
		}
	}
	if header == "" || len(rows) != 2 {
		t.Fatalf("wide Rich output lost table rows: %q", text)
	}
	changedAt := strings.Index(header, "Changed at")
	kinds := strings.Index(header, "M A D R C")
	path := strings.Index(header, "Directory")
	if changedAt < 0 || kinds < 0 || path < 0 {
		t.Fatalf("wide Rich header = %q", header)
	}
	for index, row := range rows {
		dateIndex := strings.Index(row, "2026-")
		if dateIndex < 0 || heatDisplayWidth(row[:dateIndex]) != heatDisplayWidth(header[:changedAt]) {
			t.Fatalf("row %d Changed at column drifted: %q", index, row)
		}
		kindIndex := strings.Index(row[dateIndex:], "M")
		if kindIndex < 0 || heatDisplayWidth(row[:dateIndex+kindIndex])-heatDisplayWidth(row[:dateIndex]) != heatDisplayWidth(header[changedAt:kinds]) {
			t.Fatalf("row %d change-kind column drifted: %q", index, row)
		}
	}
	if !strings.Contains(text, "▲ latest") || !strings.Contains(text, "⟦terminal⟧") {
		t.Fatalf("wide Rich output lost latest/query emphasis: %q", text)
	}
}

func TestTerminalGitHeatRichNarrowLayoutUsesLabeledRecords(t *testing.T) {
	longPath := "pkg/" + strings.Repeat("terminal-experience/", 8) + "modernization.go"
	report := Report{
		RepositoryName: "hackycy-cli",
		RangeLabel:     "last 20 commits",
		Target:         TargetDirectories,
		Query:          "terminal",
		CommitCount:    20,
		Directories: []PathHeat{{
			Path:           longPath,
			ChangedAt:      "2026-09-02 19:54:53",
			ChangedAtEpoch: 2,
			Counts:         Counts{Modified: 1, Total: 1},
		}},
		Files: []PathHeat{{
			Path:           longPath,
			ChangedAt:      "2026-09-02 19:54:53",
			ChangedAtEpoch: 2,
			Counts:         Counts{Modified: 1, Total: 1},
		}},
	}
	result := Result{Report: report}
	document := terminalGitHeatRichDocumentForWidth(result, 40)
	var output bytes.Buffer
	if err := terminalexperience.WriteRich(&output, document, terminalexperience.RichOptions{Width: 40}); err != nil {
		t.Fatalf("WriteRich() error = %v", err)
	}
	text := strings.TrimSuffix(output.String(), "\n")
	if strings.Contains(text, "\t") {
		t.Fatalf("narrow Rich output contains a tab: %q", text)
	}
	for _, expected := range []string{"Changed at", "Changes", "Directory:", "⟦terminal⟧", "pkg/", "modernization.go"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("narrow Rich output missing %q: %q", expected, text)
		}
	}
	compact := strings.NewReplacer("⟦", "", "⟧", "", "\n", "", " ", "").Replace(text)
	if !strings.Contains(compact, longPath) {
		t.Fatalf("narrow Rich output truncated path: %q", text)
	}
	for _, line := range strings.Split(text, "\n") {
		if len([]rune(line)) > 40 {
			t.Fatalf("narrow Rich line exceeds width: %d: %q", len([]rune(line)), line)
		}
	}
}

func TestTerminalGitHeatRichProjectionEscapesControlsAndPreservesLongPaths(t *testing.T) {
	longSuffix := strings.Repeat("segment/", 100) + "tail.go"
	path := "src\napi\t\x1b[31m/" + longSuffix
	row := PathHeat{Path: path, ChangedAt: "2024-01-01 00:00:00"}
	report := Report{Target: TargetFiles, Query: "api"}
	text := terminalGitHeatRichRow(1, "", row, report, time.Time{})
	if strings.ContainsAny(text, "\r\t\x1b") {
		t.Fatalf("rich row contains raw control: %q", text)
	}
	for _, expected := range []string{`\n`, `\t`, `\x1B`, "tail.go", "⟦api⟧"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("rich row = %q, missing %q", text, expected)
		}
	}
	if len([]rune(text)) < 700 {
		t.Fatalf("rich row appears truncated: length=%d", len([]rune(text)))
	}
}

func TestTerminalGitHeatRichProjectionMapsUnicodeAndControlQueryOffsets(t *testing.T) {
	path := "中API\napiary"
	row := PathHeat{Path: path, ChangedAt: "known"}
	text := terminalGitHeatRichRow(1, "", row, Report{Target: TargetFiles, Query: "api"}, time.Time{})
	for _, expected := range []string{"⟦API⟧", `\n`, "⟦api⟧ary"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("rich row = %q, missing %q", text, expected)
		}
	}
}

func TestTerminalGitHeatRichProjectionRendersInvalidUTF8AsVisibleBytes(t *testing.T) {
	path := string([]byte{'a', 0xff, 'b'})
	text := terminalGitHeatRichRow(1, "", PathHeat{Path: path}, Report{Target: TargetFiles}, time.Time{})
	if !strings.Contains(text, `a\xFFb`) {
		t.Fatalf("rich row = %q, want visible invalid byte", text)
	}
}

func TestTerminalGitHeatRichSummaryProjectsUnsafeRepositoryNames(t *testing.T) {
	report := terminalGitHeatTestReport()
	report.RepositoryName = "repo\n\x1b[31m"
	document := terminalGitHeatRichDocument(Result{Report: report})
	for _, block := range document.Blocks {
		if strings.ContainsAny(block.Text, "\r\n\x1b") {
			t.Fatalf("Rich block contains raw control: %#v", block)
		}
	}
	joined := make([]string, 0, len(document.Blocks))
	for _, block := range document.Blocks {
		joined = append(joined, block.Text)
	}
	text := strings.Join(joined, "\n")
	if !strings.Contains(text, `repo\n\x1B[31m`) {
		t.Fatalf("Rich summary did not preserve visible projection: %q", text)
	}
}

func TestIsHeatCancellationPreservesJoinedOperationFailures(t *testing.T) {
	if isHeatCancellation(errors.Join(context.Canceled, errors.New("git failed"))) {
		t.Fatal("joined operation failure was classified as cancellation")
	}
	if isHeatCancellation(fmt.Errorf("wrapped: %w", errors.Join(context.Canceled, errors.New("git failed")))) {
		t.Fatal("nested joined operation failure was classified as cancellation")
	}
	if !isHeatCancellation(errors.Join(context.Canceled, context.DeadlineExceeded)) {
		t.Fatal("all-cancellation error was not classified as cancellation")
	}
}

func TestGitHeatConsoleDescriptorContainsOnlyNormalizedSafeContext(t *testing.T) {
	options, err := NormalizeInput(Input{Days: integerPointer(7), Target: TargetFiles, Sort: SortCount, RelativeTime: true, Query: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	want := terminalexperience.ConsoleDescriptor{
		Command: "YCY / git heat",
		Target:  "file heat",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "range", Value: "last 7 days"},
			{Label: "sort", Value: "count"},
			{Label: "time", Value: "relative time"},
		},
	}
	got := gitHeatConsoleDescriptor(options)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Console descriptor = %#v, want %#v", got, want)
	}
	for _, field := range got.Metadata {
		if strings.Contains(field.Value, "secret") || strings.Contains(field.Value, "/") {
			t.Fatalf("unsafe Console metadata = %#v", got.Metadata)
		}
	}
}

func TestTerminalGitHeatRunNormalizedReportsOrderedPhasesAndCancellation(t *testing.T) {
	now := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	runner := &scriptedGitRunner{outputs: []GitOutput{
		{Stdout: []byte("/repo\n")},
		{Stdout: []byte("\x00" + heatCommitMarker + "abc\x1f1704067200\x1f2024-01-01 00:00:00 +0000\x00M\x00file.go\x00")},
	}}
	module, err := New(Dependencies{Git: runner, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	options, err := NormalizeInput(Input{Target: TargetFiles})
	if err != nil {
		t.Fatal(err)
	}
	var updates []workPhaseUpdate
	result, err := module.runNormalized(context.Background(), options, func(update workPhaseUpdate) {
		updates = append(updates, update)
	})
	if err != nil || result.Report.RepositoryName != "repo" {
		t.Fatalf("runNormalized() = %#v, %v", result, err)
	}
	want := []string{heatLocatePhaseID, heatLocatePhaseID, heatReadPhaseID, heatReadPhaseID, heatRankPhaseID, heatRankPhaseID}
	if got := phaseIDs(updates); !reflect.DeepEqual(got, want) {
		t.Fatalf("phase IDs = %#v, want %#v", got, want)
	}
	if updates[0].State != workPhaseActive || updates[1].State != workPhaseCompleted || updates[5].State != workPhaseCompleted {
		t.Fatalf("phase states = %#v", updates)
	}

	failure := errors.New("read failed")
	failing, err := New(Dependencies{Git: &scriptedGitRunner{outputs: []GitOutput{{Stdout: []byte("/repo\n")}}, err: failure}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	_, err = failing.runNormalized(context.Background(), options, nil)
	if !errors.Is(err, failure) {
		t.Fatalf("failure = %v, want %v", err, failure)
	}

	joinedFailure := errors.Join(failure, context.Canceled)
	joinedModule, err := New(Dependencies{Git: &scriptedGitRunner{err: joinedFailure}, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	var joinedUpdates []workPhaseUpdate
	_, err = joinedModule.runNormalized(context.Background(), options, func(update workPhaseUpdate) {
		joinedUpdates = append(joinedUpdates, update)
	})
	if !errors.Is(err, failure) || len(joinedUpdates) != 2 || joinedUpdates[1].State != workPhaseFailed {
		t.Fatalf("joined failure = (%v), updates = %#v", err, joinedUpdates)
	}
}

func phaseIDs(updates []workPhaseUpdate) []string {
	ids := make([]string, len(updates))
	for index, update := range updates {
		ids[index] = update.ID
	}
	return ids
}

func terminalGitHeatTestReport() Report {
	return Report{
		RepositoryName: "repo",
		RangeLabel:     "last 1 commits",
		Target:         TargetFiles,
		Query:          "file",
		CommitCount:    1,
		Files: []PathHeat{{
			Path:      "file.txt",
			Counts:    Counts{Total: 1},
			ChangedAt: "2024-01-01 00:00:00",
		}},
	}
}
