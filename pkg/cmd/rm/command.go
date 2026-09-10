package rm

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// Options contains the parsed rm request and its leaf-owned dependencies.
type Options struct {
	Context          context.Context
	Paths            []string
	Force            bool
	Depth            *int
	WorkingDirectory func() (string, error)
	Terminal         *terminal.Runtime
	Remover          PathRemover
}

// NewCmdRM creates the rm command with an optional test runner.
func NewCmdRM(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runRM
	}
	var force bool
	var depth string
	rmCommand := &cobra.Command{
		Use:   "rm [paths...]",
		Short: "Remove files/dirs, or smartly clean project artifacts when no path given",
		Args:  cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, arguments []string) error {
			options := &Options{
				Context:          command.Context(),
				Paths:            append([]string(nil), arguments...),
				Force:            force,
				WorkingDirectory: factory.WorkingDirectory,
				Terminal:         factory.Terminal,
				Remover:          osRMRemover{},
			}
			if command.Flags().Changed("depth") {
				parsed, err := parseRMDepth(depth)
				if err != nil {
					return err
				}
				options.Depth = &parsed
			}
			return runF(options)
		},
	}
	rmCommand.Flags().BoolVarP(&force, "force", "f", false, "Skip confirmation prompt")
	rmCommand.Flags().StringVarP(&depth, "depth", "d", "", "Smart scan depth (default: 5)")
	return rmCommand
}

func parseRMDepth(value string) (int, error) {
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
