package diff

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSnapshotBlobReadsByteExactFilesWithExtensionOnlyMIME(t *testing.T) {
	baseline, target := comparisonRoots(t)
	imageCases := []struct {
		path string
		mime string
		body []byte
	}{
		{path: "preview.AVIF", mime: "image/avif", body: []byte{0x01}},
		{path: "preview.GiF", mime: "image/gif", body: []byte{0x02}},
		{path: "preview.JPEG", mime: "image/jpeg", body: []byte{0x03}},
		{path: "preview.jpg", mime: "image/jpeg", body: []byte{0x04}},
		{path: "preview.PNG", mime: "image/png", body: []byte{0xff, 0x00}},
		{path: "nested/preview.SVG", mime: "image/svg+xml", body: []byte("not actually svg")},
		{path: "preview.webp", mime: "image/webp", body: []byte{0x07}},
	}
	for _, testCase := range imageCases {
		writeComparisonBytes(t, target, testCase.path, testCase.body)
	}
	writeComparisonBytes(t, target, "archive.bin", []byte{0xff, 0xfe, 0x00})
	writeComparisonBytes(t, target, "large.png", make([]byte, int(maxConfirmedTextBytes)+1))
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)

	for _, testCase := range imageCases {
		t.Run(testCase.path, func(t *testing.T) {
			blob, err := snapshot.Blob(snapshotEntryID(t, snapshot, testCase.path), SideTarget)
			if err != nil || blob.Status != BlobReady || blob.MIMEType != testCase.mime || blob.Filename != filepath.Base(testCase.path) || !bytes.Equal(blob.Bytes, testCase.body) {
				t.Fatalf("Blob() = %#v, error = %v", blob, err)
			}
		})
	}
	ordinary, err := snapshot.Blob(snapshotEntryID(t, snapshot, "archive.bin"), SideTarget)
	if err != nil || ordinary.Status != BlobReady || ordinary.MIMEType != "application/octet-stream" || ordinary.Filename != "archive.bin" || !bytes.Equal(ordinary.Bytes, []byte{0xff, 0xfe, 0x00}) {
		t.Fatalf("ordinary Blob() = %#v, error = %v", ordinary, err)
	}
	ordinary.Bytes[0] = 0
	if repeated, err := snapshot.Blob(snapshotEntryID(t, snapshot, "archive.bin"), SideTarget); err != nil || !bytes.Equal(repeated.Bytes, []byte{0xff, 0xfe, 0x00}) {
		t.Fatalf("repeated Blob() = %#v, error = %v", repeated, err)
	}
	large, err := snapshot.Blob(snapshotEntryID(t, snapshot, "large.png"), SideTarget)
	if err != nil || large.Status != BlobReady || large.MIMEType != "image/png" || len(large.Bytes) != int(maxConfirmedTextBytes)+1 {
		t.Fatalf("large Blob() = status %q, MIME %q, length %d, error = %v", large.Status, large.MIMEType, len(large.Bytes), err)
	}
	if missing, err := snapshot.Blob(snapshotEntryID(t, snapshot, "archive.bin"), SideBaseline); err != nil || missing.Status != BlobMissing {
		t.Fatalf("missing Blob() = %#v, error = %v", missing, err)
	}
	if _, err := snapshot.Blob(0, SideTarget); !errors.Is(err, errComparisonEntryNotFound) {
		t.Fatalf("unknown Blob() error = %v", err)
	}
}

func TestSnapshotBlobRejectsUnavailableAndStaleSources(t *testing.T) {
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, target, "stale.txt", "before")
	if err := os.Symlink("missing-target", filepath.Join(target, "link")); err != nil {
		t.Fatal(err)
	}
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	if unavailable, err := snapshot.Blob(snapshotEntryID(t, snapshot, "link"), SideTarget); err != nil || unavailable.Status != BlobUnavailable {
		t.Fatalf("unavailable Blob() = %#v, error = %v", unavailable, err)
	}
	if _, err := snapshot.Blob(snapshotEntryID(t, snapshot, "stale.txt"), ComparisonSide("other")); !errors.Is(err, errInvalidComparisonSide) {
		t.Fatalf("invalid-side Blob() error = %v", err)
	}

	if runtime.GOOS != "windows" {
		outside := filepath.Join(filepath.Dir(target), "outside-secret.txt")
		writeComparisonFile(t, filepath.Dir(target), "outside-secret.txt", "secret bytes that must not be returned")
		if err := os.Remove(filepath.Join(target, "stale.txt")); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(target, "stale.txt")); err != nil {
			t.Fatal(err)
		}
		stale, err := snapshot.Blob(snapshotEntryID(t, snapshot, "stale.txt"), SideTarget)
		if err != nil || stale.Status != BlobStale || len(stale.Bytes) != 0 {
			t.Fatalf("stale Blob() = %#v, error = %v", stale, err)
		}
	}

	issueSnapshot := &Snapshot{entries: []snapshotEntry{{Entry: Entry{ID: 1, Status: StatusIssue}}}}
	if issue, err := issueSnapshot.Blob(1, SideTarget); err != nil || issue.Status != BlobUnavailable {
		t.Fatalf("issue Blob() = %#v, error = %v", issue, err)
	}
}
