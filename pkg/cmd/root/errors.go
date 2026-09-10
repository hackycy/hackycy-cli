package root

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func normalizeCobraError(root *cobra.Command, arguments []string, err error) error {
	if err == nil || root == nil {
		return err
	}

	message := err.Error()
	if typed, commandPath, ok := unknownCommandDetails(message); ok {
		parent := commandAtPath(root, commandPath)
		if parent == nil {
			parent = root
		}
		return recoveryError(
			fmt.Sprintf("unknown command '%s'", actionableToken(typed)),
			parent,
			directCommandCandidates(parent, typed),
			false,
		)
	}
	if typed, ok := unknownFlagName(message); ok {
		command := commandForArguments(root, arguments)
		return recoveryError(message, command, directFlagCandidates(command, typed), true)
	}
	return err
}

func unknownCommandDetails(message string) (typed, commandPath string, ok bool) {
	const prefix = "unknown command \""
	if !strings.HasPrefix(message, prefix) {
		return "", "", false
	}
	remainder := strings.TrimPrefix(message, prefix)
	end := strings.Index(remainder, "\" for \"")
	if end < 0 {
		return "", "", false
	}
	typed = remainder[:end]
	remainder = remainder[end+len("\" for \""):]
	end = strings.IndexByte(remainder, '"')
	if end < 0 {
		return "", "", false
	}
	return typed, remainder[:end], true
}

func unknownFlagName(message string) (string, bool) {
	const prefix = "unknown flag: --"
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(message, prefix)
	if name == "" || strings.ContainsAny(name, "\r\n") {
		return "", false
	}
	return name, true
}

func recoveryError(problem string, command *cobra.Command, candidates []string, flag bool) error {
	commandPath := "ycy"
	if command != nil && command.CommandPath() != "" {
		commandPath = command.CommandPath()
	}
	if len(candidates) != 1 {
		return errors.New(problem + "; Run '" + commandPath + " --help' for usage.")
	}
	candidate := candidates[0]
	if flag {
		candidate = "--" + candidate
	} else {
		commandPath += " " + candidate
	}
	return errors.New(problem + "; did you mean '" + candidate + "'? Run '" + commandPath + " --help' for usage.")
}

func commandAtPath(root *cobra.Command, path string) *cobra.Command {
	if root == nil {
		return nil
	}
	if root.CommandPath() == path {
		return root
	}
	for _, child := range root.Commands() {
		if found := commandAtPath(child, path); found != nil {
			return found
		}
	}
	return nil
}

func commandForArguments(root *cobra.Command, arguments []string) *cobra.Command {
	command, _, _ := root.Find(arguments)
	if command == nil {
		return root
	}
	return command
}

func directCommandCandidates(command *cobra.Command, typed string) []string {
	if command == nil {
		return nil
	}
	minimumDistance := command.SuggestionsMinimumDistance
	if minimumDistance == 0 {
		minimumDistance = 2
	}
	command.SuggestionsMinimumDistance = minimumDistance
	suggestions := command.SuggestionsFor(typed)
	return uniqueCandidates(suggestions)
}

func directFlagCandidates(command *cobra.Command, typed string) []string {
	if command == nil {
		return nil
	}
	var candidates []string
	visitFlags := func(flags *pflag.FlagSet) {
		flags.VisitAll(func(flag *pflag.Flag) {
			if highConfidenceFlagMatch(typed, flag.Name) {
				candidates = append(candidates, flag.Name)
			}
		})
	}
	visitFlags(command.LocalFlags())
	visitFlags(command.InheritedFlags())
	return uniqueCandidates(candidates)
}

func highConfidenceFlagMatch(typed, candidate string) bool {
	typed = strings.ToLower(strings.TrimSpace(typed))
	candidate = strings.ToLower(strings.TrimSpace(candidate))
	if typed == "" || candidate == "" {
		return false
	}
	if len(typed) >= 2 && strings.HasPrefix(candidate, typed) {
		return true
	}
	distance := levenshteinDistance(typed, candidate)
	return distance == 1 || (distance == 2 && utf8.RuneCountInString(typed) >= 5 && utf8.RuneCountInString(candidate) >= 5)
}

func levenshteinDistance(left, right string) int {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	previous := make([]int, len(rightRunes)+1)
	current := make([]int, len(rightRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range leftRunes {
		current[0] = leftIndex + 1
		for rightIndex, rightRune := range rightRunes {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[rightIndex+1] = minDistance(
				current[rightIndex]+1,
				previous[rightIndex+1]+1,
				previous[rightIndex]+cost,
			)
		}
		previous, current = current, previous
	}
	return previous[len(rightRunes)]
}

func minDistance(values ...int) int {
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}

func uniqueCandidates(candidates []string) []string {
	seen := make(map[string]struct{}, len(candidates))
	unique := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		unique = append(unique, candidate)
	}
	return unique
}

func actionableToken(value string) string {
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(value)
}
