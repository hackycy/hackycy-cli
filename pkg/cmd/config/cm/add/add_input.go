package add

import (
	"errors"
	"strings"
	"unicode"
)

var (
	errAddNameRequired       = errors.New("Name is required")
	errAddNameContainsSpaces = errors.New("Name cannot contain spaces")
	errAddBaseURLRequired    = errors.New("Base URL is required")
	errAddModelRequired      = errors.New("Model is required")
	errAddAPIKeyRequired     = errors.New("API key is required")
)

// AddInput is the command-owned semantic input for config cm add.
type AddInput struct {
	Name    string
	BaseURL string
	Model   string
	APIKey  string
}

// AddTextPrompt describes one command-owned text or password question.
type AddTextPrompt struct {
	Message     string
	Placeholder string
	Validate    func(string) error
}

// AddPrompter presents the add interaction without coupling the command to a terminal library.
type AddPrompter interface {
	Text(AddTextPrompt) (value string, cancelled bool, err error)
	Password(AddTextPrompt) (value string, cancelled bool, err error)
}

// PromptAdd collects the legacy CM profile questions in their observable order.
func PromptAdd(prompter AddPrompter) (AddInput, bool, error) {
	name, cancelled, err := prompter.Text(AddTextPrompt{
		Message:     "Profile name",
		Placeholder: "e.g. openai, deepseek, work",
		Validate:    validateAddName,
	})
	if err != nil {
		return AddInput{}, false, err
	}
	if cancelled {
		return AddInput{}, true, nil
	}
	if err := validateAddName(name); err != nil {
		return AddInput{}, false, err
	}

	baseURL, cancelled, err := prompter.Text(AddTextPrompt{
		Message:     "OpenAI-compatible base URL",
		Placeholder: "https://api.openai.com/v1",
		Validate:    validateAddBaseURL,
	})
	if err != nil {
		return AddInput{}, false, err
	}
	if cancelled {
		return AddInput{}, true, nil
	}
	if err := validateAddBaseURL(baseURL); err != nil {
		return AddInput{}, false, err
	}

	model, cancelled, err := prompter.Text(AddTextPrompt{
		Message:     "Model",
		Placeholder: "gpt-4.1-mini",
		Validate:    validateAddModel,
	})
	if err != nil {
		return AddInput{}, false, err
	}
	if cancelled {
		return AddInput{}, true, nil
	}
	if err := validateAddModel(model); err != nil {
		return AddInput{}, false, err
	}

	apiKey, cancelled, err := prompter.Password(AddTextPrompt{
		Message:  "API key",
		Validate: validateAddAPIKey,
	})
	if err != nil {
		return AddInput{}, false, err
	}
	if cancelled {
		return AddInput{}, true, nil
	}
	if err := validateAddAPIKey(apiKey); err != nil {
		return AddInput{}, false, err
	}

	input := AddInput{Name: name, BaseURL: baseURL, Model: model, APIKey: apiKey}
	return input, false, ValidateAddInput(input)
}

// ValidateAddInput keeps the frozen validation boundary before appconfig normalization.
func ValidateAddInput(input AddInput) error {
	if err := validateAddName(input.Name); err != nil {
		return err
	}
	if err := validateAddBaseURL(input.BaseURL); err != nil {
		return err
	}
	if err := validateAddModel(input.Model); err != nil {
		return err
	}
	return validateAddAPIKey(input.APIKey)
}

func validateAddName(value string) error {
	if strings.TrimSpace(value) == "" {
		return errAddNameRequired
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return errAddNameContainsSpaces
	}
	return nil
}

func validateAddBaseURL(value string) error {
	if strings.TrimSpace(value) == "" {
		return errAddBaseURLRequired
	}
	return nil
}

func validateAddModel(value string) error {
	if strings.TrimSpace(value) == "" {
		return errAddModelRequired
	}
	return nil
}

func validateAddAPIKey(value string) error {
	if strings.TrimSpace(value) == "" {
		return errAddAPIKeyRequired
	}
	return nil
}
