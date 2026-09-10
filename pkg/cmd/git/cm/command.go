package cm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/hackycy/hackycy-cli/internal/gitprocess"
	"github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

// ConfigStore resolves Git CM's decrypted provider configuration at execution time.
type ConfigStore func() (ProfileResolver, error)

// Options contains the parsed Git CM request and its leaf-owned adapters.
type Options struct {
	Context  context.Context
	Input    Input
	Config   ConfigStore
	HTTP     *http.Client
	Terminal *terminal.Runtime
	Git      *gitprocess.Runner
}

// NewCmdCM creates the git cm leaf with an optional test runner.
func NewCmdCM(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runCM
	}
	var profile string
	var timeout string
	var language string
	var staged bool
	var stage bool
	var stageAll bool
	var push string
	var stagePush string
	var dryRun bool
	var body bool
	command := &cobra.Command{
		Use:   "cm",
		Short: "Generate an Angular-style commit message from uncommitted changes",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if factory == nil || factory.ConfigStore == nil || factory.HTTPClient == nil || factory.Terminal == nil || factory.GitRunner == nil {
				return errors.New("git cm Factory is incomplete")
			}
			input := Input{
				Profile:  profile,
				Language: language,
				Staged:   staged,
				Stage:    stage,
				StageAll: stageAll,
				DryRun:   dryRun,
				Body:     body,
			}
			if command.Flags().Changed("timeout-ms") {
				parsed, err := parseTimeoutMS(timeout)
				if err != nil {
					return err
				}
				input.TimeoutMS = &parsed
			}
			if command.Flags().Changed("push") {
				input.Push = stringPointer(push)
			}
			if command.Flags().Changed("stage-push") {
				input.StagePush = stringPointer(stagePush)
			}
			return runF(&Options{
				Context: command.Context(),
				Input:   input,
				Config: func() (ProfileResolver, error) {
					store, err := factory.ConfigStore()
					if err != nil {
						return nil, err
					}
					return store, nil
				},
				HTTP:     factory.HTTPClient,
				Terminal: factory.Terminal,
				Git:      factory.GitRunner(),
			})
		},
	}
	command.Flags().StringVar(&profile, "profile", "", "CM provider profile to use")
	command.Flags().StringVar(&timeout, "timeout-ms", "", "Provider request timeout in milliseconds")
	command.Flags().StringVarP(&language, "lang", "l", "en", "Commit message language: en or zh")
	command.Flags().BoolVarP(&staged, "staged", "S", false, "Only use staged changes")
	command.Flags().BoolVarP(&stage, "stage", "s", false, "Select files to stage before generating")
	command.Flags().BoolVarP(&stageAll, "stage-all", "a", false, "Stage all changes before generating")
	command.Flags().StringVarP(&push, "push", "p", "", "Push to remote after creating the commit, defaults to origin")
	command.Flags().Lookup("push").NoOptDefVal = "origin"
	command.Flags().StringVarP(&stagePush, "stage-push", "c", "", "Select files to stage, commit, then push, defaults to origin")
	command.Flags().Lookup("stage-push").NoOptDefVal = "origin"
	command.Flags().BoolVarP(&dryRun, "dry-run", "d", false, "Generate and print only")
	command.Flags().BoolVarP(&body, "body", "b", false, "Allow a short commit body")
	return command
}

func stringPointer(value string) *string {
	return &value
}

func parseTimeoutMS(value string) (float64, error) {
	parsed, ok := parseNumber(value)
	if !ok || !isSafeInteger(parsed) || parsed < 1000 {
		return 0, fmt.Errorf("'%s' is not a valid timeout in milliseconds. Use an integer greater than or equal to 1000.", value)
	}
	return parsed, nil
}

func parseNumber(value string) (float64, bool) {
	trimmed := strings.TrimFunc(value, func(character rune) bool {
		return unicode.IsSpace(character) || character == '\uFEFF'
	})
	if trimmed == "" {
		return 0, true
	}
	if len(trimmed) > 2 && trimmed[0] == '0' {
		base := 0
		switch trimmed[1] {
		case 'x', 'X':
			base = 16
		case 'b', 'B':
			base = 2
		case 'o', 'O':
			base = 8
		}
		if base != 0 {
			integer, err := strconv.ParseUint(trimmed[2:], base, 64)
			if err == nil {
				return float64(integer), true
			}
			return 0, false
		}
	}
	parsed, err := strconv.ParseFloat(trimmed, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, false
	}
	return parsed, true
}

func isSafeInteger(value float64) bool {
	return math.Trunc(value) == value && math.Abs(value) <= 9007199254740991
}

// NormalizeArguments preserves the legacy optional remote forms before Cobra parses flags.
func NormalizeArguments(arguments []string) []string {
	result := append([]string(nil), arguments...)
	for index := 0; index+1 < len(result); index++ {
		if result[index] == "--" {
			return result
		}
		if result[index] != "git" || result[index+1] != "cm" {
			continue
		}
		return normalizeLeafArguments(result, index+2)
	}
	return result
}

func normalizeLeafArguments(arguments []string, start int) []string {
	for index := start; index < len(arguments); index++ {
		argument := arguments[index]
		if argument == "--" {
			break
		}
		if (argument == "--push" || argument == "--stage-push" || argument == "-p" || argument == "-c") && index+1 < len(arguments) && !strings.HasPrefix(arguments[index+1], "-") {
			flag := argument
			if flag == "-p" {
				flag = "--push"
			}
			if flag == "-c" {
				flag = "--stage-push"
			}
			arguments[index] = flag + "=" + arguments[index+1]
			arguments = append(arguments[:index+1], arguments[index+2:]...)
			continue
		}
		if strings.HasPrefix(argument, "-p") && !strings.HasPrefix(argument, "-p=") && len(argument) > len("-p") {
			arguments[index] = "--push=" + argument[len("-p"):]
			continue
		}
		if strings.HasPrefix(argument, "-p=") {
			arguments[index] = "--push=" + argument[len("-p"):]
			continue
		}
		if strings.HasPrefix(argument, "-c") && !strings.HasPrefix(argument, "-c=") && len(argument) > len("-c") {
			arguments[index] = "--stage-push=" + argument[len("-c"):]
			continue
		}
		if strings.HasPrefix(argument, "-c=") {
			arguments[index] = "--stage-push=" + argument[len("-c"):]
		}
	}
	return arguments
}
