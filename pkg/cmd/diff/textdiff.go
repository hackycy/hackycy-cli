package diff

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultTextDiffContext = 3
	maxTextDiffContext     = 20
	maxConcurrentTextDiffs = 2
	maxTextDiffLines       = 5_000
	maxTextDiffCells       = 1_000_000
	maxTextDiffOutputBytes = 256 * 1024
)

var (
	errInvalidTextDiffContext = errors.New("contextLines must be an integer between 0 and 20")
	errTextDiffComplexity     = errors.New("text difference exceeds the work limit")
)

func (snapshot *Snapshot) TextDiff(parent context.Context, entryID int, options *TextDiffOptions) (TextDiffResult, error) {
	entry := snapshot.entry(entryID)
	if entry == nil || entry.Status == StatusIssue || entry.Status == StatusUnchanged {
		return TextDiffResult{}, errComparisonEntryNotFound
	}
	contextLines, err := textDiffContextLines(options)
	if err != nil {
		return TextDiffResult{}, err
	}
	if entry.baseline != nil && entry.target != nil && entry.baseline.state.Kind != entry.target.state.Kind {
		return unavailableTextDiff(entry, TextDiffMixedEntryKinds), nil
	}
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return TextDiffResult{}, err
	}
	if !snapshot.acquireTextDiffSlot() {
		return unavailableTextDiff(entry, TextDiffServerBusy), nil
	}
	defer snapshot.releaseTextDiffSlot()

	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	baseline := loadTextContent(entry.baseline, snapshot.summary.BaselineDirectory, entry.Path, false)
	target := loadTextContent(entry.target, snapshot.summary.TargetDirectory, entry.Path, false)
	if err := ctx.Err(); err != nil {
		return textDiffContextFailure(entry, err)
	}
	if baseline.Status == ContentBinary || target.Status == ContentBinary {
		return unavailableTextDiff(entry, TextDiffNonText), nil
	}
	if textContentIsLarge(baseline) || textContentIsLarge(target) {
		result := unavailableTextDiff(entry, TextDiffSourceTooLarge)
		addTextDiffContentMetadata(&result, SideBaseline, baseline)
		addTextDiffContentMetadata(&result, SideTarget, target)
		return result, nil
	}
	if baseline.Status == ContentStale || target.Status == ContentStale {
		return unavailableTextDiff(entry, TextDiffStale), nil
	}
	if !textContentIsReadable(baseline) || !textContentIsReadable(target) {
		return TextDiffResult{}, errors.New("Comparison Entry does not have readable text on both sides")
	}

	baselineText := ""
	if baseline.Status == ContentReady {
		baselineText = baseline.Text
	}
	targetText := ""
	if target.Status == ContentReady {
		targetText = target.Text
	}
	if entry.Status == StatusModified && baseline.Status == ContentReady && target.Status == ContentReady && baselineText == targetText {
		return TextDiffResult{
			Status:           TextDiffNoTextualChanges,
			Path:             entry.Path,
			ComparisonStatus: StatusModified,
			Reason:           TextDiffEncodingOrBOMOnly,
			BaselineEncoding: textEncodingPointer(baseline.Encoding),
			TargetEncoding:   textEncodingPointer(target.Encoding),
		}, nil
	}

	operations, err := calculateTextDiff(ctx, splitTextDiffLines(baselineText), splitTextDiffLines(targetText))
	if err != nil {
		return textDiffCalculationFailure(entry, err)
	}
	patch, addedLines, deletedLines, err := formatTextDiff(ctx, operations, contextLines, textDiffHeader(baseline, "baseline"), textDiffHeader(target, "target"))
	if err != nil {
		return textDiffCalculationFailure(entry, err)
	}
	outputBytes := len(patch)
	if outputBytes > maxTextDiffOutputBytes {
		return TextDiffResult{
			Status:           TextDiffUnavailable,
			Path:             entry.Path,
			ComparisonStatus: entry.Status,
			Reason:           TextDiffOutputTooLarge,
			AddedLines:       addedLines,
			DeletedLines:     deletedLines,
			OutputBytes:      outputBytes,
		}, nil
	}

	result := TextDiffResult{
		Status:           TextDiffReady,
		Path:             entry.Path,
		ComparisonStatus: entry.Status,
		ContextLines:     contextLines,
		AddedLines:       addedLines,
		DeletedLines:     deletedLines,
		Patch:            patch,
	}
	if baseline.Status == ContentReady {
		result.BaselineEncoding = textEncodingPointer(baseline.Encoding)
	}
	if target.Status == ContentReady {
		result.TargetEncoding = textEncodingPointer(target.Encoding)
	}
	return result, nil
}

func (snapshot *Snapshot) acquireTextDiffSlot() bool {
	if snapshot.textDiffSlots == nil {
		return false
	}
	select {
	case snapshot.textDiffSlots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (snapshot *Snapshot) releaseTextDiffSlot() {
	<-snapshot.textDiffSlots
}

func textDiffContextLines(options *TextDiffOptions) (int, error) {
	if options == nil || options.ContextLines == nil {
		return defaultTextDiffContext, nil
	}
	if *options.ContextLines < 0 || *options.ContextLines > maxTextDiffContext {
		return 0, errInvalidTextDiffContext
	}
	return *options.ContextLines, nil
}

func unavailableTextDiff(entry *snapshotEntry, reason TextDiffReason) TextDiffResult {
	return TextDiffResult{
		Status:           TextDiffUnavailable,
		Path:             entry.Path,
		ComparisonStatus: entry.Status,
		Reason:           reason,
	}
}

func textContentIsLarge(content TextContent) bool {
	return content.Status == ContentGuarded || content.Status == ContentBlocked
}

func textContentIsReadable(content TextContent) bool {
	return content.Status == ContentReady || content.Status == ContentMissing
}

func addTextDiffContentMetadata(result *TextDiffResult, side ComparisonSide, content TextContent) {
	if !textContentIsLarge(content) {
		return
	}
	size := content.Size
	includeLineCount := content.Status == ContentGuarded || content.LineCount > 0
	switch side {
	case SideBaseline:
		result.BaselineSize = &size
		if includeLineCount {
			lineCount := content.LineCount
			result.BaselineLineCount = &lineCount
		}
	case SideTarget:
		result.TargetSize = &size
		if includeLineCount {
			lineCount := content.LineCount
			result.TargetLineCount = &lineCount
		}
	}
}

func textDiffHeader(content TextContent, name string) string {
	if content.Status == ContentMissing {
		return "/dev/null"
	}
	return name
}

func textEncodingPointer(encoding TextEncoding) *TextEncoding {
	value := encoding
	return &value
}

func textDiffContextFailure(entry *snapshotEntry, err error) (TextDiffResult, error) {
	if errors.Is(err, context.DeadlineExceeded) {
		return unavailableTextDiff(entry, TextDiffComplexityLimit), nil
	}
	return TextDiffResult{}, err
}

func textDiffCalculationFailure(entry *snapshotEntry, err error) (TextDiffResult, error) {
	if errors.Is(err, errTextDiffComplexity) || errors.Is(err, context.DeadlineExceeded) {
		return unavailableTextDiff(entry, TextDiffComplexityLimit), nil
	}
	return TextDiffResult{}, err
}

type textDiffLine struct {
	text string
}

type textDiffOperationKind uint8

const (
	textDiffEqual textDiffOperationKind = iota
	textDiffDelete
	textDiffAdd
)

type textDiffOperation struct {
	kind textDiffOperationKind
	line textDiffLine
}

func splitTextDiffLines(text string) []textDiffLine {
	if text == "" {
		return nil
	}
	lines := make([]textDiffLine, 0, strings.Count(text, "\n")+1)
	for len(text) > 0 {
		newline := strings.IndexByte(text, '\n')
		if newline < 0 {
			lines = append(lines, textDiffLine{text: text})
			break
		}
		lines = append(lines, textDiffLine{text: text[:newline+1]})
		text = text[newline+1:]
	}
	return lines
}

func calculateTextDiff(ctx context.Context, baseline, target []textDiffLine) ([]textDiffOperation, error) {
	if len(baseline)+len(target) > maxTextDiffLines {
		return nil, errTextDiffComplexity
	}
	if len(baseline) == 0 {
		return textDiffSingleSide(ctx, target, textDiffAdd)
	}
	if len(target) == 0 {
		return textDiffSingleSide(ctx, baseline, textDiffDelete)
	}
	if len(target) > maxTextDiffCells/len(baseline) {
		return nil, errTextDiffComplexity
	}

	columns := len(target)
	directions := make([]textDiffOperationKind, len(baseline)*columns)
	previous := make([]int, columns+1)
	current := make([]int, columns+1)
	for baselineIndex, baselineLine := range baseline {
		current[0] = 0
		for targetIndex, targetLine := range target {
			if (baselineIndex*columns+targetIndex)&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			directionIndex := baselineIndex*columns + targetIndex
			switch {
			case baselineLine.text == targetLine.text:
				current[targetIndex+1] = previous[targetIndex] + 1
				directions[directionIndex] = textDiffEqual
			case previous[targetIndex+1] >= current[targetIndex]:
				current[targetIndex+1] = previous[targetIndex+1]
				directions[directionIndex] = textDiffDelete
			default:
				current[targetIndex+1] = current[targetIndex]
				directions[directionIndex] = textDiffAdd
			}
		}
		previous, current = current, previous
	}

	operations := make([]textDiffOperation, 0, len(baseline)+len(target))
	for baselineIndex, targetIndex := len(baseline), len(target); baselineIndex > 0 || targetIndex > 0; {
		if (baselineIndex+targetIndex)&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		switch {
		case baselineIndex == 0:
			targetIndex--
			operations = append(operations, textDiffOperation{kind: textDiffAdd, line: target[targetIndex]})
		case targetIndex == 0:
			baselineIndex--
			operations = append(operations, textDiffOperation{kind: textDiffDelete, line: baseline[baselineIndex]})
		case directions[(baselineIndex-1)*columns+targetIndex-1] == textDiffEqual:
			baselineIndex--
			targetIndex--
			operations = append(operations, textDiffOperation{kind: textDiffEqual, line: baseline[baselineIndex]})
		case directions[(baselineIndex-1)*columns+targetIndex-1] == textDiffDelete:
			baselineIndex--
			operations = append(operations, textDiffOperation{kind: textDiffDelete, line: baseline[baselineIndex]})
		default:
			targetIndex--
			operations = append(operations, textDiffOperation{kind: textDiffAdd, line: target[targetIndex]})
		}
	}
	for left, right := 0, len(operations)-1; left < right; left, right = left+1, right-1 {
		operations[left], operations[right] = operations[right], operations[left]
	}
	return normalizeTextDiffReplacements(operations), nil
}

func normalizeTextDiffReplacements(operations []textDiffOperation) []textDiffOperation {
	normalized := make([]textDiffOperation, 0, len(operations))
	for start := 0; start < len(operations); {
		if operations[start].kind == textDiffEqual {
			normalized = append(normalized, operations[start])
			start++
			continue
		}
		end := start
		for end < len(operations) && operations[end].kind != textDiffEqual {
			end++
		}
		for index := start; index < end; index++ {
			if operations[index].kind == textDiffDelete {
				normalized = append(normalized, operations[index])
			}
		}
		for index := start; index < end; index++ {
			if operations[index].kind == textDiffAdd {
				normalized = append(normalized, operations[index])
			}
		}
		start = end
	}
	return normalized
}

func textDiffSingleSide(ctx context.Context, lines []textDiffLine, kind textDiffOperationKind) ([]textDiffOperation, error) {
	operations := make([]textDiffOperation, len(lines))
	for index, line := range lines {
		if index&1023 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		operations[index] = textDiffOperation{kind: kind, line: line}
	}
	return operations, nil
}

func formatTextDiff(ctx context.Context, operations []textDiffOperation, contextLines int, baselineHeader, targetHeader string) (string, int, int, error) {
	var output strings.Builder
	output.WriteString("--- ")
	output.WriteString(baselineHeader)
	output.WriteByte('\n')
	output.WriteString("+++ ")
	output.WriteString(targetHeader)
	output.WriteByte('\n')

	addedLines, deletedLines := 0, 0
	for _, operation := range operations {
		switch operation.kind {
		case textDiffAdd:
			addedLines++
		case textDiffDelete:
			deletedLines++
		}
	}

	for _, hunk := range textDiffHunks(operations, contextLines) {
		if err := ctx.Err(); err != nil {
			return "", 0, 0, err
		}
		baselineStart, targetStart := 1, 1
		for index := 0; index < hunk.start; index++ {
			if operations[index].kind != textDiffAdd {
				baselineStart++
			}
			if operations[index].kind != textDiffDelete {
				targetStart++
			}
		}
		baselineCount, targetCount := 0, 0
		for index := hunk.start; index < hunk.end; index++ {
			if operations[index].kind != textDiffAdd {
				baselineCount++
			}
			if operations[index].kind != textDiffDelete {
				targetCount++
			}
		}
		forceCounts := baselineCount == 0 || targetCount == 0
		fmt.Fprintf(&output, "@@ -%s +%s @@\n", textDiffRange(baselineStart, baselineCount, forceCounts), textDiffRange(targetStart, targetCount, forceCounts))
		for index := hunk.start; index < hunk.end; index++ {
			if index&1023 == 0 {
				if err := ctx.Err(); err != nil {
					return "", 0, 0, err
				}
			}
			writeTextDiffOperation(&output, operations[index])
		}
	}
	return output.String(), addedLines, deletedLines, nil
}

type textDiffHunk struct {
	start int
	end   int
}

func textDiffHunks(operations []textDiffOperation, contextLines int) []textDiffHunk {
	hunks := make([]textDiffHunk, 0)
	for index, operation := range operations {
		if operation.kind == textDiffEqual {
			continue
		}
		start := index - contextLines
		if start < 0 {
			start = 0
		}
		end := index + contextLines + 1
		if end > len(operations) {
			end = len(operations)
		}
		if len(hunks) > 0 && start <= hunks[len(hunks)-1].end {
			if end > hunks[len(hunks)-1].end {
				hunks[len(hunks)-1].end = end
			}
			continue
		}
		hunks = append(hunks, textDiffHunk{start: start, end: end})
	}
	return hunks
}

func textDiffRange(start, count int, forceCount bool) string {
	if count == 0 {
		return strconv.Itoa(start-1) + ",0"
	}
	if count == 1 && !forceCount {
		return strconv.Itoa(start)
	}
	return strconv.Itoa(start) + "," + strconv.Itoa(count)
}

func writeTextDiffOperation(output *strings.Builder, operation textDiffOperation) {
	switch operation.kind {
	case textDiffEqual:
		output.WriteByte(' ')
	case textDiffDelete:
		output.WriteByte('-')
	case textDiffAdd:
		output.WriteByte('+')
	}
	output.WriteString(operation.line.text)
	if !strings.HasSuffix(operation.line.text, "\n") {
		output.WriteByte('\n')
		output.WriteString("\\ No newline at end of file\n")
	}
}
