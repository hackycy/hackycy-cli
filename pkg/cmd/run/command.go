package run

import (
	"context"
	"fmt"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options contains the parsed run request and its leaf-owned dependencies.
type Options struct {
	Context          context.Context
	Directory        string
	WorkingDirectory func() (string, error)
	Terminal         *terminal.Runtime
	Reader           FileReader
	Exists           FileExists
	Runner           ChildRunner
}

// NewCmdRun creates the run command with an optional test runner.
func NewCmdRun(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runRun
	}
	return &cobra.Command{
		Use:                "run [path]",
		Short:              "Run package.json scripts",
		DisableFlagParsing: true,
		Args: func(_ *cobra.Command, arguments []string) error {
			_, err := parseRunArguments(arguments)
			return err
		},
		RunE: func(command *cobra.Command, arguments []string) error {
			parsed, err := parseRunArguments(arguments)
			if err != nil {
				return err
			}
			if parsed.help {
				return command.Help()
			}
			return runF(&Options{
				Context:          command.Context(),
				Directory:        parsed.directory,
				WorkingDirectory: factory.WorkingDirectory,
				Terminal:         factory.Terminal,
				Reader:           osRunFileReader{},
				Exists:           osRunPathExists,
				Runner:           newOSRunChildRunner(factory.IOStreams.In, factory.IOStreams.Out, factory.IOStreams.ErrOut),
			})
		},
	}
}

type runChildOutcome struct {
	code int
}

func (outcome *runChildOutcome) Error() string {
	return "run child outcome"
}

func (outcome *runChildOutcome) ExitCode() int {
	return outcome.code
}

type parsedRunArguments struct {
	directory string
	help      bool
}

func parseRunArguments(arguments []string) (parsedRunArguments, error) {
	operands := make([]string, 0, len(arguments))
	parsed := parsedRunArguments{}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			operands = append(operands, arguments[index:]...)
			break
		}
		switch {
		case argument == "--log-level", argument == "--log-format":
			if index+1 == len(arguments) {
				return parsedRunArguments{}, fmt.Errorf("flag needs an argument: %s", argument)
			}
			index++
		case strings.HasPrefix(argument, "--log-level="), strings.HasPrefix(argument, "--log-format="), argument == "--verbose", strings.HasPrefix(argument, "--verbose="), argument == "--quiet", strings.HasPrefix(argument, "--quiet="), argument == "-v":
		default:
			operands = append(operands, argument)
		}
	}
	if len(operands) > 1 {
		return parsedRunArguments{}, fmt.Errorf("accepts at most 1 arg(s), received %d", len(operands))
	}
	if len(operands) == 1 {
		if operands[0] == "--help" || operands[0] == "-h" {
			parsed.help = true
			return parsed, nil
		}
		parsed.directory = operands[0]
	}
	return parsed, nil
}
