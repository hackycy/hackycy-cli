package git

import "github.com/hackycy/hackycy-cli/pkg/cmd/git/cm"

// NormalizeArguments preserves domain-specific syntax before Cobra parses it.
func NormalizeArguments(arguments []string) []string {
	return cm.NormalizeArguments(arguments)
}
