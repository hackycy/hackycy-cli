package heat

import (
	"reflect"
	"testing"
)

func TestParseLogPreservesPathsAndUsesRenameCopyDestinations(t *testing.T) {
	output := []byte(
		"\x00" + heatCommitMarker + "abc\x1f1710000000\x1f2024-03-09 12:00:00 +0800\x00" +
			"M\x00dir/space name.txt\x00" +
			"R100\x00old\tname\x00new\nname\x00" +
			"C100\x00copy-source\x00quote\"name\\.txt\x00" +
			"T\x00ignored.txt\x00" +
			"\x00" + heatCommitMarker + "def\x1f1710000010\x1f2024-03-09 12:00:10 +0800\x00" +
			"A\x00--pathspec:[literal]中.txt\x00",
	)

	parsed, err := ParseLog(output)
	if err != nil {
		t.Fatalf("ParseLog() error = %v", err)
	}
	want := Log{
		CommitCount: 2,
		Changes: []Change{
			{Kind: ChangeModified, Path: "dir/space name.txt", ChangedAt: "2024-03-09 12:00:00", ChangedAtEpoch: 1710000000},
			{Kind: ChangeRenamed, Path: "new\nname", ChangedAt: "2024-03-09 12:00:00", ChangedAtEpoch: 1710000000},
			{Kind: ChangeCopied, Path: "quote\"name\\.txt", ChangedAt: "2024-03-09 12:00:00", ChangedAtEpoch: 1710000000},
			{Kind: ChangeAdded, Path: "--pathspec:[literal]中.txt", ChangedAt: "2024-03-09 12:00:10", ChangedAtEpoch: 1710000010},
		},
	}
	if !reflect.DeepEqual(parsed, want) {
		t.Fatalf("ParseLog() = %#v, want %#v", parsed, want)
	}
}

func TestParseLogAcceptsPrettyFormatNewlineBeforeStatus(t *testing.T) {
	parsed, err := ParseLog([]byte(
		"\x00" + heatCommitMarker + "abc\x1f1\x1f2024-01-01 00:00:00 +0000\x00\nM\x00line\nname\x00",
	))
	if err != nil {
		t.Fatalf("ParseLog() error = %v", err)
	}
	if !reflect.DeepEqual(parsed, Log{
		CommitCount: 1,
		Changes:     []Change{{Kind: ChangeModified, Path: "line\nname", ChangedAt: "2024-01-01 00:00:00", ChangedAtEpoch: 1}},
	}) {
		t.Fatalf("ParseLog() = %#v", parsed)
	}
}

func TestParseLogCountsEmptyCommitsAndRejectsMalformedRecords(t *testing.T) {
	empty, err := ParseLog([]byte("\x00" + heatCommitMarker + "abc\x1f1\x1f2024-01-01 00:00:00 +0000\x00"))
	if err != nil {
		t.Fatalf("ParseLog() error = %v", err)
	}
	if empty.CommitCount != 1 || len(empty.Changes) != 0 {
		t.Fatalf("empty log = %#v", empty)
	}

	for _, output := range [][]byte{
		[]byte("M\x00file.txt\x00"),
		[]byte("\x00" + heatCommitMarker + "abc\x1fnot-an-epoch\x1fdate\x00"),
		[]byte("\x00" + heatCommitMarker + "abc\x1f1\x1fdate\x00R100\x00source\x00"),
	} {
		if _, err := ParseLog(output); err == nil {
			t.Fatalf("ParseLog(%q) error = nil", output)
		}
	}
}
