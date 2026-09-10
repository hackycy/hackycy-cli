package pulse

import (
	"reflect"
	"testing"
)

func TestBuildReportGroupsRepositoriesAndSortsCommitsByFormattedDate(t *testing.T) {
	commits := []Commit{
		{Repository: "/workspace/Zed", Author: "Ada", Date: "2026-08-22 09:00:00", Subject: "older"},
		{Repository: "/workspace/alpha", Author: "Ben", Date: "2026-08-23 10:00:00", Subject: "newest"},
		{Repository: "/workspace/Zed", Author: "Cara", Date: "2026-08-23 12:00:00", Subject: "newer"},
		{Repository: "/workspace/alpha", Author: "Dora", Date: "2026-08-23 10:00:00", Subject: "same-time"},
	}

	report := BuildReport(commits)
	if report.CommitCount != 4 {
		t.Fatalf("commit count = %d, want 4", report.CommitCount)
	}
	if report.IsEmpty() {
		t.Fatal("report unexpectedly empty")
	}
	want := []RepositoryReport{
		{
			Path: "/workspace/alpha",
			Commits: []Commit{
				{Repository: "/workspace/alpha", Author: "Ben", Date: "2026-08-23 10:00:00", Subject: "newest"},
				{Repository: "/workspace/alpha", Author: "Dora", Date: "2026-08-23 10:00:00", Subject: "same-time"},
			},
		},
		{
			Path: "/workspace/Zed",
			Commits: []Commit{
				{Repository: "/workspace/Zed", Author: "Cara", Date: "2026-08-23 12:00:00", Subject: "newer"},
				{Repository: "/workspace/Zed", Author: "Ada", Date: "2026-08-22 09:00:00", Subject: "older"},
			},
		},
	}
	if !reflect.DeepEqual(report.Repositories, want) {
		t.Fatalf("repositories = %#v, want %#v", report.Repositories, want)
	}
}

func TestBuildReportKeepsNoCommitResultEmpty(t *testing.T) {
	report := BuildReport(nil)
	if !report.IsEmpty() || report.CommitCount != 0 || len(report.Repositories) != 0 {
		t.Fatalf("empty report = %#v", report)
	}
}
