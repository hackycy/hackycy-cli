package diff

import "testing"

func TestExplicitExclusionsPreserveBunGlobVectors(t *testing.T) {
	testCases := []struct {
		name      string
		patterns  []string
		path      string
		directory bool
		want      bool
	}{
		{name: "root wildcard does not match nested path", patterns: []string{"*.tmp"}, path: "nested/a.tmp", want: false},
		{name: "root wildcard matches root path", patterns: []string{"*.tmp"}, path: "a.tmp", want: true},
		{name: "globstar matches root path", patterns: []string{"**/*.tmp"}, path: "a.tmp", want: true},
		{name: "globstar matches nested path", patterns: []string{"**/*.tmp"}, path: "nested/a.tmp", want: true},
		{name: "directory pattern excludes directory descendants", patterns: []string{"cache/**"}, path: "cache", directory: true, want: true},
		{name: "directory pattern does not exclude bare directory without directory context", patterns: []string{"cache/**"}, path: "cache", want: false},
		{name: "directory pattern matches descendant", patterns: []string{"cache/**"}, path: "cache/nested/a.txt", want: true},
		{name: "brace alternatives", patterns: []string{"*.{tmp,log}"}, path: "a.log", want: true},
		{name: "escaped metacharacter", patterns: []string{`literal\*.txt`}, path: "literal*.txt", want: true},
		{name: "invalid pattern matches nothing", patterns: []string{"["}, path: "a.txt", want: false},
		{name: "single negation matches complement", patterns: []string{"!keep.txt"}, path: "drop.txt", want: true},
		{name: "single negation does not match excluded literal", patterns: []string{"!keep.txt"}, path: "keep.txt", want: false},
		{name: "double negation matches literal", patterns: []string{"!!keep.txt"}, path: "keep.txt", want: true},
		{name: "Windows separators normalize to protocol paths", patterns: []string{"nested/*.tmp"}, path: `nested\a.tmp`, want: true},
		{name: "duplicates retain OR behavior", patterns: []string{"*.tmp", "*.tmp"}, path: "a.tmp", want: true},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			matcher := newExclusionMatcher(testCase.patterns)
			if got := matcher.excludes(testCase.path, testCase.directory); got != testCase.want {
				t.Fatalf("excludes(%q, %t) = %t, want %t", testCase.path, testCase.directory, got, testCase.want)
			}
		})
	}
}
