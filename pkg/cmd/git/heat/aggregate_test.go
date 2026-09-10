package heat

import (
	"reflect"
	"testing"
)

func TestAggregateCountsKindsUsesNewestTimeAndBuildsImmediateDirectories(t *testing.T) {
	aggregation := Aggregate(Log{
		CommitCount: 4,
		Changes: []Change{
			{Kind: ChangeModified, Path: "src/a.go", ChangedAt: "2024-01-01 00:00:10", ChangedAtEpoch: 10},
			{Kind: ChangeAdded, Path: "src/a.go", ChangedAt: "2024-01-01 00:00:20", ChangedAtEpoch: 20},
			{Kind: ChangeDeleted, Path: "src/nested/b.go", ChangedAt: "2024-01-01 00:00:15", ChangedAtEpoch: 15},
			{Kind: ChangeRenamed, Path: "root.txt", ChangedAt: "2024-01-01 00:00:30", ChangedAtEpoch: 30},
			{Kind: ChangeCopied, Path: "src/c.go", ChangedAt: "2024-01-01 00:00:20", ChangedAtEpoch: 20},
			{Kind: ChangeModified, Path: "src/a.go", ChangedAt: "2024-01-01 00:00:05", ChangedAtEpoch: 5},
		},
	}, SortPath)

	wantFiles := []PathHeat{
		{
			Path:      "src/a.go",
			Counts:    Counts{Total: 3, Modified: 2, Added: 1},
			ChangedAt: "2024-01-01 00:00:20", ChangedAtEpoch: 20,
		},
		{
			Path:      "src/c.go",
			Counts:    Counts{Total: 1, Copied: 1},
			ChangedAt: "2024-01-01 00:00:20", ChangedAtEpoch: 20,
		},
		{
			Path:      "src/nested/b.go",
			Counts:    Counts{Total: 1, Deleted: 1},
			ChangedAt: "2024-01-01 00:00:15", ChangedAtEpoch: 15,
		},
		{
			Path:      "root.txt",
			Counts:    Counts{Total: 1, Renamed: 1},
			ChangedAt: "2024-01-01 00:00:30", ChangedAtEpoch: 30,
		},
	}
	if !reflect.DeepEqual(aggregation.Files, wantFiles) {
		t.Fatalf("files = %#v, want %#v", aggregation.Files, wantFiles)
	}
	wantDirectories := []PathHeat{
		{
			Path:      "src",
			Counts:    Counts{Total: 4, Modified: 2, Added: 1, Copied: 1},
			ChangedAt: "2024-01-01 00:00:20", ChangedAtEpoch: 20,
		},
		{
			Path:      "src/nested",
			Counts:    Counts{Total: 1, Deleted: 1},
			ChangedAt: "2024-01-01 00:00:15", ChangedAtEpoch: 15,
		},
		{
			Path:      ".",
			Counts:    Counts{Total: 1, Renamed: 1},
			ChangedAt: "2024-01-01 00:00:30", ChangedAtEpoch: 30,
		},
	}
	if aggregation.CommitCount != 4 {
		t.Fatalf("commit count = %d, want 4", aggregation.CommitCount)
	}
	if !reflect.DeepEqual(aggregation.Directories, wantDirectories) {
		t.Fatalf("directories = %#v, want %#v", aggregation.Directories, wantDirectories)
	}
}

func TestAggregateSortsCountsThenEnglishLocalePaths(t *testing.T) {
	aggregation := Aggregate(Log{Changes: []Change{
		{Kind: ChangeAdded, Path: "Z.txt"},
		{Kind: ChangeAdded, Path: "a.txt"},
		{Kind: ChangeAdded, Path: "A.txt"},
		{Kind: ChangeAdded, Path: "á.txt"},
		{Kind: ChangeAdded, Path: "ä.txt"},
		{Kind: ChangeAdded, Path: "two.txt"},
		{Kind: ChangeModified, Path: "two.txt"},
	}}, SortCount)

	if got, want := heatPaths(aggregation.Files), []string{"two.txt", "a.txt", "A.txt", "á.txt", "ä.txt", "Z.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("file paths = %#v, want %#v", got, want)
	}
}

func TestAggregateTreatsOnlySlashAsAGitDirectorySeparator(t *testing.T) {
	aggregation := Aggregate(Log{Changes: []Change{
		{Kind: ChangeAdded, Path: "nested/file.txt"},
		{Kind: ChangeAdded, Path: "back\\slash.txt"},
	}}, SortPath)

	if got, want := heatPaths(aggregation.Files), []string{"nested/file.txt", "back\\slash.txt"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("file paths = %#v, want %#v", got, want)
	}
	if got, want := heatPaths(aggregation.Directories), []string{"nested", "."}; !reflect.DeepEqual(got, want) {
		t.Fatalf("directory paths = %#v, want %#v", got, want)
	}
}

func heatPaths(rows []PathHeat) []string {
	paths := make([]string, len(rows))
	for index, row := range rows {
		paths[index] = row.Path
	}
	return paths
}
