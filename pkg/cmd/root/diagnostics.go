package root

import "strings"

type diagnosticControls struct {
	logLevels    []string
	logFormats   []string
	verboseCount int
	quietCount   int
}

func normalizeDiagnosticAliases(arguments []string) []string {
	normalized := make([]string, 0, len(arguments))
	for index, argument := range arguments {
		if argument == "--" {
			normalized = append(normalized, arguments[index:]...)
			break
		}
		if isGitHeatQueryShorthand(arguments, index) && strings.HasPrefix(argument, "-q") {
			normalized = append(normalized, argument)
			continue
		}
		if argument == "-q" {
			normalized = append(normalized, "--quiet")
			continue
		}
		if strings.HasPrefix(argument, "-q=") {
			normalized = append(normalized, "--quiet"+strings.TrimPrefix(argument, "-q"))
			continue
		}
		if bundle, ok := diagnosticShortFlagBundle(argument); ok {
			normalized = append(normalized, bundle...)
			continue
		}
		normalized = append(normalized, argument)
	}
	return normalized
}

func diagnosticShortFlagBundle(argument string) ([]string, bool) {
	if len(argument) < 3 || !strings.HasPrefix(argument, "-") || strings.HasPrefix(argument, "--") {
		return nil, false
	}
	bundle := make([]string, 0, len(argument)-1)
	for _, shorthand := range argument[1:] {
		switch shorthand {
		case 'v':
			bundle = append(bundle, "-v")
		case 'q':
			bundle = append(bundle, "--quiet")
		default:
			return nil, false
		}
	}
	return bundle, true
}

func isGitHeatQueryShorthand(arguments []string, end int) bool {
	var commands []string
	for index := 0; index < end; index++ {
		argument := arguments[index]
		if argument == "--" {
			return false
		}
		if argument == "--log-level" || argument == "--log-format" {
			index++
			continue
		}
		if strings.HasPrefix(argument, "--") || strings.HasPrefix(argument, "-") {
			continue
		}
		commands = append(commands, argument)
	}
	return len(commands) >= 2 && commands[0] == "git" && commands[1] == "heat"
}

func collectDiagnosticControls(arguments []string) diagnosticControls {
	controls := diagnosticControls{}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			break
		}
		switch {
		case argument == "--log-level":
			controls.logLevels = append(controls.logLevels, nextDiagnosticValue(arguments, &index))
		case strings.HasPrefix(argument, "--log-level="):
			controls.logLevels = append(controls.logLevels, strings.TrimPrefix(argument, "--log-level="))
		case argument == "--log-format":
			controls.logFormats = append(controls.logFormats, nextDiagnosticValue(arguments, &index))
		case strings.HasPrefix(argument, "--log-format="):
			controls.logFormats = append(controls.logFormats, strings.TrimPrefix(argument, "--log-format="))
		case argument == "--verbose" || strings.HasPrefix(argument, "--verbose=") || argument == "-v":
			controls.verboseCount++
		case argument == "--quiet" || strings.HasPrefix(argument, "--quiet="):
			controls.quietCount++
		}
	}
	return controls
}

func nextDiagnosticValue(arguments []string, index *int) string {
	if *index+1 == len(arguments) {
		return ""
	}
	*index = *index + 1
	return arguments[*index]
}
