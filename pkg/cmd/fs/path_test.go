package fs

import (
	"errors"
	"testing"
)

func TestParseWorkspacePathNormalizesRootRelativeSegments(t *testing.T) {
	tests := map[string]struct {
		input string
		want  string
	}{
		"empty path names the root":          {input: "", want: ""},
		"duplicate separators are discarded": {input: "projects//go///main.go", want: "projects/go/main.go"},
		"Unicode names remain visible":       {input: "目录/报告.txt", want: "目录/报告.txt"},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := ParseWorkspacePath(test.input)
			if err != nil {
				t.Fatalf("ParseWorkspacePath(%q) error = %v", test.input, err)
			}
			if got.String() != test.want {
				t.Fatalf("ParseWorkspacePath(%q) = %q, want %q", test.input, got, test.want)
			}
		})
	}
}

func TestParseWorkspacePathRejectsNonProtocolPaths(t *testing.T) {
	for _, input := range []string{
		"/absolute",
		"C:relative",
		"z:/absolute",
		"nested\\name",
		"nul\x00name",
		string([]byte{'n', 'a', 'm', 'e', 0xff}),
		".",
		"nested/./name",
		"..",
		"nested/../name",
	} {
		if _, err := ParseWorkspacePath(input); !errors.Is(err, ErrInvalidWorkspacePath) {
			t.Errorf("ParseWorkspacePath(%q) error = %v, want ErrInvalidWorkspacePath", input, err)
		}
	}
}
