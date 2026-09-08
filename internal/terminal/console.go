package terminal

import (
	"errors"
	"fmt"
	"strings"
)

const (
	maxConsoleMetadata  = 4
	maxConsoleField     = 160
	maxConsoleFormSteps = 64
)

// ErrInvalidConsoleDescriptor reports a descriptor that cannot safely be
// rendered as the bounded Ops Console context.
var ErrInvalidConsoleDescriptor = errors.New("terminal console descriptor is invalid")

// ErrInvalidFinishRequest reports a completion projection that cannot be
// safely shown in the terminal or retained in the transcript.
var ErrInvalidFinishRequest = errors.New("terminal finish request is invalid")

func defaultConsoleDescriptor() ConsoleDescriptor {
	return ConsoleDescriptor{
		Command: "YCY",
		Target:  "terminal session",
		Status:  "READY",
		Metadata: []ConsoleMetadata{{
			Label: "mode",
			Value: "interactive",
		}},
	}
}

func normalizeConsoleDescriptor(descriptor ConsoleDescriptor) (ConsoleDescriptor, error) {
	command, err := normalizeConsoleField(descriptor.Command, "command", true)
	if err != nil {
		return ConsoleDescriptor{}, err
	}
	target, err := normalizeConsoleField(descriptor.Target, "target", false)
	if err != nil {
		return ConsoleDescriptor{}, err
	}
	status, err := normalizeConsoleField(descriptor.Status, "status", false)
	if err != nil {
		return ConsoleDescriptor{}, err
	}
	if status == "" {
		status = "READY"
	}
	if len(descriptor.Metadata) > maxConsoleMetadata {
		return ConsoleDescriptor{}, consoleDescriptorError("metadata has %d fields; at most %d are allowed", len(descriptor.Metadata), maxConsoleMetadata)
	}
	if len(descriptor.FormCatalog) > maxConsoleFormSteps {
		return ConsoleDescriptor{}, consoleDescriptorError("form catalog has %d steps; at most %d are allowed", len(descriptor.FormCatalog), maxConsoleFormSteps)
	}

	metadata := make([]ConsoleMetadata, 0, len(descriptor.Metadata))
	for _, field := range descriptor.Metadata {
		label, err := normalizeConsoleField(field.Label, "metadata label", true)
		if err != nil {
			return ConsoleDescriptor{}, err
		}
		value, err := normalizeConsoleField(field.Value, "metadata value", true)
		if err != nil {
			return ConsoleDescriptor{}, err
		}
		metadata = append(metadata, ConsoleMetadata{Label: label, Value: value})
	}
	formCatalog, err := normalizeConsoleFormCatalog(descriptor.FormCatalog)
	if err != nil {
		return ConsoleDescriptor{}, err
	}

	return ConsoleDescriptor{
		Command:     command,
		Target:      target,
		Status:      status,
		Metadata:    metadata,
		FormCatalog: formCatalog,
	}, nil
}

func normalizeConsoleFormCatalog(catalog []ConsoleFormStep) ([]ConsoleFormStep, error) {
	if len(catalog) == 0 {
		return nil, nil
	}
	steps := make([]ConsoleFormStep, 0, len(catalog))
	seen := make(map[string]struct{}, len(catalog))
	for _, step := range catalog {
		id, err := normalizeConsoleField(step.ID, "form step ID", true)
		if err != nil {
			return nil, err
		}
		name, err := normalizeConsoleField(step.Name, "form step name", true)
		if err != nil {
			return nil, err
		}
		detail, err := normalizeConsoleField(step.Detail, "form step detail", false)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[id]; exists {
			return nil, consoleDescriptorError("form step ID %q is duplicated", id)
		}
		seen[id] = struct{}{}
		steps = append(steps, ConsoleFormStep{ID: id, Name: name, Detail: detail, Sensitive: step.Sensitive})
	}
	return steps, nil
}

func normalizeFinishRequest(request FinishRequest) (FinishRequest, error) {
	if !request.Outcome.valid() {
		return FinishRequest{}, fmt.Errorf("%w: outcome is invalid", ErrInvalidFinishRequest)
	}
	location, err := normalizeConsoleField(request.Location, "location", false)
	if err != nil {
		return FinishRequest{}, fmt.Errorf("%w: %v", ErrInvalidFinishRequest, err)
	}
	if len(request.Summary.Blocks) > maxConsoleFormSteps {
		return FinishRequest{}, fmt.Errorf("%w: summary has too many blocks", ErrInvalidFinishRequest)
	}
	summary := PresentationDocument{Blocks: make([]PresentationBlock, 0, len(request.Summary.Blocks))}
	for _, block := range request.Summary.Blocks {
		text, err := normalizeConsoleField(block.Text, "summary", false)
		if err != nil {
			return FinishRequest{}, fmt.Errorf("%w: %v", ErrInvalidFinishRequest, err)
		}
		summary.Blocks = append(summary.Blocks, PresentationBlock{Role: block.Role, Text: text, Sensitive: block.Sensitive})
	}
	request.Location = location
	request.Summary = summary
	return request, nil
}

func normalizeConsoleField(value, name string, required bool) (string, error) {
	value = strings.Join(strings.Fields(stripTerminalControl(strings.ToValidUTF8(value, "�"))), " ")
	if required && value == "" {
		return "", consoleDescriptorError("%s is required", name)
	}
	if len(value) > maxConsoleField {
		return "", consoleDescriptorError("%s exceeds %d bytes", name, maxConsoleField)
	}
	return value, nil
}

func consoleDescriptorError(format string, arguments ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalidConsoleDescriptor}, arguments...)...)
}
