package diff

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

type exclusionMatcher struct {
	patterns []exclusionPattern
}

type exclusionPattern struct {
	pattern string
	negated bool
}

func newExclusionMatcher(patterns []string) exclusionMatcher {
	compiled := make([]exclusionPattern, 0, len(patterns))
	for _, rawPattern := range patterns {
		pattern := rawPattern
		negationCount := 0
		for strings.HasPrefix(pattern, "!") {
			pattern = strings.TrimPrefix(pattern, "!")
			negationCount++
		}
		compiled = append(compiled, exclusionPattern{
			pattern: pattern,
			negated: negationCount%2 == 1,
		})
	}
	return exclusionMatcher{patterns: compiled}
}

func (matcher exclusionMatcher) excludes(comparisonPath string, directory bool) bool {
	comparisonPath = strings.ReplaceAll(comparisonPath, "\\", "/")
	for _, pattern := range matcher.patterns {
		if pattern.pattern == "" {
			continue
		}
		if !directory && strings.HasSuffix(pattern.pattern, "/**") && comparisonPath == strings.TrimSuffix(pattern.pattern, "/**") {
			continue
		}
		matches, err := doublestar.Match(pattern.pattern, comparisonPath)
		if err != nil {
			continue
		}
		if !matches && directory {
			matches, err = doublestar.Match(pattern.pattern, comparisonPath+"/")
			if err != nil {
				continue
			}
		}
		if pattern.negated {
			matches = !matches
		}
		if matches {
			return true
		}
	}
	return false
}
