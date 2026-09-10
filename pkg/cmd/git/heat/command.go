package heat

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

// Options contains the parsed git heat request and its leaf-owned adapters.
type Options struct {
	Context      context.Context
	Limit        *int
	Days         *int
	Target       Target
	Sort         Sort
	RelativeTime bool
	Query        string
	Width        int
	Terminal     *terminal.Runtime
	Git          *gitprocess.Runner
	Now          func() time.Time
}

// NewCmdHeat creates the git heat leaf with an optional test runner.
func NewCmdHeat(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runHeat
	}
	var limit string
	var days string
	var target string
	var sort string
	var relativeTime bool
	var query string
	command := &cobra.Command{
		Use:   "heat",
		Short: "Show frequently changed files and directories in recent commits",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.Terminal == nil || factory.GitRunner == nil || factory.Now == nil {
				return errors.New("git heat Factory is incomplete")
			}
			options := &Options{
				Context:      command.Context(),
				Target:       TargetDirectories,
				Sort:         SortPath,
				RelativeTime: relativeTime,
				Query:        query,
				Terminal:     factory.Terminal,
				Git:          factory.GitRunner(),
				Now:          factory.Now,
			}
			if command.Flags().Changed("limit") {
				parsed, err := parseInteger(limit)
				if err != nil {
					return err
				}
				options.Limit = &parsed
			}
			if command.Flags().Changed("days") {
				parsed, err := parseInteger(days)
				if err != nil {
					return err
				}
				options.Days = &parsed
			}
			if command.Flags().Changed("type") {
				parsed, err := parseTarget(target)
				if err != nil {
					return err
				}
				options.Target = parsed
			}
			if command.Flags().Changed("sort") {
				parsed, err := parseSort(sort)
				if err != nil {
					return err
				}
				options.Sort = parsed
			}
			options.Width = heatTerminalWidth(factory.IOStreams.Out)
			return runF(options)
		},
	}
	command.Flags().StringVarP(&limit, "limit", "n", "", "Number of recent commits to inspect")
	command.Flags().StringVarP(&days, "days", "d", "", "Number of recent days to inspect")
	command.Flags().StringVarP(&target, "type", "t", "", "Report type: files or directories")
	command.Flags().StringVarP(&sort, "sort", "s", "", "Sort by count or path")
	command.Flags().BoolVarP(&relativeTime, "relative-time", "r", false, "Show Changed at as relative time")
	command.Flags().StringVarP(&query, "query", "q", "", "Highlight files or directories that contain text")
	return command
}

// heatTerminalWidth captures the result stream's current width for the
// command-owned responsive Rich projection. A non-file writer has no reliable
// terminal geometry, so the renderer will use its stable fallback width.
func heatTerminalWidth(output io.Writer) int {
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

func parseTarget(value string) (Target, error) {
	if value == string(TargetFiles) {
		return TargetFiles, nil
	}
	if value == string(TargetDirectories) || value == "dirs" {
		return TargetDirectories, nil
	}
	return "", fmt.Errorf("'%s' is not a valid report type. Use files or directories.", value)
}

func parseSort(value string) (Sort, error) {
	if value == string(SortCount) {
		return SortCount, nil
	}
	if value == string(SortPath) {
		return SortPath, nil
	}
	return "", fmt.Errorf("'%s' is not a valid sort. Use count or path.", value)
}
