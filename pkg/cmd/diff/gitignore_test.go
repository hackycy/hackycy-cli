package diff

import "testing"

func TestTargetGitIgnoreMatcherAppliesHierarchicalRules(t *testing.T) {
	matcher := newTargetIgnoreMatcher(map[string]string{
		"":       "*.tmp\nbaseline-only/\nroot-only.txt\n",
		"nested": "!keep.tmp\nlocal-only.txt\n",
	})

	testCases := []struct {
		name      string
		path      string
		directory bool
		want      bool
	}{
		{name: "root rule matches nested file", path: "nested/drop.tmp", want: true},
		{name: "nested negation clears ancestor exclusion", path: "nested/keep.tmp", want: false},
		{name: "target rule applies to baseline-only subtree", path: "baseline-only/secret.txt", want: true},
		{name: "nested rule does not apply outside its base", path: "elsewhere/local-only.txt", want: false},
		{name: "nested rule applies beneath its base", path: "nested/local-only.txt", want: true},
		{name: "directory-only root rule applies to directory", path: "baseline-only", directory: true, want: true},
		{name: "ordinary path remains visible", path: "nested/keep.txt", want: false},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := matcher.ignored(testCase.path, testCase.directory); got != testCase.want {
				t.Fatalf("ignored(%q, %t) = %t, want %t", testCase.path, testCase.directory, got, testCase.want)
			}
		})
	}
}

func TestTargetGitIgnoreMatcherPreservesEscapedMarkersAndTrailingSpaces(t *testing.T) {
	matcher := newTargetIgnoreMatcher(map[string]string{
		"": "# comment\n\\!literal\n\\#literal\ntrimmed \nkept\\ \n",
	})

	testCases := []struct {
		path string
		want bool
	}{
		{path: "!literal", want: true},
		{path: "#literal", want: true},
		{path: "trimmed", want: true},
		{path: "trimmed ", want: false},
		{path: "kept ", want: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.path, func(t *testing.T) {
			if got := matcher.ignored(testCase.path, false); got != testCase.want {
				t.Fatalf("ignored(%q) = %t, want %t", testCase.path, got, testCase.want)
			}
		})
	}
}

func TestTargetGitIgnoreMatcherHandlesAnchoringDirectoriesAndGlobstars(t *testing.T) {
	matcher := newTargetIgnoreMatcher(map[string]string{
		"": "/root.txt\nlogs/\n**/generated.txt\n",
	})

	testCases := []struct {
		name      string
		path      string
		directory bool
		want      bool
	}{
		{name: "anchored root path", path: "root.txt", want: true},
		{name: "anchored path does not match nested", path: "nested/root.txt", want: false},
		{name: "directory rule matches directory", path: "logs", directory: true, want: true},
		{name: "directory rule covers descendants", path: "logs/current.txt", want: true},
		{name: "globstar matches root", path: "generated.txt", want: true},
		{name: "globstar matches nested", path: "nested/generated.txt", want: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := matcher.ignored(testCase.path, testCase.directory); got != testCase.want {
				t.Fatalf("ignored(%q, %t) = %t, want %t", testCase.path, testCase.directory, got, testCase.want)
			}
		})
	}
}

func TestTargetGitIgnoreMatcherNormalizesRuleTextLikeNode(t *testing.T) {
	matcher := newTargetIgnoreMatcher(map[string]string{
		"": "\ufeffignored.txt\r\nna\u00efve.txt\r\ninvalid\xff.txt\n",
	})

	testCases := []string{"ignored.txt", "na\u00efve.txt", "invalid\ufffd.txt"}
	for _, path := range testCases {
		t.Run(path, func(t *testing.T) {
			if !matcher.ignored(path, false) {
				t.Fatalf("ignored(%q) = false, want true", path)
			}
		})
	}
}
