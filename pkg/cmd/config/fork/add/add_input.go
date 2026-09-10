package add

import (
	"errors"
	"strings"
	"unicode"
)

var (
	errAddNameRequired       = errors.New("Name is required")
	errAddNameContainsSpaces = errors.New("Name cannot contain spaces")
	errAddHostRequired       = errors.New("Host is required")
	errAddTokenRequired      = errors.New("Token is required")
	errAddInvalidProvider    = errors.New("invalid provider type")
	errAddInvalidProtocol    = errors.New("invalid protocol")
)

// AddInput is the command-owned semantic input for config fork add.
type AddInput struct {
	Alias  string
	Host   string
	Type   string
	Scheme string
	Token  string
}

// Choice is one terminal-selectable add prompt option.
type Choice struct {
	Value string
	Label string
}

// TextPrompt describes one command-owned text or password question.
type TextPrompt struct {
	Message     string
	Placeholder string
	Validate    func(string) error
}

// SelectPrompt describes one command-owned choice question.
type SelectPrompt struct {
	Message string
	Choices []Choice
}

// AddPrompter presents the add interaction without coupling the command to a terminal library.
type AddPrompter interface {
	Text(TextPrompt) (value string, cancelled bool, err error)
	Select(SelectPrompt) (value string, cancelled bool, err error)
	Password(TextPrompt) (value string, cancelled bool, err error)
}

var providerChoices = []Choice{
	{Value: "gitlab", Label: "GitLab"},
	{Value: "github", Label: "GitHub"},
}

var protocolChoices = []Choice{
	{Value: "https", Label: "HTTPS"},
	{Value: "http", Label: "HTTP (self-hosted / no TLS)"},
}

// PromptAdd collects the legacy add questions in their observable order.
func PromptAdd(prompter AddPrompter) (AddInput, bool, error) {
	alias, cancelled, err := prompter.Text(TextPrompt{
		Message:     "Instance name (alias)",
		Placeholder: "e.g. work, github, company-gl",
		Validate:    validateAddAlias,
	})
	if err != nil {
		return AddInput{}, false, err
	}
	if cancelled {
		return AddInput{}, true, nil
	}
	if err := validateAddAlias(alias); err != nil {
		return AddInput{}, false, err
	}

	host, cancelled, err := prompter.Text(TextPrompt{
		Message:     "Host",
		Placeholder: "e.g. gitlab.company.com, github.com",
		Validate:    validateAddHost,
	})
	if err != nil {
		return AddInput{}, false, err
	}
	if cancelled {
		return AddInput{}, true, nil
	}
	if err := validateAddHost(host); err != nil {
		return AddInput{}, false, err
	}

	provider, cancelled, err := prompter.Select(SelectPrompt{Message: "Provider type", Choices: providerChoices})
	if err != nil {
		return AddInput{}, false, err
	}
	if cancelled {
		return AddInput{}, true, nil
	}
	if !containsChoice(providerChoices, provider) {
		return AddInput{}, false, errAddInvalidProvider
	}

	protocol, cancelled, err := prompter.Select(SelectPrompt{Message: "Protocol", Choices: protocolChoices})
	if err != nil {
		return AddInput{}, false, err
	}
	if cancelled {
		return AddInput{}, true, nil
	}
	if !containsChoice(protocolChoices, protocol) {
		return AddInput{}, false, errAddInvalidProtocol
	}

	token, cancelled, err := prompter.Password(TextPrompt{Message: "Access token", Validate: validateAddToken})
	if err != nil {
		return AddInput{}, false, err
	}
	if cancelled {
		return AddInput{}, true, nil
	}
	if err := validateAddToken(token); err != nil {
		return AddInput{}, false, err
	}

	input := AddInput{Alias: alias, Host: host, Type: provider, Scheme: protocol, Token: token}
	return input, false, ValidateAddInput(input)
}

// ValidateAddInput keeps the frozen validation boundary without trimming persisted values.
func ValidateAddInput(input AddInput) error {
	if err := validateAddAlias(input.Alias); err != nil {
		return err
	}
	if err := validateAddHost(input.Host); err != nil {
		return err
	}
	if !containsChoice(providerChoices, input.Type) {
		return errAddInvalidProvider
	}
	if !containsChoice(protocolChoices, input.Scheme) {
		return errAddInvalidProtocol
	}
	return validateAddToken(input.Token)
}

func validateAddAlias(value string) error {
	if strings.TrimSpace(value) == "" {
		return errAddNameRequired
	}
	if strings.IndexFunc(value, unicode.IsSpace) >= 0 {
		return errAddNameContainsSpaces
	}
	return nil
}

func validateAddHost(value string) error {
	if strings.TrimSpace(value) == "" {
		return errAddHostRequired
	}
	return nil
}

func validateAddToken(value string) error {
	if strings.TrimSpace(value) == "" {
		return errAddTokenRequired
	}
	return nil
}

func containsChoice(choices []Choice, value string) bool {
	for _, choice := range choices {
		if choice.Value == value {
			return true
		}
	}
	return false
}
