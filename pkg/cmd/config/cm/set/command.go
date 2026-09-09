package set

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
	terminalexperience "github.com/hackycy/hackycy-cli/internal/terminal"
	"github.com/hackycy/hackycy-cli/pkg/cmdutil"
	"github.com/spf13/cobra"
)

type StoreProvider func() (SetWriter, error)

type Options struct {
	Context  context.Context
	Profile  string
	Key      string
	Value    string
	Store    StoreProvider
	Terminal *terminalexperience.Runtime
}

func NewCmdSet(factory *cmdutil.Factory, runF func(*Options) error) *cobra.Command {
	if runF == nil {
		runF = runSet
	}
	return &cobra.Command{Use: "set <profile> <key> <value>", Short: "Set an optional commit message provider profile value", Args: cobra.ExactArgs(3), RunE: func(command *cobra.Command, arguments []string) error {
		if factory == nil || factory.ConfigStore == nil || factory.Terminal == nil {
			return errors.New("config cm set Factory is incomplete")
		}
		return runF(&Options{Context: command.Context(), Profile: arguments[0], Key: arguments[1], Value: arguments[2], Store: func() (SetWriter, error) {
			store, err := factory.ConfigStore()
			if err != nil {
				return nil, err
			}
			return store, nil
		}, Terminal: factory.Terminal})
	}}
}

func runSet(options *Options) error {
	if options == nil || options.Store == nil || options.Terminal == nil {
		return errors.New("config cm set options are incomplete")
	}
	ctx := options.Context
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := options.Terminal.OpenConsole(ctx, terminalCMSetConsoleDescriptor(options.Profile, options.Key))
	if err != nil {
		return err
	}
	defer run.Close()
	caps := options.Terminal.Capabilities()

	// Set is one atomic appconfig operation. Rich keeps one controlled Work
	// Catalog open for the operation; Plain keeps the established single
	// loading diagnostic and Automation is deliberately silent.
	phases := newCMSetPhaseSink(run, caps)
	finish := func(outcome terminalexperience.FinishOutcome, document *terminalexperience.PresentationDocument, workErr error) error {
		return errors.Join(workErr, phases.close(), run.Finish(terminalCMSetFinishRequest(outcome), document))
	}
	if err := phases.begin(); err != nil {
		return finish(terminalexperience.Failed, nil, err)
	}

	result, workErr := func() (SetResult, error) {
		writer, err := options.Store()
		if err != nil {
			return SetResult{}, err
		}
		module, err := NewSet(SetDependencies{Writer: writer})
		if err != nil {
			return SetResult{}, err
		}
		return module.Run(ctx, SetRequest{Profile: options.Profile, Key: options.Key, Value: options.Value})
	}()

	if caps.Interaction == terminalexperience.RichInteractive {
		state := terminalexperience.PhaseCompleted
		detail := cmSetSuccessDetail(options.Profile, options.Key, options.Value)
		if workErr != nil {
			state = terminalexperience.PhaseFailed
			detail = "Unable to update CM profile"
		}
		workErr = errors.Join(workErr, phases.end(state, detail))
	}
	if workErr != nil {
		return finish(terminalexperience.Failed, nil, workErr)
	}
	document := terminalCMSetDocument(result)
	if caps.Interaction == terminalexperience.RichInteractive && caps.Stdout.Terminal {
		document = terminalCMSetRichDocument(result)
	}
	return finish(terminalexperience.Succeeded, &document, nil)
}

func terminalCMSetDocument(result SetResult) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: terminalexperience.VisualRoleSuccess, Text: fmt.Sprintf("Profile %s updated", result.Profile)}}}
}

func terminalCMSetRichDocument(result SetResult) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{
		{Role: terminalexperience.VisualRoleMuted, Text: "YCY / config cm set"},
		{Role: terminalexperience.VisualRoleTitle, Text: "Update CM profile"},
		{Role: terminalexperience.VisualRoleMuted, Text: "Change one stored provider setting"},
		{Role: terminalexperience.VisualRoleSuccess, Text: "Profile " + safeCMSetProfile(result.Profile) + " updated"},
	}}
}

const (
	cmSetWorkCatalogID = "update-cm-profile"
	cmSetPhaseID       = "update-cm-profile"
	cmSetPhaseName     = "Update CM profile"
)

type cmSetPhaseSink struct {
	run         terminalexperience.ExperienceRun
	caps        terminalexperience.Capabilities
	work        terminalexperience.WorkSession
	workStarted bool
	workClosed  bool
}

func newCMSetPhaseSink(run terminalexperience.ExperienceRun, caps terminalexperience.Capabilities) *cmSetPhaseSink {
	return &cmSetPhaseSink{run: run, caps: caps}
}

func (sink *cmSetPhaseSink) begin() error {
	if sink.caps.Interaction == terminalexperience.PlainInteractive {
		return sink.run.Notice(terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{
			Role: terminalexperience.VisualRoleActive,
			Text: "Updating CM profile...",
		}}})
	}
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminalexperience.OperationPhase{
		ID:     cmSetPhaseID,
		State:  terminalexperience.PhaseActive,
		Detail: "Validating setting and saving profile",
	})
}

func (sink *cmSetPhaseSink) end(state terminalexperience.PhaseState, detail string) error {
	if sink.caps.Interaction != terminalexperience.RichInteractive {
		return nil
	}
	if err := sink.ensureWork(); err != nil {
		return err
	}
	return sink.work.Update(terminalexperience.OperationPhase{ID: cmSetPhaseID, State: state, Detail: detail})
}

func (sink *cmSetPhaseSink) ensureWork() error {
	if sink.workStarted {
		return nil
	}
	work, err := terminalexperience.StartWork(sink.run, terminalCMSetWorkCatalog())
	if err != nil {
		return err
	}
	sink.work = work
	sink.workStarted = true
	return nil
}

func (sink *cmSetPhaseSink) close() error {
	if sink.workClosed || sink.work == nil {
		return nil
	}
	sink.workClosed = true
	return sink.work.Close()
}

func terminalCMSetWorkCatalog() terminalexperience.WorkCatalog {
	return terminalexperience.WorkCatalog{
		ID:    cmSetWorkCatalogID,
		Label: cmSetPhaseName,
		Phases: []terminalexperience.PhaseDefinition{{
			ID:   cmSetPhaseID,
			Name: cmSetPhaseName,
		}},
	}
}

func terminalCMSetFinishRequest(outcome terminalexperience.FinishOutcome) terminalexperience.FinishRequest {
	request := terminalexperience.FinishRequest{Outcome: outcome}
	switch outcome {
	case terminalexperience.Succeeded:
		request.Summary = terminalCMSetOutcomeDocument("Profile updated", terminalexperience.VisualRoleSuccess)
	case terminalexperience.Failed:
		request.Summary = terminalCMSetOutcomeDocument("Unable to update CM profile", terminalexperience.VisualRoleError)
	}
	return request
}

func terminalCMSetOutcomeDocument(text string, role terminalexperience.VisualRole) terminalexperience.PresentationDocument {
	return terminalexperience.PresentationDocument{Blocks: []terminalexperience.PresentationBlock{{Role: role, Text: text}}}
}

func cmSetSuccessDetail(profile, key, value string) string {
	return "Profile: " + safeCMSetProfile(profile) + "; Setting: " + safeCMSetKey(key) + "; " + safeCMSetValueDetail(key, value)
}

func safeCMSetProfile(value string) string {
	return safeCMSetField(value, "Profile")
}

func safeCMSetKey(value string) string {
	return safeCMSetField(value, "Setting")
}

func safeCMSetField(value, fallback string) string {
	if !utf8.ValidString(value) {
		return fallback
	}
	var builder strings.Builder
	for _, r := range value {
		if unicode.IsControl(r) {
			return fallback
		}
		builder.WriteRune(r)
	}
	value = strings.TrimSpace(builder.String())
	if value == "" {
		return fallback
	}
	runes := []rune(value)
	if len(runes) > 256 {
		return string(runes[:256]) + "..."
	}
	return value
}

func safeCMSetValueDetail(key, value string) string {
	switch key {
	case "apiKey":
		return "API key: [redacted]"
	case "baseURL":
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return "Base URL: <empty>"
		}
		if !utf8.ValidString(trimmed) || strings.IndexFunc(trimmed, unicode.IsControl) >= 0 {
			return "Base URL configured"
		}
		parsed, err := url.Parse(trimmed)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return "Base URL configured"
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		result := parsed.Scheme + "://" + parsed.Host + parsed.EscapedPath()
		result = strings.TrimSuffix(result, "/")
		if result == parsed.Scheme+"://"+parsed.Host {
			return "Base URL: " + result
		}
		return "Base URL: " + result
	case "model":
		if strings.TrimSpace(value) == "" {
			return "Model: <empty>"
		}
		return "Model: " + safeCMSetField(value, "Model configured")
	default:
		if strings.TrimSpace(value) == "" {
			return "Requested value: <empty>"
		}
		return "Requested value: " + safeCMSetField(value, "Requested value")
	}
}

var _ SetWriter = (*appconfig.Store)(nil)

func terminalCMSetConsoleDescriptor(profile, key string) terminalexperience.ConsoleDescriptor {
	return terminalexperience.ConsoleDescriptor{
		Command: "YCY / config cm set",
		Target:  "commit message profile update",
		Status:  "READY",
		Metadata: []terminalexperience.ConsoleMetadata{
			{Label: "scope", Value: "commit message configuration"},
			{Label: "profile", Value: safeCMSetProfile(profile)},
			{Label: "setting", Value: safeCMSetKey(key)},
		},
	}
}
