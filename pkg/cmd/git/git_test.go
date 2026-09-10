package git

import "testing"

func TestNewCmdGitRegistersItsDirectLeaves(t *testing.T) {
	command := NewCmdGit(nil)
	for _, name := range []string{"heat", "pulse", "fork", "cm"} {
		child, _, err := command.Find([]string{name})
		if err != nil || child == command || child.Name() != name {
			t.Fatalf("Find(%q) = (%v, %v)", name, child, err)
		}
	}
}

func TestNormalizeArgumentsDelegatesToCM(t *testing.T) {
	arguments := []string{"git", "cm", "--push", "upstream"}
	got := NormalizeArguments(arguments)
	if len(got) != 3 || got[2] != "--push=upstream" {
		t.Fatalf("NormalizeArguments(%#v) = %#v", arguments, got)
	}
}
