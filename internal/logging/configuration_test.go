package logging

import "testing"

func TestParseConfigurationResolvesPrecedenceAndAliases(t *testing.T) {
	tests := []struct {
		name  string
		input ConfigurationInput
		want  Configuration
	}{
		{
			name:  "defaults",
			input: ConfigurationInput{},
			want:  Configuration{Level: Info, Format: TextFormat},
		},
		{
			name: "environment",
			input: ConfigurationInput{LookupEnv: environmentLookup(map[string]string{
				"YCY_LOG_LEVEL":  "warn",
				"YCY_LOG_FORMAT": "json",
			})},
			want: Configuration{Level: Warn, Format: JSONFormat},
		},
		{
			name: "explicit level keeps environment format",
			input: ConfigurationInput{
				LogLevels: []string{"debug"},
				LookupEnv: environmentLookup(map[string]string{"YCY_LOG_LEVEL": "error", "YCY_LOG_FORMAT": "json"}),
			},
			want: Configuration{Level: Debug, Format: JSONFormat},
		},
		{
			name: "explicit format keeps environment level",
			input: ConfigurationInput{
				LogFormats: []string{"text"},
				LookupEnv:  environmentLookup(map[string]string{"YCY_LOG_LEVEL": "warn", "YCY_LOG_FORMAT": "json"}),
			},
			want: Configuration{Level: Warn, Format: TextFormat},
		},
		{
			name:  "verbose",
			input: ConfigurationInput{VerboseCount: 1},
			want:  Configuration{Level: Debug, Format: TextFormat},
		},
		{
			name:  "quiet",
			input: ConfigurationInput{QuietCount: 1},
			want:  Configuration{Level: Error, Format: TextFormat},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseConfiguration(test.input)
			if err != nil || got != test.want {
				t.Fatalf("ParseConfiguration() = (%#v, %v), want (%#v, nil)", got, err, test.want)
			}
		})
	}
}

func TestParseConfigurationRejectsConflictsAndInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		input ConfigurationInput
		want  string
	}{
		{name: "repeated log level", input: ConfigurationInput{LogLevels: []string{"info", "warn"}}, want: "--log-level may be specified only once"},
		{name: "repeated verbose", input: ConfigurationInput{VerboseCount: 2}, want: "--verbose may be specified only once"},
		{name: "repeated quiet", input: ConfigurationInput{QuietCount: 2}, want: "--quiet may be specified only once"},
		{name: "mixed level controls", input: ConfigurationInput{LogLevels: []string{"info"}, VerboseCount: 1}, want: "--log-level, --verbose, and --quiet are mutually exclusive"},
		{name: "repeated format", input: ConfigurationInput{LogFormats: []string{"text", "json"}}, want: "--log-format may be specified only once"},
		{name: "invalid explicit level", input: ConfigurationInput{LogLevels: []string{"loud"}}, want: `invalid log level "loud" (expected debug, info, warn, or error)`},
		{name: "invalid explicit format", input: ConfigurationInput{LogFormats: []string{"yaml"}}, want: `invalid log format "yaml" (expected text or json)`},
		{name: "invalid environment level", input: ConfigurationInput{LookupEnv: environmentLookup(map[string]string{"YCY_LOG_LEVEL": "loud"})}, want: `invalid log level "loud" (expected debug, info, warn, or error)`},
		{name: "invalid environment format", input: ConfigurationInput{LookupEnv: environmentLookup(map[string]string{"YCY_LOG_FORMAT": "yaml"})}, want: `invalid log format "yaml" (expected text or json)`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseConfiguration(test.input)
			if err == nil || err.Error() != test.want {
				t.Fatalf("ParseConfiguration() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseRecordFormat(t *testing.T) {
	if format, err := ParseRecordFormat(" JSON "); err != nil || format != JSONFormat {
		t.Fatalf("ParseRecordFormat() = (%q, %v)", format, err)
	}
}

func environmentLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
