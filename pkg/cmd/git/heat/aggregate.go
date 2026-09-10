package heat

import (
	"path"
	"sort"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Counts records the observed occurrences for each supported Git status.
type Counts struct {
	Total    int
	Modified int
	Added    int
	Deleted  int
	Renamed  int
	Copied   int
}

// PathHeat is the aggregated heat for one Git file or immediate directory.
type PathHeat struct {
	Path string
	Counts

	ChangedAt      string
	ChangedAtEpoch int64
}

// Aggregation contains all file and directory rows for one parsed log range.
type Aggregation struct {
	CommitCount int
	Files       []PathHeat
	Directories []PathHeat
}

// Aggregate combines parsed changes according to the legacy heat semantics.
func Aggregate(log Log, sortKind Sort) Aggregation {
	collator := collate.New(language.AmericanEnglish)
	files := aggregateFiles(log.Changes)
	sortFileRows(files, sortKind, collator)
	directories := aggregateDirectories(files)
	sortDirectoryRows(directories, sortKind, collator)
	return Aggregation{
		CommitCount: log.CommitCount,
		Files:       files,
		Directories: directories,
	}
}

func aggregateFiles(changes []Change) []PathHeat {
	rows := make([]PathHeat, 0, len(changes))
	indexByPath := make(map[string]int, len(changes))
	for _, change := range changes {
		index, exists := indexByPath[change.Path]
		if !exists {
			index = len(rows)
			indexByPath[change.Path] = index
			rows = append(rows, PathHeat{Path: change.Path})
		}
		incrementHeat(&rows[index], change)
	}
	return rows
}

func aggregateDirectories(files []PathHeat) []PathHeat {
	rows := make([]PathHeat, 0, len(files))
	indexByPath := make(map[string]int, len(files))
	for _, file := range files {
		directory := path.Dir(file.Path)
		index, exists := indexByPath[directory]
		if !exists {
			index = len(rows)
			indexByPath[directory] = index
			rows = append(rows, PathHeat{Path: directory})
		}
		row := &rows[index]
		row.Total += file.Total
		row.Modified += file.Modified
		row.Added += file.Added
		row.Deleted += file.Deleted
		row.Renamed += file.Renamed
		row.Copied += file.Copied
		if file.ChangedAtEpoch > row.ChangedAtEpoch {
			row.ChangedAt = file.ChangedAt
			row.ChangedAtEpoch = file.ChangedAtEpoch
		}
	}
	return rows
}

func incrementHeat(row *PathHeat, change Change) {
	row.Total++
	if change.ChangedAtEpoch >= row.ChangedAtEpoch {
		row.ChangedAt = change.ChangedAt
		row.ChangedAtEpoch = change.ChangedAtEpoch
	}
	switch change.Kind {
	case ChangeModified:
		row.Modified++
	case ChangeAdded:
		row.Added++
	case ChangeDeleted:
		row.Deleted++
	case ChangeRenamed:
		row.Renamed++
	case ChangeCopied:
		row.Copied++
	}
}

func sortFileRows(rows []PathHeat, sortKind Sort, collator *collate.Collator) {
	sort.SliceStable(rows, func(leftIndex, rightIndex int) bool {
		left := rows[leftIndex]
		right := rows[rightIndex]
		if sortKind == SortPath {
			leftRoot := !containsGitDirectory(left.Path)
			rightRoot := !containsGitDirectory(right.Path)
			if leftRoot != rightRoot {
				return !leftRoot
			}
		} else if left.Total != right.Total {
			return left.Total > right.Total
		}
		return collator.CompareString(left.Path, right.Path) < 0
	})
}

func sortDirectoryRows(rows []PathHeat, sortKind Sort, collator *collate.Collator) {
	sort.SliceStable(rows, func(leftIndex, rightIndex int) bool {
		left := rows[leftIndex]
		right := rows[rightIndex]
		if sortKind == SortPath {
			if left.Path == "." && right.Path != "." {
				return false
			}
			if right.Path == "." && left.Path != "." {
				return true
			}
		} else if left.Total != right.Total {
			return left.Total > right.Total
		}
		return collator.CompareString(left.Path, right.Path) < 0
	})
}

func containsGitDirectory(filePath string) bool {
	for _, character := range filePath {
		if character == '/' {
			return true
		}
	}
	return false
}
