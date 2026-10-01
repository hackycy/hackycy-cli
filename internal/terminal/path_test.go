package terminal

import "testing"

func TestPathLabelRedactsAbsolutePathsAcrossPlatforms(t *testing.T) {
	for _, test := range []struct{ value, want string }{
		{"", "workspace"},
		{"/private/work/project", "project"},
		{"/private/work/project/", "project"},
		{`C:\private\work\project`, "project"},
		{"C:/private/work/project/", "project"},
		{`\\server\share\project`, "project"},
		{`\private\project`, "project"},
		{"/", "workspace"},
		{`C:\`, "workspace"},
		{"../../private/project", "../../private/project"},
		{`relative\project`, "relative/project"},
	} {
		t.Run(test.value, func(t *testing.T) {
			if got := PathLabel(test.value, "workspace"); got != test.want {
				t.Fatalf("PathLabel(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}
