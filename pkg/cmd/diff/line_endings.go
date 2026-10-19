package diff

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
)

// ComparisonOptions belongs to a request, never to the shared workspace.
type ComparisonOptions struct {
	IgnoreLineEndings bool
}

type LineEndingComparison struct {
	Supported bool   `json:"supported"`
	Only      bool   `json:"only"`
	Reason    string `json:"reason,omitempty"`
}

func ignoreLineEndings(options []ComparisonOptions) bool {
	return len(options) > 0 && options[0].IgnoreLineEndings
}

func projectEntry(entry Entry, ignore bool) Entry {
	entry = cloneEntry(entry)
	entry.RawStatus = entry.Status
	if ignore && entry.Status == StatusModified && entry.LineEndings.Only {
		entry.Status = StatusUnchanged
	}
	return entry
}

func projectCounts(counts StatusCounts, only int, ignore bool) StatusCounts {
	if ignore {
		counts.Modified -= only
		counts.Unchanged += only
	}
	return counts
}

func (snapshot *Snapshot) projectDirectory(node TreeNode, ignore bool) TreeNode {
	node.Counts = projectCounts(node.Counts, snapshot.lineEndingCounts[node.Path], ignore)
	return node
}

func countLineEndings(entries []snapshotEntry) map[string]int {
	counts := make(map[string]int)
	for _, entry := range entries {
		if !entry.LineEndings.Only {
			continue
		}
		for directory := parentComparisonPath(entry.Path); ; directory = parentComparisonPath(directory) {
			counts[directory]++
			if directory == "" {
				break
			}
		}
	}
	return counts
}

func compareLineEndings(ctx context.Context, baseline, target *sourceEntry, path, baselineRoot, targetRoot string) (LineEndingComparison, error) {
	unavailable := func(reason string) (LineEndingComparison, error) {
		return LineEndingComparison{Reason: reason}, nil
	}
	if baseline.state.Kind != EntryKindFile || target.state.Kind != EntryKindFile || isImagePath(path) {
		return unavailable("LF/CRLF ignoring applies only to text files")
	}
	if baseline.state.Size > maxConfirmedTextBytes || target.state.Size > maxConfirmedTextBytes {
		return unavailable("File exceeds 20 MiB; LF/CRLF ignoring was not applied")
	}
	left, err := readLineEndingSource(ctx, baseline, baselineRoot, path)
	if err != nil {
		return LineEndingComparison{}, err
	}
	right, err := readLineEndingSource(ctx, target, targetRoot, path)
	if err != nil {
		return LineEndingComparison{}, err
	}
	leftText, leftOK := decodeText(left)
	rightText, rightOK := decodeText(right)
	if err := ctx.Err(); err != nil {
		return LineEndingComparison{}, err
	}
	if !leftOK || !rightOK || strings.ContainsRune(leftText.text, 0) || strings.ContainsRune(rightText.text, 0) {
		return unavailable("Unsupported text; LF/CRLF ignoring was not applied")
	}
	if leftText.encoding != rightText.encoding || !bytes.Equal(textBOM(left), textBOM(right)) {
		return unavailable("Text encoding or BOM differs; LF/CRLF ignoring was not applied")
	}
	only := strings.ReplaceAll(leftText.text, "\r\n", "\n") == strings.ReplaceAll(rightText.text, "\r\n", "\n")
	return LineEndingComparison{Supported: true, Only: only}, ctx.Err()
}

func textBOM(data []byte) []byte {
	if bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		return data[:3]
	}
	if bytes.HasPrefix(data, []byte{0xff, 0xfe}) || bytes.HasPrefix(data, []byte{0xfe, 0xff}) {
		return data[:2]
	}
	return nil
}

// Bound reads even if a source grows after discovery, and check cancellation
// between chunks instead of using the rendering path's line/size guards.
func readLineEndingSource(ctx context.Context, source *sourceEntry, root, path string) ([]byte, error) {
	if !pathResolvesTo(absoluteComparisonPath(root, path)) {
		return nil, errors.New("Comparison Entry path changed while checking line endings")
	}
	if err := assertSourceStable(root, path, source); err != nil {
		return nil, err
	}
	file, err := openComparisonFile(absoluteComparisonPath(root, path))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	before, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !sameSourceInfo(source.info, before) {
		return nil, errors.New("Comparison Entry changed while checking line endings")
	}
	data := make([]byte, 0, int(source.state.Size))
	buffer := make([]byte, comparisonChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := file.Read(buffer)
		if int64(len(data)+n) > maxConfirmedTextBytes || int64(len(data)+n) > source.state.Size {
			return nil, errors.New("Comparison Entry changed while checking line endings")
		}
		data = append(data, buffer[:n]...)
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !sameSourceInfo(source.info, after) || int64(len(data)) != source.state.Size {
		return nil, errors.New("Comparison Entry changed while checking line endings")
	}
	if !pathResolvesTo(absoluteComparisonPath(root, path)) {
		return nil, errors.New("Comparison Entry path changed while checking line endings")
	}
	return data, assertSourceStable(root, path, source)
}
