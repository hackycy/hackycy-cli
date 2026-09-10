package heat

import (
	"reflect"
	"testing"
	"time"
)

func TestBuildReportPreservesSummaryRowsAndEmptyBehavior(t *testing.T) {
	report := BuildReport("C:\\workspace\\repo", NormalizedInput{
		Range:        Range{Days: 2},
		Target:       TargetDirectories,
		Sort:         SortCount,
		RelativeTime: true,
		Query:        " api ",
	}, Log{
		CommitCount: 2,
		Changes: []Change{
			{Kind: ChangeModified, Path: "api/client.go", ChangedAt: "2024-03-01 12:00:00", ChangedAtEpoch: 1},
			{Kind: ChangeAdded, Path: "README.md", ChangedAt: "2024-03-01 13:00:00", ChangedAtEpoch: 2},
		},
	})

	if report.RepositoryName != "repo" {
		t.Fatalf("repository name = %q", report.RepositoryName)
	}
	if report.RangeLabel != "last 2 days" || !report.ShowCommitCount() {
		t.Fatalf("range summary = %q, show commits = %t", report.RangeLabel, report.ShowCommitCount())
	}
	if !reflect.DeepEqual(heatPaths(report.Rows()), []string{".", "api"}) {
		t.Fatalf("rows = %#v", report.Rows())
	}
	if report.IsEmpty() {
		t.Fatal("report is empty")
	}

	commitRange := BuildReport("/repo", NormalizedInput{Range: Range{Limit: 3}, Target: TargetFiles, Sort: SortPath}, Log{})
	if commitRange.RangeLabel != "last 3 commits" || commitRange.ShowCommitCount() {
		t.Fatalf("commit range = %#v", commitRange)
	}
	if !commitRange.IsEmpty() {
		t.Fatal("empty log report is not empty")
	}
}

func TestTimeMarksUsesFirstRowForTiesAndSkipsUnknownTimes(t *testing.T) {
	rows := []PathHeat{
		{Path: "first", ChangedAtEpoch: 10},
		{Path: "tie", ChangedAtEpoch: 10},
		{Path: "earliest", ChangedAtEpoch: 5},
		{Path: "unknown", ChangedAtEpoch: 0},
	}
	if got, want := TimeMarks(rows), []TimeMark{TimeMarkLatest, "", TimeMarkEarliest, ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("marks = %#v, want %#v", got, want)
	}
	if got, want := TimeMarks([]PathHeat{{Path: "only", ChangedAtEpoch: 1}}), []TimeMark{TimeMarkLatest}; !reflect.DeepEqual(got, want) {
		t.Fatalf("single mark = %#v, want %#v", got, want)
	}
}

func TestQueryMatchesIsCaseInsensitiveAndNonOverlapping(t *testing.T) {
	if got, want := QueryMatches("API/api/apiary", "api"), []Match{{Start: 0, End: 3}, {Start: 4, End: 7}, {Start: 8, End: 11}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("matches = %#v, want %#v", got, want)
	}
	if got := QueryMatches("aaaa", "aa"); !reflect.DeepEqual(got, []Match{{Start: 0, End: 2}, {Start: 2, End: 4}}) {
		t.Fatalf("overlap matches = %#v", got)
	}
	if got := QueryMatches("path", ""); got != nil {
		t.Fatalf("empty query matches = %#v", got)
	}
}

func TestQueryMatchesPreservesUnicodePathBoundaries(t *testing.T) {
	testCases := []struct {
		path  string
		query string
		want  []Match
	}{
		{path: "中API名", query: "api", want: []Match{{Start: len("中"), End: len("中API")}}},
		{path: "İx", query: "i", want: []Match{{Start: 0, End: len("İ")}}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			if got := QueryMatches(testCase.path, testCase.query); !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("QueryMatches(%q, %q) = %#v, want %#v", testCase.path, testCase.query, got, testCase.want)
			}
		})
	}
}

func TestFormatChangedAtUsesEpochTimeAndInjectedClock(t *testing.T) {
	now := time.Date(2024, time.March, 10, 0, 5, 0, 0, time.FixedZone("DST", -7*60*60))
	row := PathHeat{ChangedAt: "2024-03-09 23:05:00", ChangedAtEpoch: now.Add(-time.Hour).Unix()}
	if got := FormatChangedAt(row, false, now); got != "2024-03-09 23:05:00" {
		t.Fatalf("absolute time = %q", got)
	}
	if got := FormatChangedAt(row, true, now); got != "1 hour ago" {
		t.Fatalf("relative time = %q", got)
	}
	if got := FormatChangedAt(PathHeat{}, true, now); got != "-" {
		t.Fatalf("missing time = %q", got)
	}
	if got := FormatChangedAt(PathHeat{ChangedAt: "known", ChangedAtEpoch: now.Add(time.Hour).Unix()}, true, now); got != "in 1 hour" {
		t.Fatalf("future time = %q", got)
	}
}
