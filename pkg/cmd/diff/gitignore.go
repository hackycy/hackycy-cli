package diff

import (
	"bufio"
	"strings"

	gitignore "github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

type targetIgnoreMatcher struct {
	rules map[string][]gitignore.Pattern
}

func newTargetIgnoreMatcher(sources map[string]string) targetIgnoreMatcher {
	rules := make(map[string][]gitignore.Pattern, len(sources))
	matcher := targetIgnoreMatcher{rules: rules}
	for basePath, source := range sources {
		matcher.add(basePath, source)
	}
	return matcher
}

func (matcher targetIgnoreMatcher) add(basePath, source string) {
	basePath = strings.Trim(strings.ReplaceAll(basePath, "\\", "/"), "/")
	matcher.rules[basePath] = parseIgnorePatterns(source)
}

func (matcher targetIgnoreMatcher) ignored(comparisonPath string, directory bool) bool {
	comparisonPath = strings.Trim(strings.ReplaceAll(comparisonPath, "\\", "/"), "/")
	if comparisonPath == "" {
		return false
	}

	directoryPath := parentComparisonPath(comparisonPath)
	ancestors := []string{""}
	for _, part := range strings.Split(directoryPath, "/") {
		if part == "" {
			continue
		}
		if previous := ancestors[len(ancestors)-1]; previous != "" {
			ancestors = append(ancestors, previous+"/"+part)
		} else {
			ancestors = append(ancestors, part)
		}
	}

	ignored := false
	for _, basePath := range ancestors {
		relativePath, ok := comparisonPathRelativeTo(comparisonPath, basePath)
		if !ok || relativePath == "" {
			continue
		}
		result := matchIgnorePatterns(matcher.rules[basePath], strings.Split(relativePath, "/"), directory)
		switch result {
		case gitignore.Exclude:
			ignored = true
		case gitignore.Include:
			ignored = false
		}
	}
	return ignored
}

func parseIgnorePatterns(source string) []gitignore.Pattern {
	patterns := make([]gitignore.Pattern, 0)
	scanner := bufio.NewScanner(strings.NewReader(source))
	firstLine := true
	for scanner.Scan() {
		line := strings.ToValidUTF8(scanner.Text(), "\uFFFD")
		if firstLine {
			line = strings.TrimPrefix(line, "\ufeff")
			firstLine = false
		}
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(normalizeIgnorePattern(line), nil))
	}
	return patterns
}

// normalizeIgnorePattern keeps Gitignore escapes portable across filepath.Match
// implementations, where Windows treats backslash as a path separator.
func normalizeIgnorePattern(pattern string) string {
	if strings.HasPrefix(pattern, `\!`) {
		pattern = `[!]` + pattern[2:]
	}
	if strings.HasPrefix(pattern, `\#`) {
		pattern = `[#]` + pattern[2:]
	}
	if strings.HasSuffix(pattern, `\ `) {
		pattern = strings.TrimSuffix(pattern, `\ `) + `[ ]`
	}
	return pattern
}

func matchIgnorePatterns(patterns []gitignore.Pattern, path []string, directory bool) gitignore.MatchResult {
	for index := len(patterns) - 1; index >= 0; index-- {
		if result := patterns[index].Match(path, directory); result != gitignore.NoMatch {
			return result
		}
	}
	return gitignore.NoMatch
}

func parentComparisonPath(comparisonPath string) string {
	if slash := strings.LastIndexByte(comparisonPath, '/'); slash >= 0 {
		return comparisonPath[:slash]
	}
	return ""
}

func comparisonPathRelativeTo(comparisonPath, basePath string) (string, bool) {
	if basePath == "" {
		return comparisonPath, true
	}
	prefix := basePath + "/"
	if !strings.HasPrefix(comparisonPath, prefix) {
		return "", false
	}
	return strings.TrimPrefix(comparisonPath, prefix), true
}
