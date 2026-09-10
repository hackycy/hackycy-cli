package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

const (
	cmTestSystemMessage = "Return exactly: ok"
	cmTestUserMessage   = "Connection test."
)

type cmTestChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type cmTestChatCompletionRequest struct {
	Model           string              `json:"model"`
	Temperature     float64             `json:"temperature"`
	MaxOutputTokens float64             `json:"max_tokens"`
	Messages        []cmTestChatMessage `json:"messages"`
	Thinking        *cmTestThinking     `json:"thinking,omitempty"`
}

type cmTestThinking struct {
	Type string `json:"type"`
}

func newCMTestProviderRequest(context context.Context, profile appconfig.ResolvedCMProfile) (*http.Request, error) {
	payload := cmTestChatCompletionRequest{
		Model:           profile.Model,
		Temperature:     profile.Temperature,
		MaxOutputTokens: profile.MaxOutputTokens,
		Messages: []cmTestChatMessage{
			{Role: "system", Content: cmTestSystemMessage},
			{Role: "user", Content: cmTestUserMessage},
		},
	}
	if shouldDisableCMTestThinking(profile) {
		payload.Thinking = &cmTestThinking{Type: "disabled"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode CM test provider request: %w", err)
	}
	request, err := http.NewRequestWithContext(context, http.MethodPost, profile.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create CM test provider request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+profile.APIKey)
	request.Header.Set("Content-Type", "application/json")
	return request, nil
}

func shouldDisableCMTestThinking(profile appconfig.ResolvedCMProfile) bool {
	return strings.Contains(profile.BaseURL, "api.deepseek.com") && strings.HasPrefix(strings.ToLower(profile.Model), "deepseek-v4-")
}
