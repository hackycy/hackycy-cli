package heat

import (
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// TimeMark identifies the newest or oldest nonzero changed time in a report.
type TimeMark string

const (
	TimeMarkEarliest TimeMark = "earliest"
	TimeMarkLatest   TimeMark = "latest"
)

// Match identifies one case-insensitive non-overlapping path-query match.
type Match struct {
	Start int
	End   int
}

// Report is the semantic report rendered by the command-owned presentation layer.
type Report struct {
	RepositoryRoot string
	RepositoryName string
	RangeLabel     string
	Target         Target
	Sort           Sort
	RelativeTime   bool
	Query          string
	CommitCount    int
	Files          []PathHeat
	Directories    []PathHeat
}

// BuildReport converts one parsed Git log into the complete heat report data.
func BuildReport(repositoryRoot string, options NormalizedInput, log Log) Report {
	aggregation := Aggregate(log, options.Sort)
	return Report{
		RepositoryRoot: repositoryRoot,
		RepositoryName: repositoryName(repositoryRoot),
		RangeLabel:     rangeLabel(options.Range),
		Target:         options.Target,
		Sort:           options.Sort,
		RelativeTime:   options.RelativeTime,
		Query:          options.Query,
		CommitCount:    aggregation.CommitCount,
		Files:          aggregation.Files,
		Directories:    aggregation.Directories,
	}
}

// Rows returns the rows selected by the command target.
func (report Report) Rows() []PathHeat {
	if report.Target == TargetFiles {
		return report.Files
	}
	return report.Directories
}

// IsEmpty matches the legacy empty-result behavior, which is based on file rows.
func (report Report) IsEmpty() bool {
	return report.CommitCount == 0 || len(report.Files) == 0
}

// ShowCommitCount reports whether the range summary includes a commit count.
func (report Report) ShowCommitCount() bool {
	return report.RangeLabel != "" && !strings.Contains(report.RangeLabel, "commit")
}

// TimeMarks returns one marker per supplied row, choosing the first row on ties.
func TimeMarks(rows []PathHeat) []TimeMark {
	marks := make([]TimeMark, len(rows))
	earliestIndex := -1
	latestIndex := -1
	var earliestEpoch int64
	var latestEpoch int64
	for index, row := range rows {
		if row.ChangedAtEpoch <= 0 {
			continue
		}
		if latestIndex == -1 || row.ChangedAtEpoch > latestEpoch {
			latestIndex = index
			latestEpoch = row.ChangedAtEpoch
		}
		if earliestIndex == -1 || row.ChangedAtEpoch < earliestEpoch {
			earliestIndex = index
			earliestEpoch = row.ChangedAtEpoch
		}
	}
	if latestIndex >= 0 {
		marks[latestIndex] = TimeMarkLatest
	}
	if earliestIndex >= 0 && earliestIndex != latestIndex {
		marks[earliestIndex] = TimeMarkEarliest
	}
	return marks
}

// QueryMatches returns the exact non-overlapping matches used to emphasize paths.
func QueryMatches(filePath, query string) []Match {
	if query == "" {
		return nil
	}
	pathRunes, byteOffsets := lowerPathRunes(filePath)
	queryRunes := lowerQueryRunes(query)
	if len(queryRunes) == 0 || len(pathRunes) < len(queryRunes) {
		return nil
	}
	var matches []Match
	for index := 0; index+len(queryRunes) <= len(pathRunes); {
		if !equalRunes(pathRunes[index:index+len(queryRunes)], queryRunes) {
			index++
			continue
		}
		matches = append(matches, Match{Start: byteOffsets[index], End: byteOffsets[index+len(queryRunes)]})
		index += len(queryRunes)
	}
	return matches
}

func lowerPathRunes(value string) ([]rune, []int) {
	runes := make([]rune, 0, len(value))
	offsets := make([]int, 0, len(value)+1)
	for offset, character := range value {
		runes = append(runes, unicode.ToLower(character))
		offsets = append(offsets, offset)
	}
	offsets = append(offsets, len(value))
	return runes, offsets
}

func lowerQueryRunes(value string) []rune {
	runes := make([]rune, 0, len(value))
	for _, character := range value {
		runes = append(runes, unicode.ToLower(character))
	}
	return runes
}

func equalRunes(left, right []rune) bool {
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// FormatChangedAt presents an absolute or fixed-clock relative time.
func FormatChangedAt(row PathHeat, relative bool, now time.Time) string {
	if row.ChangedAt == "" {
		return "-"
	}
	if !relative || row.ChangedAtEpoch <= 0 {
		return row.ChangedAt
	}
	return relativeTime(time.Unix(row.ChangedAtEpoch, 0), now)
}

func rangeLabel(rangeValue Range) string {
	if rangeValue.IsDayRange() {
		suffix := "s"
		if rangeValue.Days == 1 {
			suffix = ""
		}
		return "last " + integerString(rangeValue.Days) + " day" + suffix
	}
	return "last " + integerString(rangeValue.Limit) + " commits"
}

func repositoryName(repositoryRoot string) string {
	normalized := strings.TrimRight(strings.ReplaceAll(repositoryRoot, "\\", "/"), "/")
	if normalized == "" {
		return repositoryRoot
	}
	return path.Base(normalized)
}

func relativeTime(then, now time.Time) string {
	duration := now.Sub(then)
	future := duration < 0
	if future {
		duration = -duration
	}
	quantity, unit := relativeParts(duration)
	if quantity == 0 {
		return "just now"
	}
	label := integerString(quantity) + " " + unit
	if quantity != 1 {
		label += "s"
	}
	if future {
		return "in " + label
	}
	return label + " ago"
}

func relativeParts(duration time.Duration) (int, string) {
	switch {
	case duration < time.Minute:
		return 0, "second"
	case duration < time.Hour:
		return int(duration / time.Minute), "minute"
	case duration < 24*time.Hour:
		return int(duration / time.Hour), "hour"
	case duration < 30*24*time.Hour:
		return int(duration / (24 * time.Hour)), "day"
	case duration < 365*24*time.Hour:
		return int(duration / (30 * 24 * time.Hour)), "month"
	default:
		return int(duration / (365 * 24 * time.Hour)), "year"
	}
}

func integerString(value int) string {
	return strconv.Itoa(value)
}
