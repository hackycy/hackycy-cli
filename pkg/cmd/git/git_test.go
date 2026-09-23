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
