package pulse

import (
	"sort"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// RepositoryReport is one sorted repository group in a pulse report.
type RepositoryReport struct {
	Path    string
	Commits []Commit
}

// Report is the terminal-independent semantic result of git pulse.
type Report struct {
	CommitCount  int
	Repositories []RepositoryReport
}

// IsEmpty reports whether the selected range produced any commits.
func (report Report) IsEmpty() bool {
	return report.CommitCount == 0
}

// BuildReport groups commits by absolute repository path and sorts the legacy presentation order.
func BuildReport(commits []Commit) Report {
	groups := make(map[string][]Commit)
	for _, commit := range commits {
		groups[commit.Repository] = append(groups[commit.Repository], commit)
	}

	paths := make([]string, 0, len(groups))
	for repository := range groups {
		paths = append(paths, repository)
	}
	collator := collate.New(language.AmericanEnglish)
	sort.Slice(paths, func(left, right int) bool {
		return collator.CompareString(paths[left], paths[right]) < 0
	})

	repositories := make([]RepositoryReport, 0, len(paths))
	for _, repository := range paths {
		group := append([]Commit(nil), groups[repository]...)
		sort.SliceStable(group, func(left, right int) bool {
			return group[left].Date > group[right].Date
		})
		repositories = append(repositories, RepositoryReport{Path: repository, Commits: group})
	}
	return Report{CommitCount: len(commits), Repositories: repositories}
}
