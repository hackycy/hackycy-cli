package diff

import "testing"

func TestHardExcludedAppliesInventoryRulesAtEveryDepth(t *testing.T) {
	testCases := []struct {
		name      string
		path      string
		directory bool
		want      bool
	}{
		{name: "git directory", path: ".git", directory: true, want: true},
		{name: "nested git file", path: "project/.git", directory: false, want: true},
		{name: "case variant git remains visible", path: ".GIT", directory: true, want: false},
		{name: "macOS metadata file", path: "nested/.DS_Store", directory: false, want: true},
		{name: "macOS sidecar", path: "nested/._notes", directory: false, want: true},
		{name: "macOS system directory", path: ".Spotlight-V100", directory: true, want: true},
		{name: "macOS system file is not a directory exclusion", path: ".Trashes", directory: false, want: false},
		{name: "windows system file case insensitive", path: "nested/ThUmBs.Db", directory: false, want: true},
		{name: "windows system name directory is not a file exclusion", path: "Desktop.ini", directory: true, want: false},
		{name: "windows system directory case insensitive", path: "nested/$RECYCLE.BIN", directory: true, want: true},
		{name: "ordinary path", path: "nested/keep.txt", directory: false, want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := hardExcluded(testCase.path, testCase.directory); got != testCase.want {
				t.Fatalf("hardExcluded(%q, %t) = %t, want %t", testCase.path, testCase.directory, got, testCase.want)
			}
		})
	}
}
