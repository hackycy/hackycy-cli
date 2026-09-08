package heat

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
)

const (
	terminalGitHeatEmptyMessage = "No changed files found in the selected range."
	terminalGitHeatLegend       = "Legend: latest, earliest, M modified, A added, D deleted, R renamed, C copied"
	terminalGitHeatDefaultWidth = 80
	terminalGitHeatNarrowWidth  = 72
	terminalGitHeatColumnGap    = 2
)

func runHeat(options *Options) error {
	if options == nil || options.Terminal == nil || options.Git == nil || options.Now == nil {
		return errors.New("git heat options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	module, err := New(Dependencies{
		Git: gitRunnerAdapter{runner: options.Git},
		Now: options.Now,
	})
	if err != nil {
		return err
	}
	input := Input{
		Limit:        options.Limit,
		Days:         options.Days,
		Target:       options.Target,
		Sort:         options.Sort,
		RelativeTime: options.RelativeTime,
		Query:        options.Query,
	}
	normalized, err := NormalizeInput(input)
	if err != nil {
		return err
	}
	run, err := options.Terminal.OpenConsole(ctx, gitHeatConsoleDescriptor(normalized))
	if err != nil {
		return err
	}
	defer run.Close()
	caps := options.Terminal.Capabilities()
	var updates chan terminalexperience.OperationPhase
	var trackDone chan error
	var presentationErr error
	if caps.Interaction == terminalexperience.RichInteractive {
		updates = make(chan terminalexperience.OperationPhase, 16)
		trackDone = make(chan error, 1)
		go func() {
			trackDone <- run.Track(terminalexperience.TrackedOperation{
				ID:    "git-heat",
				Label: "Repository heat",
				Phases: []terminalexperience.PhaseDefinition{
					{ID: heatLocatePhaseID, Name: "Locate Git repository"},
					{ID: heatReadPhaseID, Name: "Read Git history"},
					{ID: heatRankPhaseID, Name: "Rank hot paths"},
				},
				Updates:       updates,
				RequestCancel: cancel,
			})
		}()
	}

	result, workErr := module.runNormalized(ctx, normalized, func(update workPhaseUpdate) {
		if caps.Interaction == terminalexperience.RichInteractive {
			updates <- terminalHeatPhase(update)
			return
		}
		if caps.Interaction != terminalexperience.PlainInteractive {
			return
		}
		var document terminalexperience.PresentationDocument
		switch update.State {
		case workPhaseActive:
			document = terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
				Role: terminalexperience.VisualRoleActive,
				Text: update.Detail + "...",
			}}}
		case workPhaseFailed:
			document = terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
				Role: terminalexperience.VisualRoleError,
				Text: "Failed: " + update.Name,
			}}}
		case workPhaseCancelled:
			document = terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
				Role: terminalexperience.VisualRoleWarning,
				Text: "Cancelled: " + update.Name,
			}}}
		default:
			return
		}
		if err := run.Notice(document); err != nil && presentationErr == nil {
			presentationErr = err
		}
	})
	if caps.Interaction == terminalexperience.RichInteractive {
		close(updates)
		if trackErr := <-trackDone; trackErr != nil {
			workErr = errors.Join(workErr, trackErr)
		}
	}
	if presentationErr != nil {
		workErr = errors.Join(workErr, presentationErr)
	}
	if workErr != nil {
		outcome := terminalexperience.Failed
		if isHeatCancellation(workErr) {
			outcome = terminalexperience.Cancelled
		}
		return errors.Join(workErr, run.Finish(terminalGitHeatFinishRequest(outcome, Result{}), nil))
	}
	document := terminalGitHeatDocument(result)
	if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
		document = terminalGitHeatRichDocumentForWidth(result, options.Width)
	}
	return run.Finish(terminalGitHeatFinishRequest(terminalexperience.Succeeded, result), &document)
}

const (
	heatLocatePhaseID = "locate-repository"
	heatReadPhaseID   = "read-git-history"
	heatRankPhaseID   = "rank-hot-paths"
)

func gitHeatConsoleDescriptor(options NormalizedInput) terminalexperience.ConsoleDescriptor {
	target := "directory heat"
	if options.Target == TargetFiles {
		target = "file heat"
	}
	timeMode := "absolute time"
	if options.RelativeTime {
		timeMode = "relative time"
	}
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / git heat",
		Target:  target,
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "range", Value: rangeLabel(options.Range)},
			{Label: "sort", Value: string(options.Sort)},
			{Label: "time", Value: timeMode},
		},
	}
}

func terminalHeatPhase(update workPhaseUpdate) terminalexperience.OperationPhase {
	state := terminalexperience.PhaseActive
	switch update.State {
	case workPhaseCompleted:
		state = terminalexperience.PhaseCompleted
	case workPhaseCancelled:
		state = terminalexperience.PhaseCancelled
	case workPhaseFailed:
		state = terminalexperience.PhaseFailed
	}
	return terminalexperience.OperationPhase{ID: update.ID, State: state, Detail: update.Detail}
}

func isHeatCancellation(err error) bool {
	if err == nil || (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) {
		return false
	}
	// errors.Join can carry a real operation failure alongside a cancellation.
	// Preserve the failure outcome in that case; only an all-cancellation tree
	// is a truthful cancellation result.
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		causes := joined.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !isHeatCancellation(cause) {
				return false
			}
		}
	} else if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		if cause := wrapped.Unwrap(); cause != nil {
			return isHeatCancellation(cause)
		}
	}
	return true
}

func terminalGitHeatDocument(result Result) terminalexperience.PresentationDocument {
	report := result.Report
	if report.IsEmpty() {
		return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleWarning,
			Text: terminalGitHeatEmptyMessage,
		}}}
	}

	rows := report.Rows()
	marks := TimeMarks(rows)
	blocks := []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleTitle,
		Text: "HACKYCY CLI",
	}, {
		Role: terminalexperience.VisualRoleActive,
		Text: terminalGitHeatSummary(report, len(rows)),
	}, {
		Role: terminalexperience.VisualRoleMuted,
		Text: terminalGitHeatHeader(report),
	}}
	for index, row := range rows {
		role := terminalexperience.VisualRolePlain
		if len(QueryMatches(row.Path, report.Query)) > 0 {
			role = terminalexperience.VisualRoleActive
		}
		blocks = append(blocks, terminalexperience.PresentationBlock{
			Role: role,
			Text: terminalGitHeatRow(index+1, marks[index], row, report.RelativeTime, result.Now),
		})
	}
	blocks = append(blocks, terminalexperience.PresentationBlock{
		Role: terminalexperience.VisualRoleMuted,
		Text: terminalGitHeatLegend,
	})
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalGitHeatRichDocument(result Result) terminalexperience.PresentationDocument {
	return terminalGitHeatRichDocumentForWidth(result, terminalGitHeatDefaultWidth)
}

func terminalGitHeatRichDocumentForWidth(result Result, width int) terminalexperience.PresentationDocument {
	if width <= 0 {
		width = terminalGitHeatDefaultWidth
	}
	report := result.Report
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / git heat"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Repository heat"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Ranked change activity for " + safeHeatText(report.RepositoryName)},
	}
	if report.IsEmpty() {
		return terminalexperience.PresentationDocument{Blocks: append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleWarning, Text: terminalGitHeatEmptyMessage},
		)}
	}
	rows := report.Rows()
	marks := TimeMarks(rows)
	if width < terminalGitHeatNarrowWidth {
		blocks = append(blocks, terminalGitHeatRichNarrowSummary(report, len(rows), width)...)
		blocks = append(blocks, terminalexperience.PresentationBlock{
			Role: terminalexperience.VisualRoleMuted,
			Text: heatWrapText("RANK / CHANGED AT / CHANGES / "+terminalGitHeatTargetLabel(report), width),
		})
		for index, row := range rows {
			role := terminalGitHeatRichRowRole(marks[index], row, report)
			blocks = append(blocks, terminalexperience.PresentationBlock{
				Role: role,
				Text: terminalGitHeatNarrowRow(index+1, marks[index], row, report, result.Now, width),
			})
		}
	} else {
		blocks = append(blocks,
			terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleActive, Text: safeHeatText(terminalGitHeatSummary(report, len(rows)))},
		)
		columns := terminalGitHeatRichColumns(report, rows, marks, result.Now)
		blocks = append(blocks, terminalexperience.PresentationBlock{
			Role: terminalexperience.VisualRoleMuted,
			Text: terminalGitHeatRichHeader(report, columns),
		})
		for index, row := range rows {
			role := terminalGitHeatRichRowRole(marks[index], row, report)
			blocks = append(blocks, terminalexperience.PresentationBlock{
				Role: role,
				Text: terminalGitHeatWideRow(index+1, marks[index], row, report, result.Now, columns, width),
			})
		}
	}
	blocks = append(blocks, terminalexperience.PresentationBlock{Role: terminalexperience.VisualRoleMuted, Text: terminalGitHeatLegend})
	return terminalexperience.PresentationDocument{Blocks: blocks}
}

func terminalHeatSummaryDocument(result Result) terminalexperience.PresentationDocument {
	report := result.Report
	count := len(report.Rows())
	if report.IsEmpty() {
		return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleWarning,
			Text: "Found 0 changed files",
		}}}
	}
	target := "files"
	if report.Target == TargetDirectories {
		target = "directories"
	}
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
		Role: terminalexperience.VisualRoleSuccess,
		Text: safeHeatText(fmt.Sprintf("Ranked %d %s from %s", count, target, report.RangeLabel)),
	}}}
}

func terminalGitHeatFinishRequest(outcome terminalexperience.FinishOutcome, result Result) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{Outcome: outcome}
	switch outcome {
	case terminalexperience.Succeeded:
		request.Summary = terminalHeatSummaryDocument(result)
	case terminalexperience.Cancelled:
		request.Summary = terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleWarning,
			Text: "Repository heat cancelled",
		}}}
	case terminalexperience.Failed:
		request.Summary = terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleError,
			Text: "Unable to complete repository heat",
		}}}
	}
	return request
}

func terminalGitHeatRichRow(rank int, mark TimeMark, row PathHeat, report Report, now time.Time) string {
	rows := []PathHeat{row}
	marks := []TimeMark{mark}
	columns := terminalGitHeatRichColumns(report, rows, marks, now)
	return terminalGitHeatWideRow(rank, mark, row, report, now, columns, terminalGitHeatDefaultWidth)
}

type terminalGitHeatColumnWidths struct {
	rank    int
	changed int
	kinds   int
	pathAt  int
}

func terminalGitHeatRichColumns(report Report, rows []PathHeat, marks []TimeMark, now time.Time) terminalGitHeatColumnWidths {
	columns := terminalGitHeatColumnWidths{
		rank:    heatDisplayWidth("#"),
		changed: heatDisplayWidth("Changed at"),
		kinds:   heatDisplayWidth("M A D R C"),
	}
	for index, row := range rows {
		if index >= len(marks) {
			break
		}
		columns.rank = max(columns.rank, heatDisplayWidth(terminalGitHeatRichRankLabel(index+1, marks[index])))
		columns.changed = max(columns.changed, heatDisplayWidth(safeHeatText(FormatChangedAt(row, report.RelativeTime, now))))
		columns.kinds = max(columns.kinds, heatDisplayWidth(terminalGitHeatKindLabels(row)))
	}
	columns.pathAt = columns.rank + terminalGitHeatColumnGap + columns.changed + terminalGitHeatColumnGap + columns.kinds + terminalGitHeatColumnGap
	return columns
}

func terminalGitHeatRichHeader(report Report, columns terminalGitHeatColumnWidths) string {
	return strings.Join([]string{
		heatPadRight("#", columns.rank),
		heatPadRight("Changed at", columns.changed),
		heatPadRight("M A D R C", columns.kinds),
		terminalGitHeatTargetLabel(report),
	}, strings.Repeat(" ", terminalGitHeatColumnGap))
}

func terminalGitHeatWideRow(rank int, mark TimeMark, row PathHeat, report Report, now time.Time, columns terminalGitHeatColumnWidths, width int) string {
	pathText := terminalGitHeatRichPath(row, report)
	prefix := strings.Join([]string{
		heatPadRight(terminalGitHeatRichRankLabel(rank, mark), columns.rank),
		heatPadRight(safeHeatText(FormatChangedAt(row, report.RelativeTime, now)), columns.changed),
		heatPadRight(terminalGitHeatKindLabels(row), columns.kinds),
	}, strings.Repeat(" ", terminalGitHeatColumnGap))
	pathAt := columns.pathAt
	prefix += strings.Repeat(" ", terminalGitHeatColumnGap)
	available := width - pathAt
	if available < 1 {
		available = 1
	}
	pathLines := heatWrapValue(pathText, available)
	lines := make([]string, 0, len(pathLines))
	lines = append(lines, prefix+pathLines[0])
	continuationIndent := strings.Repeat(" ", pathAt)
	continuationWidth := width - pathAt
	if continuationWidth < 1 {
		continuationIndent = " "
		continuationWidth = max(width-1, 1)
	}
	for _, line := range pathLines[1:] {
		chunks := heatWrapValue(line, continuationWidth)
		for _, chunk := range chunks {
			lines = append(lines, continuationIndent+chunk)
		}
	}
	return strings.Join(lines, "\n")
}

func terminalGitHeatNarrowRow(rank int, mark TimeMark, row PathHeat, report Report, now time.Time, width int) string {
	rankLabel := fmt.Sprintf("#%02d", rank)
	switch mark {
	case TimeMarkLatest:
		rankLabel += "  ▲ latest"
	case TimeMarkEarliest:
		rankLabel += "  ▼ earliest"
	}
	pathLabel := terminalGitHeatTargetLabel(report)
	lines := []string{rankLabel}
	lines = append(lines, terminalGitHeatLabeledLines("Changed at", safeHeatText(FormatChangedAt(row, report.RelativeTime, now)), width)...)
	lines = append(lines, terminalGitHeatLabeledLines("Changes", terminalGitHeatKindLabels(row), width)...)
	lines = append(lines, terminalGitHeatLabeledLines(pathLabel, terminalGitHeatRichPath(row, report), width)...)
	return strings.Join(lines, "\n")
}

func terminalGitHeatLabeledLines(label, value string, width int) []string {
	if value == "" {
		value = "-"
	}
	prefix := "  " + label + ": "
	continuation := strings.Repeat(" ", heatDisplayWidth(prefix))
	if heatDisplayWidth(prefix) >= width {
		labelLines := strings.Split(heatWrapText(strings.TrimSpace(prefix), width), "\n")
		valueLines := heatWrapValue(value, max(width-2, 1))
		lines := append([]string(nil), labelLines...)
		for _, valueLine := range valueLines {
			lines = append(lines, "  "+valueLine)
		}
		return lines
	}
	remaining := value
	lines := make([]string, 0, 2)
	first := true
	for first || remaining != "" {
		linePrefix := continuation
		if first {
			linePrefix = prefix
		}
		available := width - heatDisplayWidth(linePrefix)
		if available < 1 {
			available = 1
		}
		chunks := heatWrapValue(remaining, available)
		chunk := chunks[0]
		if chunk == "" && remaining != "" {
			_, size := utf8.DecodeRuneInString(remaining)
			chunk = remaining[:size]
		}
		lines = append(lines, linePrefix+chunk)
		remaining = remaining[len(chunk):]
		first = false
	}
	return lines
}

func terminalGitHeatRichNarrowSummary(report Report, count, width int) []terminalexperience.PresentationBlock {
	singular := "file"
	if report.Target == TargetDirectories {
		singular = "directory"
	}
	blocks := []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: heatWrapText("Repository: "+safeHeatText(report.RepositoryName), width)},
		{Role: terminalexperience.VisualRoleActive, Text: heatWrapText("Range: "+safeHeatText(report.RangeLabel), width)},
	}
	if report.ShowCommitCount() {
		blocks = append(blocks, terminalexperience.PresentationBlock{
			Role: terminalexperience.VisualRoleMuted,
			Text: heatWrapText(fmt.Sprintf("Commits: %d", report.CommitCount), width),
		})
	}
	blocks = append(blocks, terminalexperience.PresentationBlock{
		Role: terminalexperience.VisualRoleMuted,
		Text: heatWrapText("Result: "+terminalGitHeatCountLabel(count, singular), width),
	})
	return blocks
}

func terminalGitHeatRichRowRole(mark TimeMark, row PathHeat, report Report) terminalexperience.VisualRole {
	switch mark {
	case TimeMarkLatest:
		return terminalexperience.VisualRoleSuccess
	case TimeMarkEarliest:
		return terminalexperience.VisualRoleWarning
	default:
		if len(QueryMatches(row.Path, report.Query)) > 0 {
			return terminalexperience.VisualRoleActive
		}
		return terminalexperience.VisualRolePlain
	}
}

func terminalGitHeatRichRankLabel(rank int, mark TimeMark) string {
	label := strconv.Itoa(rank)
	switch mark {
	case TimeMarkLatest:
		return label + " ▲ latest"
	case TimeMarkEarliest:
		return label + " ▼ earliest"
	default:
		return label
	}
}

func terminalGitHeatRichPath(row PathHeat, report Report) string {
	pathProjection := projectHeatText(row.Path)
	pathText := pathProjection.Text
	if matches := QueryMatches(row.Path, report.Query); len(matches) > 0 {
		pathText = highlightHeatMatches(pathText, pathProjection.mapMatches(matches))
	}
	if pathText == "" {
		return "-"
	}
	return pathText
}

func terminalGitHeatTargetLabel(report Report) string {
	if report.Target == TargetDirectories {
		return "Directory"
	}
	return "File"
}

func heatDisplayWidth(value string) int {
	width := 0
	for _, character := range value {
		if character == '\n' || character == '\r' {
			continue
		}
		if unicode.Is(unicode.Mn, character) {
			continue
		}
		if heatWideRune(character) {
			width += 2
			continue
		}
		width++
	}
	return width
}

func heatWideRune(character rune) bool {
	switch {
	case character >= 0x1100 && character <= 0x115f:
		return true
	case character >= 0x2e80 && character <= 0xa4cf:
		return true
	case character >= 0xac00 && character <= 0xd7a3:
		return true
	case character >= 0xf900 && character <= 0xfaff:
		return true
	case character >= 0xfe10 && character <= 0xfe6f:
		return true
	case character >= 0xff01 && character <= 0xff60:
		return true
	case character >= 0x1f300 && character <= 0x1faff:
		return true
	default:
		return false
	}
}

func heatPadRight(value string, width int) string {
	padding := width - heatDisplayWidth(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}

func heatWrapText(value string, width int) string {
	if width <= 0 {
		return value
	}
	inputLines := strings.Split(value, "\n")
	outputLines := make([]string, 0, len(inputLines))
	for _, line := range inputLines {
		outputLines = append(outputLines, heatWrapValue(line, width)...)
	}
	return strings.Join(outputLines, "\n")
}

func heatWrapValue(value string, width int) []string {
	if value == "" {
		return []string{""}
	}
	if width <= 0 || heatDisplayWidth(value) <= width {
		return []string{value}
	}
	lines := make([]string, 0, 2)
	remaining := value
	for heatDisplayWidth(remaining) > width {
		cut := heatWrapCut(remaining, width)
		if cut <= 0 {
			_, cut = utf8.DecodeRuneInString(remaining)
		}
		lines = append(lines, remaining[:cut])
		remaining = remaining[cut:]
	}
	lines = append(lines, remaining)
	return lines
}

func heatWrapCut(value string, width int) int {
	used := 0
	cut := 0
	lastStrongBreak := 0
	lastWeakBreak := 0
	for offset, character := range value {
		characterWidth := heatDisplayWidth(string(character))
		if used+characterWidth > width {
			break
		}
		used += characterWidth
		cut = offset + utf8.RuneLen(character)
		if character == '/' || character == '\\' || unicode.IsSpace(character) {
			lastStrongBreak = cut
		} else if character == '-' {
			lastWeakBreak = cut
		}
	}
	if lastStrongBreak > 0 && lastStrongBreak < cut {
		return lastStrongBreak
	}
	if lastWeakBreak > 0 && lastWeakBreak < cut {
		return lastWeakBreak
	}
	return cut
}

func highlightHeatMatches(value string, matches []Match) string {
	if len(matches) == 0 {
		return value
	}
	var output strings.Builder
	last := 0
	for _, match := range matches {
		if match.Start < last || match.Start < 0 || match.End > len(value) || match.End <= match.Start {
			continue
		}
		output.WriteString(value[last:match.Start])
		output.WriteString("⟦")
		output.WriteString(value[match.Start:match.End])
		output.WriteString("⟧")
		last = match.End
	}
	output.WriteString(value[last:])
	return output.String()
}

func safeHeatText(value string) string {
	return projectHeatText(value).Text
}

// heatTextProjection keeps a safe display string together with byte-boundary
// mappings back to the original path. Query matching is performed on the
// original Git path so the projection must not change its semantics.
type heatTextProjection struct {
	Text    string
	Offsets []int
}

func projectHeatText(value string) heatTextProjection {
	projection := heatTextProjection{Offsets: make([]int, len(value)+1)}
	var output strings.Builder
	for offset := 0; offset < len(value); {
		start := offset
		projection.Offsets[start] = output.Len()
		character, size := utf8.DecodeRuneInString(value[offset:])
		if character == utf8.RuneError && size == 1 {
			output.WriteString(fmt.Sprintf("\\x%02X", value[offset]))
			offset++
			projection.Offsets[offset] = output.Len()
			continue
		}
		output.WriteString(escapeHeatRune(character))
		offset += size
		for index := start + 1; index < offset; index++ {
			projection.Offsets[index] = projection.Offsets[start]
		}
		projection.Offsets[offset] = output.Len()
	}
	return heatTextProjection{Text: output.String(), Offsets: projection.Offsets}
}

func escapeHeatRune(character rune) string {
	switch character {
	case '\r':
		return `\r`
	case '\n':
		return `\n`
	case '\t':
		return `\t`
	}
	if unicode.IsControl(character) {
		if character <= 0xff {
			return fmt.Sprintf("\\x%02X", character)
		}
		return fmt.Sprintf("\\u%04X", character)
	}
	return string(character)
}

func (projection heatTextProjection) mapMatches(matches []Match) []Match {
	if len(matches) == 0 || len(projection.Offsets) == 0 {
		return nil
	}
	mapped := make([]Match, 0, len(matches))
	for _, match := range matches {
		if match.Start < 0 || match.End <= match.Start || match.End >= len(projection.Offsets) {
			continue
		}
		mapped = append(mapped, Match{
			Start: projection.Offsets[match.Start],
			End:   projection.Offsets[match.End],
		})
	}
	return mapped
}

func terminalGitHeatPlainText(report Report, now time.Time) string {
	if report.IsEmpty() {
		return terminalGitHeatEmptyMessage + "\n"
	}
	return "HACKYCY CLI\n\n" + terminalGitHeatReportText(report, now)
}

func terminalGitHeatReportText(report Report, now time.Time) string {
	rows := report.Rows()
	marks := TimeMarks(rows)
	var output strings.Builder
	output.WriteString(terminalGitHeatSummary(report, len(rows)))
	output.WriteString("\n\n")
	output.WriteString(terminalGitHeatHeader(report))
	output.WriteByte('\n')
	for index, row := range rows {
		output.WriteString(terminalGitHeatRow(index+1, marks[index], row, report.RelativeTime, now))
		output.WriteByte('\n')
	}
	output.WriteString(terminalGitHeatLegend)
	output.WriteByte('\n')
	return output.String()
}

func terminalGitHeatSummary(report Report, count int) string {
	parts := []string{report.RepositoryName, report.RangeLabel}
	if report.ShowCommitCount() {
		parts = append(parts, terminalGitHeatCountLabel(report.CommitCount, "commit"))
	}
	if report.Target == TargetFiles {
		parts = append(parts, terminalGitHeatCountLabel(count, "file"))
	} else {
		parts = append(parts, terminalGitHeatCountLabel(count, "directory"))
	}
	return strings.Join(parts, " | ")
}

func terminalGitHeatCountLabel(count int, singular string) string {
	if count == 1 {
		return strconv.Itoa(count) + " " + singular
	}
	if singular == "directory" {
		return strconv.Itoa(count) + " directories"
	}
	return strconv.Itoa(count) + " " + singular + "s"
}

func terminalGitHeatHeader(report Report) string {
	if report.Target == TargetFiles {
		return "#\tChanged at\tM A D R C\tFile"
	}
	return "#\tChanged at\tM A D R C\tDirectory"
}

func terminalGitHeatRow(rank int, mark TimeMark, row PathHeat, relative bool, now time.Time) string {
	return fmt.Sprintf("%s\t%s\t%s\t%s",
		terminalGitHeatRankLabel(rank, mark),
		FormatChangedAt(row, relative, now),
		terminalGitHeatKindLabels(row),
		row.Path,
	)
}

func terminalGitHeatRankLabel(rank int, mark TimeMark) string {
	if mark == "" {
		return strconv.Itoa(rank)
	}
	return strconv.Itoa(rank) + " (" + string(mark) + ")"
}

func terminalGitHeatKindLabels(row PathHeat) string {
	return strings.Join([]string{
		terminalGitHeatKindLabel("M", row.Modified),
		terminalGitHeatKindLabel("A", row.Added),
		terminalGitHeatKindLabel("D", row.Deleted),
		terminalGitHeatKindLabel("R", row.Renamed),
		terminalGitHeatKindLabel("C", row.Copied),
	}, " ")
}

func terminalGitHeatKindLabel(label string, count int) string {
	if count == 0 {
		return "-"
	}
	return label
}
