package logging

import (
	"fmt"
	"strings"
)

// RecordFormat selects the stable rendering schema for Diagnostic Records.
type RecordFormat string

const (
	// TextFormat emits one human-readable Diagnostic Record per line.
	TextFormat RecordFormat = "text"
	// JSONFormat emits one NDJSON Diagnostic Record per line.
	JSONFormat RecordFormat = "json"
)

// Configuration is the resolved diagnostic configuration for one invocation.
type Configuration struct {
	Level  Level
	Format RecordFormat
}

// ConfigurationInput keeps CLI occurrence counts separate from environment
// fallback so mutually exclusive controls cannot silently choose a winner.
type ConfigurationInput struct {
	LogLevels    []string
	LogFormats   []string
	VerboseCount int
	QuietCount   int
	LookupEnv    func(string) (string, bool)
}

// ParseConfiguration resolves CLI controls, environment fallbacks, and defaults.
func ParseConfiguration(input ConfigurationInput) (Configuration, error) {
	if len(input.LogLevels) > 1 {
		return Configuration{}, fmt.Errorf("--log-level may be specified only once")
	}
	if input.VerboseCount > 1 {
		return Configuration{}, fmt.Errorf("--verbose may be specified only once")
	}
	if input.QuietCount > 1 {
		return Configuration{}, fmt.Errorf("--quiet may be specified only once")
	}
	if len(input.LogLevels)+input.VerboseCount+input.QuietCount > 1 {
		return Configuration{}, fmt.Errorf("--log-level, --verbose, and --quiet are mutually exclusive")
	}
	if len(input.LogFormats) > 1 {
		return Configuration{}, fmt.Errorf("--log-format may be specified only once")
	}

	configuration := Configuration{Level: Info, Format: TextFormat}
	if value, ok := lookupConfigurationEnv(input.LookupEnv, "YCY_LOG_LEVEL"); ok {
		level, err := ParseLevel(value)
		if err != nil {
			return Configuration{}, err
		}
		configuration.Level = level
	}
	if value, ok := lookupConfigurationEnv(input.LookupEnv, "YCY_LOG_FORMAT"); ok {
		format, err := ParseRecordFormat(value)
		if err != nil {
			return Configuration{}, err
		}
		configuration.Format = format
	}

	switch {
	case len(input.LogLevels) == 1:
		level, err := parseExplicitLevel(input.LogLevels[0])
		if err != nil {
			return Configuration{}, err
		}
		configuration.Level = level
	case input.VerboseCount == 1:
		configuration.Level = Debug
	case input.QuietCount == 1:
		configuration.Level = Error
	}
	if len(input.LogFormats) == 1 {
		format, err := parseExplicitFormat(input.LogFormats[0])
		if err != nil {
			return Configuration{}, err
		}
		configuration.Format = format
	}
	return configuration, nil
}

// ParseRecordFormat accepts the public diagnostic format spelling.
func ParseRecordFormat(value string) (RecordFormat, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(TextFormat):
		return TextFormat, nil
	case string(JSONFormat):
		return JSONFormat, nil
	default:
		return TextFormat, fmt.Errorf("invalid log format %q (expected text or json)", value)
	}
}

func parseExplicitLevel(value string) (Level, error) {
	if strings.TrimSpace(value) == "" {
		return Info, fmt.Errorf("invalid log level %q (expected debug, info, warn, or error)", value)
	}
	return ParseLevel(value)
}

func parseExplicitFormat(value string) (RecordFormat, error) {
	if strings.TrimSpace(value) == "" {
		return TextFormat, fmt.Errorf("invalid log format %q (expected text or json)", value)
	}
	return ParseRecordFormat(value)
}

func lookupConfigurationEnv(lookup func(string) (string, bool), key string) (string, bool) {
	if lookup == nil {
		return "", false
	}
	return lookup(key)
}
