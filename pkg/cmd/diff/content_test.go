package diff

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshotDetailAndContentClassifyStableSources(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonBytes(t, target, "utf8.txt", append([]byte{0xef, 0xbb, 0xbf}, []byte("hello\x00world")...))
	writeComparisonBytes(t, target, "utf16le.txt", []byte{0xff, 0xfe, 'h', 0, 'i', 0})
	writeComparisonBytes(t, target, "invalid.bin", []byte{0xff, 0xfe, 0x61})
	writeComparisonBytes(t, target, "image.PNG", []byte{0xff, 0x00})
	if err := os.Symlink("missing-target", filepath.Join(target, "link")); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)

	utf8ID := snapshotEntryID(t, snapshot, "utf8.txt")
	detail, err := snapshot.Detail(utf8ID)
	if err != nil || detail.Presentation != PresentationText {
		t.Fatalf("utf8 detail = %#v, error = %v", detail, err)
	}
	content, err := snapshot.Content(utf8ID, SideTarget, false)
	if err != nil || content.Status != ContentReady || content.Text != "hello\x00world" || content.Encoding != EncodingUTF8 || content.Size != int64(len([]byte{0xef, 0xbb, 0xbf})+len("hello\x00world")) || content.LineCount != 1 {
		t.Fatalf("utf8 content = %#v, error = %v", content, err)
	}
	if missing, err := snapshot.Content(utf8ID, SideBaseline, false); err != nil || missing.Status != ContentMissing {
		t.Fatalf("missing content = %#v, error = %v", missing, err)
	}

	utf16ID := snapshotEntryID(t, snapshot, "utf16le.txt")
	utf16, err := snapshot.Content(utf16ID, SideTarget, false)
	if err != nil || utf16.Status != ContentReady || utf16.Text != "hi" || utf16.Encoding != EncodingUTF16LE || utf16.LineCount != 1 {
		t.Fatalf("utf16 content = %#v, error = %v", utf16, err)
	}
	invalidID := snapshotEntryID(t, snapshot, "invalid.bin")
	if invalid, err := snapshot.Content(invalidID, SideTarget, false); err != nil || invalid.Status != ContentBinary {
		t.Fatalf("invalid content = %#v, error = %v", invalid, err)
	}
	imageID := snapshotEntryID(t, snapshot, "image.PNG")
	if image, err := snapshot.Detail(imageID); err != nil || image.Presentation != PresentationImage {
		t.Fatalf("image detail = %#v, error = %v", image, err)
	}
	linkID := snapshotEntryID(t, snapshot, "link")
	if link, err := snapshot.Detail(linkID); err != nil || link.Presentation != PresentationSymlink {
		t.Fatalf("link detail = %#v, error = %v", link, err)
	}
	if link, err := snapshot.Content(linkID, SideTarget, false); err != nil || link.Status != ContentBinary {
		t.Fatalf("link content = %#v, error = %v", link, err)
	}
}

func TestSnapshotContentHonorsTextLimitsAndReportsStaleSources(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, target, "guarded.txt", strings.Repeat("x\n", 50_000))
	writeComparisonFile(t, target, "blocked.txt", strings.Repeat("x\n", 200_000))
	writeComparisonFile(t, target, "long.txt", strings.Repeat("x", maxTextLineLength+1))
	writeComparisonFile(t, target, "stale.txt", "before")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)

	guarded, err := snapshot.Content(snapshotEntryID(t, snapshot, "guarded.txt"), SideTarget, false)
	if err != nil || guarded.Status != ContentGuarded || guarded.LineCount != 50_001 {
		t.Fatalf("guarded content = %#v, error = %v", guarded, err)
	}
	if forced, err := snapshot.Content(snapshotEntryID(t, snapshot, "guarded.txt"), SideTarget, true); err != nil || forced.Status != ContentReady {
		t.Fatalf("forced guarded content = %#v, error = %v", forced, err)
	}
	blocked, err := snapshot.Content(snapshotEntryID(t, snapshot, "blocked.txt"), SideTarget, true)
	if err != nil || blocked.Status != ContentBlocked || blocked.LineCount != 200_001 {
		t.Fatalf("blocked content = %#v, error = %v", blocked, err)
	}
	long, err := snapshot.Content(snapshotEntryID(t, snapshot, "long.txt"), SideTarget, true)
	if err != nil || long.Status != ContentBlocked || long.LineCount != 1 {
		t.Fatalf("long-line content = %#v, error = %v", long, err)
	}
	staleID := snapshotEntryID(t, snapshot, "stale.txt")
	if err := os.WriteFile(filepath.Join(target, "stale.txt"), []byte("changed-size"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale, err := snapshot.Content(staleID, SideTarget, false)
	if err != nil || stale.Status != ContentStale {
		t.Fatalf("stale content = %#v, error = %v", stale, err)
	}
}

func TestDecodeTextAndLineMetricsMatchTheTextDecoderContract(t *testing.T) {
	testCases := []struct {
		name     string
		bytes    []byte
		text     string
		encoding TextEncoding
		valid    bool
	}{
		{name: "empty utf8", bytes: []byte{}, text: "", encoding: EncodingUTF8, valid: true},
		{name: "utf8 bom", bytes: []byte{0xef, 0xbb, 0xbf, 'h', 'i'}, text: "hi", encoding: EncodingUTF8, valid: true},
		{name: "utf8 nul", bytes: []byte{'h', 0, 'i'}, text: "h\x00i", encoding: EncodingUTF8, valid: true},
		{name: "utf16 little endian", bytes: []byte{0xff, 0xfe, 'h', 0, 'i', 0}, text: "hi", encoding: EncodingUTF16LE, valid: true},
		{name: "utf16 big endian", bytes: []byte{0xfe, 0xff, 0, 'h', 0, 'i'}, text: "hi", encoding: EncodingUTF16BE, valid: true},
		{name: "utf16 supplementary", bytes: []byte{0xff, 0xfe, 0x3d, 0xd8, 0x00, 0xde}, text: "\U0001f600", encoding: EncodingUTF16LE, valid: true},
		{name: "invalid utf8", bytes: []byte{0xc3, 0x28}},
		{name: "odd utf16", bytes: []byte{0xff, 0xfe, 0x61}},
		{name: "unpaired utf16 high surrogate", bytes: []byte{0xff, 0xfe, 0x00, 0xd8}},
		{name: "unpaired utf16 low surrogate", bytes: []byte{0xfe, 0xff, 0xdc, 0x00}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			decoded, ok := decodeText(testCase.bytes)
			if ok != testCase.valid {
				t.Fatalf("decodeText() valid = %t, want %t", ok, testCase.valid)
			}
			if ok && (decoded.text != testCase.text || decoded.encoding != testCase.encoding) {
				t.Fatalf("decodeText() = %#v, want text %q and encoding %q", decoded, testCase.text, testCase.encoding)
			}
		})
	}

	lineCases := []struct {
		text  string
		lines int
	}{
		{text: "", lines: 0},
		{text: "one", lines: 1},
		{text: "one\n", lines: 2},
		{text: "one\rtwo", lines: 2},
		{text: "one\r\ntwo", lines: 2},
		{text: "\r\n", lines: 2},
		{text: "\U0001f600", lines: 1},
	}
	for _, testCase := range lineCases {
		lines, oversized := textLineMetrics(testCase.text)
		if lines != testCase.lines || oversized {
			t.Fatalf("textLineMetrics(%q) = (%d, %t), want (%d, false)", testCase.text, lines, oversized, testCase.lines)
		}
	}

	withinLimit := strings.Repeat("\U0001f600", maxTextLineLength/2)
	if _, oversized := textLineMetrics(withinLimit); oversized {
		t.Fatal("a line at the UTF-16 code-unit limit is oversized")
	}
	if _, oversized := textLineMetrics(withinLimit + "\U0001f600"); !oversized {
		t.Fatal("a line over the UTF-16 code-unit limit is not oversized")
	}
}

func TestSnapshotDetailPrefersStaleOverBinarySources(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonBytes(t, baseline, "mixed.bin", []byte{0xc3, 0x28})
	writeComparisonFile(t, target, "mixed.bin", "before")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	if err := os.WriteFile(filepath.Join(target, "mixed.bin"), []byte("changed-size"), 0o644); err != nil {
		t.Fatal(err)
	}
	detail, err := snapshot.Detail(snapshotEntryID(t, snapshot, "mixed.bin"))
	if err != nil || detail.Presentation != PresentationStale {
		t.Fatalf("detail = %#v, error = %v", detail, err)
	}
}

func TestSnapshotContentHonorsByteBoundariesIndependentlyOfLineLimits(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, target, "automatic-at-limit.txt", textWithShortLines(int(maxAutoTextBytes)))
	writeComparisonFile(t, target, "automatic-over-limit.txt", textWithShortLines(int(maxAutoTextBytes)+1))
	writeComparisonFile(t, target, "confirmed-at-limit.txt", textWithShortLines(int(maxConfirmedTextBytes)))
	writeComparisonFile(t, target, "confirmed-over-limit.txt", textWithShortLines(int(maxConfirmedTextBytes)+1))
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)

	automaticAtLimit, err := snapshot.Content(snapshotEntryID(t, snapshot, "automatic-at-limit.txt"), SideTarget, false)
	if err != nil || automaticAtLimit.Status != ContentReady || automaticAtLimit.Size != maxAutoTextBytes {
		t.Fatalf("automatic content at limit = %#v, error = %v", automaticAtLimit, err)
	}
	automaticOverLimit, err := snapshot.Content(snapshotEntryID(t, snapshot, "automatic-over-limit.txt"), SideTarget, false)
	if err != nil || automaticOverLimit.Status != ContentGuarded || automaticOverLimit.Size != maxAutoTextBytes+1 {
		t.Fatalf("automatic content over limit = %#v, error = %v", automaticOverLimit, err)
	}
	if forced, err := snapshot.Content(snapshotEntryID(t, snapshot, "automatic-over-limit.txt"), SideTarget, true); err != nil || forced.Status != ContentReady {
		t.Fatalf("forced automatic content = %#v, error = %v", forced, err)
	}

	confirmedAtLimit, err := snapshot.Content(snapshotEntryID(t, snapshot, "confirmed-at-limit.txt"), SideTarget, true)
	if err != nil || confirmedAtLimit.Status != ContentReady || confirmedAtLimit.Size != maxConfirmedTextBytes {
		t.Fatalf("confirmed content at limit = %#v, error = %v", confirmedAtLimit, err)
	}
	confirmedOverLimit, err := snapshot.Content(snapshotEntryID(t, snapshot, "confirmed-over-limit.txt"), SideTarget, true)
	if err != nil || confirmedOverLimit.Status != ContentBlocked || confirmedOverLimit.Size != maxConfirmedTextBytes+1 || confirmedOverLimit.LineCount != 0 {
		t.Fatalf("confirmed content over limit = %#v, error = %v", confirmedOverLimit, err)
	}
}

func snapshotEntryID(t *testing.T, snapshot *Snapshot, path string) int {
	t.Helper()
	for _, entry := range snapshot.entries {
		if entry.Path == path {
			return entry.ID
		}
	}
	t.Fatalf("entry %q is absent", path)
	return 0
}

func writeComparisonBytes(t *testing.T, root, relativePath string, contents []byte) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func textWithShortLines(size int) string {
	const lineLength = 128
	line := strings.Repeat("x", lineLength-1) + "\n"
	completeLines := size / len(line)
	remainder := size % len(line)
	return strings.Repeat(line, completeLines) + strings.Repeat("x", remainder)
}
