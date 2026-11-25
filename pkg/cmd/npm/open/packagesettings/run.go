package packagesettings

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/hackycy/hackycy-cli/internal/logging"
	"github.com/hackycy/hackycy-cli/internal/terminal"
)

const continueStep = "continue-opening"

func runPackageSettings(options *Options) error {
	directory, err := options.WorkingDirectory()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	names, err := discoverPackages(options.Context, directory)
	if err != nil {
		return err
	}
	if len(names) > 0 && !options.Batch && options.Terminal.Capabilities().Interaction == terminal.Automation {
		return errors.New("opening package settings one at a time requires an interactive terminal; use --batch to open all listed packages")
	}
	if err := terminal.WritePlain(options.Output, packageList(names)); err != nil {
		return fmt.Errorf("write package list: %w", err)
	}
	if len(names) == 0 {
		return nil
	}
	if options.Batch {
		return openBatch(options, names)
	}
	if err := openPackage(options, names[0]); err != nil || len(names) == 1 {
		return err
	}
	return openRemaining(options, names)
}

func packageList(names []string) terminal.PresentationDocument {
	if len(names) == 0 {
		return terminal.PresentationDocument{Blocks: []terminal.PresentationBlock{{Text: "No npm packages to open."}}}
	}
	title := fmt.Sprintf("Found %d npm packages:", len(names))
	if len(names) == 1 {
		title = "Found 1 npm package:"
	}
	blocks := make([]terminal.PresentationBlock, 0, len(names)+1)
	blocks = append(blocks, terminal.PresentationBlock{Text: title})
	for _, name := range names {
		blocks = append(blocks, terminal.PresentationBlock{Text: "  " + name})
	}
	return terminal.PresentationDocument{Blocks: blocks}
}

func openBatch(options *Options, names []string) error {
	var failures []terminal.PresentationBlock
	for _, name := range names {
		if options.Context.Err() != nil {
			break
		}
		if err := openPackage(options, name); err != nil && options.Context.Err() == nil {
			failures = append(failures, terminal.PresentationBlock{Text: logging.RedactDiagnostic(err.Error())})
		}
	}
	if len(failures) == 0 {
		return options.Context.Err()
	}
	document := terminal.PresentationDocument{Blocks: failures}
	reportErr := terminal.WritePlain(options.Terminal.DiagnosticWriter(), document)
	return errors.Join(fmt.Errorf("failed to open %d npm package settings page(s)", len(failures)), reportErr, options.Context.Err())
}

func openRemaining(options *Options, names []string) (runErr error) {
	run, err := options.Terminal.OpenConsole(options.Context, terminal.ConsoleDescriptor{
		Command: "ycy npm open package-settings",
		FormCatalog: []terminal.ConsoleFormStep{
			{ID: continueStep, Name: "Open the next package"},
		},
	})
	if err != nil {
		return err
	}
	outcome := terminal.Succeeded
	defer func() {
		if options.Context.Err() != nil {
			outcome = terminal.Cancelled
		} else if runErr != nil {
			outcome = terminal.Failed
		}
		runErr = errors.Join(runErr, run.Finish(terminal.FinishRequest{Outcome: outcome}), run.Close())
	}()

	for index, name := range names[1:] {
		if err := options.Context.Err(); err != nil {
			return err
		}
		message := fmt.Sprintf("Open settings for %s?", name)
		answer, err := run.Ask(terminal.InteractionRequest{
			Kind:            terminal.InteractionConfirm,
			Message:         message,
			Description:     fmt.Sprintf("[%d/%d] Requested settings for %s", index+1, len(names), names[index]),
			PlainPrompt:     message + " [Y/n, Enter to continue]: ",
			HasDefault:      true,
			Default:         terminal.InteractionAnswer{Confirmed: true},
			ConsoleStepID:   continueStep,
			TranscriptLabel: "Open next package",
		})
		if err := options.Context.Err(); err != nil {
			return err
		}
		if errors.Is(err, terminal.ErrInteractionCancelled) || (err == nil && !answer.Confirmed) {
			outcome = terminal.Cancelled
			return nil
		}
		if err != nil {
			return err
		}
		if err := openPackage(options, name); err != nil {
			return err
		}
	}
	return nil
}

func openPackage(options *Options, name string) error {
	if err := options.Context.Err(); err != nil {
		return err
	}
	address := settingsURL(name)
	if err := options.OpenURL(options.Context, address); err != nil {
		return fmt.Errorf("open settings for %s (%s): %w", name, address, err)
	}
	return nil
}

func settingsURL(name string) string {
	return (&url.URL{Scheme: "https", Host: "www.npmjs.com", Path: "/package/" + name + "/access"}).String()
}

func contextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}
