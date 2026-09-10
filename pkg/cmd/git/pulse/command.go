package pulse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// Options contains the parsed git pulse request and its leaf-owned adapters.
type Options struct {
	Context          context.Context
	Directory        string
	Days             *int
	WorkingDirectory func() (string, error)
	Terminal         *terminal.Runtime
	Git              *gitprocess.Runner
	Now              func() time.Time
	Width            int
}

// NewCmdPulse creates the git pulse leaf with an optional test runner.
func NewCmdPulse(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runPulse
	}
	var days string
	command := &cobra.Command{
		Use:   "pulse [directory]",
		Short: "Show recent Git activity across repositories",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(command *cobra.Command, arguments []string) error {
			if factory == nil || factory.WorkingDirectory == nil || factory.Terminal == nil || factory.GitRunner == nil || factory.Now == nil {
				return errors.New("git pulse Factory is incomplete")
			}
			options := &Options{
				Context:          command.Context(),
				WorkingDirectory: factory.WorkingDirectory,
				Terminal:         factory.Terminal,
				Git:              factory.GitRunner(),
				Now:              factory.Now,
			}
			if len(arguments) == 1 {
				options.Directory = arguments[0]
			}
			if command.Flags().Changed("days") {
				parsed, err := parseInteger(days)
				if err != nil {
					return err
				}
				options.Days = &parsed
			}
			options.Width = pulseTerminalWidth(factory.IOStreams.Out)
			return runF(options)
		},
	}
	command.Flags().StringVar(&days, "days", "", "Number of days to search")
	return command
}

func pulseTerminalWidth(output io.Writer) int {
	file, ok := output.(*os.File)
	if !ok || file == nil {
		return 0
	}
	width, _, err := term.GetSize(int(file.Fd()))
	if err != nil || width <= 0 {
		return 0
	}
	return width
}

func parseInteger(value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, fmt.Errorf("'%s' is not a valid integer", value)
	}
	end := 0
	if trimmed[0] == '+' || trimmed[0] == '-' {
		end++
	}
	for end < len(trimmed) && trimmed[end] >= '0' && trimmed[end] <= '9' {
		end++
	}
	if end == 0 || (end == 1 && (trimmed[0] == '+' || trimmed[0] == '-')) {
		return 0, fmt.Errorf("'%s' is not a valid integer", value)
	}
	parsed, err := strconv.ParseInt(trimmed[:end], 10, 0)
	if err != nil {
		return 0, fmt.Errorf("'%s' is not a valid integer", value)
	}
	return int(parsed), nil
}
