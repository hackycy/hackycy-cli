package cm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

const commitMessageMaxOutputTokens = 4_096.0

// ProviderTransport is Git CM's command-owned HTTP boundary.
type ProviderTransport interface {
	Do(*http.Request) (*http.Response, error)
}

// TokenUsage records finite token fields returned by an OpenAI-compatible provider.
type TokenUsage struct {
	PromptTokens     *float64
	CompletionTokens *float64
	TotalTokens      *float64
}

// ModelInput is the system instruction plus selected semantic evidence.
type ModelInput struct {
	System   string
	Evidence string
}

// ModelOutput is one provider-generated commit-message candidate.
type ModelOutput struct {
	Content string
	Usage   *TokenUsage
}

// CommitMessageModel is the command-private model generation boundary.
type CommitMessageModel interface {
	Generate(context.Context, ModelInput) (ModelOutput, error)
}

type openAICompatibleModel struct {
	profile   appconfig.ResolvedCMProfile
	transport ProviderTransport
}

// NewOpenAICompatibleModel creates the provider model configured for one resolved profile.
func NewOpenAICompatibleModel(profile appconfig.ResolvedCMProfile, transport ProviderTransport) (CommitMessageModel, error) {
	if transport == nil {
		return nil, errors.New("Git CM provider transport is required")
	}
	return &openAICompatibleModel{profile: profile, transport: transport}, nil
}

func (model *openAICompatibleModel) Generate(ctx context.Context, input ModelInput) (ModelOutput, error) {
	result, err := executeCommitMessageProvider(ctx, model.profile, model.transport, input)
	if err != nil {
		return ModelOutput{}, redactProviderError(err, model.profile.APIKey)
	}
	return result, nil
}

type chatCompletionMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatCompletionRequest struct {
	Model           string                  `json:"model"`
	Temperature     float64                 `json:"temperature"`
	MaxOutputTokens float64                 `json:"max_tokens"`
	Messages        []chatCompletionMessage `json:"messages"`
	Thinking        *providerThinking       `json:"thinking,omitempty"`
}

type providerThinking struct {
	Type string `json:"type"`
}

func executeCommitMessageProvider(ctx context.Context, profile appconfig.ResolvedCMProfile, transport ProviderTransport, input ModelInput) (ModelOutput, error) {
	timeout := time.Duration(profile.TimeoutMS * float64(time.Millisecond))
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := newCommitMessageProviderRequest(requestContext, profile, input)
	if err != nil {
		return ModelOutput{}, err
	}
	response, err := transport.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return ModelOutput{}, fmt.Errorf("Provider request timed out after %sms", strconv.FormatFloat(profile.TimeoutMS, 'f', -1, 64))
		}
		return ModelOutput{}, err
	}
	if response == nil {
		return ModelOutput{}, errors.New("Commit-message provider returned no response")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return ModelOutput{}, fmt.Errorf("read commit-message provider response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return ModelOutput{}, commitMessageHTTPError(response, body)
	}
	return decodeCommitMessageProviderResponse(body)
}

func newCommitMessageProviderRequest(ctx context.Context, profile appconfig.ResolvedCMProfile, input ModelInput) (*http.Request, error) {
	payload := chatCompletionRequest{
		Model:           profile.Model,
		Temperature:     profile.Temperature,
		MaxOutputTokens: commitMessageMaxOutputTokens,
		Messages: []chatCompletionMessage{
			{Role: "system", Content: input.System},
			{Role: "user", Content: input.Evidence},
		},
	}
	if shouldDisableCommitMessageThinking(profile) {
		payload.Thinking = &providerThinking{Type: "disabled"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode commit-message provider request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, profile.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create commit-message provider request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+profile.APIKey)
	request.Header.Set("Content-Type", "application/json")
	return request, nil
}

func shouldDisableCommitMessageThinking(profile appconfig.ResolvedCMProfile) bool {
	return strings.Contains(profile.BaseURL, "api.deepseek.com") && strings.HasPrefix(strings.ToLower(profile.Model), "deepseek-v4-")
}

func commitMessageHTTPError(response *http.Response, body []byte) error {
	statusText := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(response.Status), strconv.Itoa(response.StatusCode)))
	if statusText == "" {
		statusText = http.StatusText(response.StatusCode)
	}
	message := fmt.Sprintf("%d %s", response.StatusCode, statusText)
	if len(body) > 0 {
		message += ": " + string(body)
	}
	return errors.New(message)
}

func decodeCommitMessageProviderResponse(body []byte) (ModelOutput, error) {
	raw := string(body)
	decoded := map[string]any{}
	if len(body) > 0 {
		if err := json.Unmarshal(body, &decoded); err != nil {
			return ModelOutput{}, fmt.Errorf("decode commit-message provider response: %w", err)
		}
	}
	choices, _ := decoded["choices"].([]any)
	choice := map[string]any{}
	if len(choices) > 0 {
		choice, _ = choices[0].(map[string]any)
	}
	message, _ := choice["message"].(map[string]any)
	content, _ := message["content"].(string)
	content = strings.TrimSpace(content)
	if content == "" {
		return ModelOutput{}, fmt.Errorf("Provider returned an empty response (finish_reason=%s, response=%s)", commitMessageFinishReason(choice), commitMessageResponseSummary(raw))
	}
	return ModelOutput{Content: content, Usage: normalizeCommitMessageUsage(decoded["usage"])}, nil
}

func normalizeCommitMessageUsage(value any) *TokenUsage {
	usage, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	prompt, hasPrompt := finiteProviderNumber(usage["prompt_tokens"])
	completion, hasCompletion := finiteProviderNumber(usage["completion_tokens"])
	total, hasTotal := finiteProviderNumber(usage["total_tokens"])
	if !hasTotal && hasPrompt && hasCompletion {
		total = prompt + completion
		hasTotal = true
	}
	if !hasPrompt && !hasCompletion && !hasTotal {
		return nil
	}
	result := &TokenUsage{}
	if hasPrompt {
		result.PromptTokens = &prompt
	}
	if hasCompletion {
		result.CompletionTokens = &completion
	}
	if hasTotal {
		result.TotalTokens = &total
	}
	return result
}

func finiteProviderNumber(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok && !math.IsNaN(number) && !math.IsInf(number, 0)
}

func commitMessageFinishReason(choice map[string]any) string {
	if reason, found := choice["finish_reason"]; found && reason != nil {
		return fmt.Sprint(reason)
	}
	return "unknown"
}

func commitMessageResponseSummary(value string) string {
	const limit = 500
	if value == "" {
		return "<empty body>"
	}
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= limit {
		return trimmed
	}
	return trimmed[:limit] + "..."
}

func redactProviderError(err error, apiKey string) error {
	if apiKey == "" {
		return err
	}
	redacted := strings.ReplaceAll(err.Error(), apiKey, "[REDACTED]")
	if redacted == err.Error() {
		return err
	}
	return providerRedactedError{source: err, message: redacted}
}

type providerRedactedError struct {
	source  error
	message string
}

func (err providerRedactedError) Error() string {
	return err.message
}

func (err providerRedactedError) Unwrap() error {
	return err.source
}
