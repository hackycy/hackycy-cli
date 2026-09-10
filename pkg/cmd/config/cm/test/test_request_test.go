package test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestNewCMTestProviderRequestUsesTheLegacyOpenAICompatibleShape(t *testing.T) {
	profile := appconfig.ResolvedCMProfile{
		Name:            "work",
		BaseURL:         "https://provider.test/v1",
		Model:           "provider-model",
		APIKey:          "test-api-key",
		Temperature:     0.75,
		TimeoutMS:       300000,
		MaxOutputTokens: 99.5,
	}

	request, err := newCMTestProviderRequest(context.Background(), profile)
	if err != nil {
		t.Fatalf("newCMTestProviderRequest() returned an error: %v", err)
	}
	if request.Method != http.MethodPost || request.URL.String() != "https://provider.test/v1/chat/completions" {
		t.Fatalf("request = (%s, %s), want POST https://provider.test/v1/chat/completions", request.Method, request.URL)
	}
	if request.Header.Get("Authorization") != "Bearer test-api-key" || request.Header.Get("Content-Type") != "application/json" {
		t.Fatal("request headers did not retain the expected bearer authentication and JSON content type")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read request body: %v", err)
	}
	if strings.Contains(string(body), profile.APIKey) {
		t.Fatal("request body exposed the API key")
	}
	var payload cmTestChatCompletionRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	if payload.Model != profile.Model || payload.Temperature != profile.Temperature || payload.MaxOutputTokens != profile.MaxOutputTokens {
		t.Fatalf("request parameters = (%q, %v, %v)", payload.Model, payload.Temperature, payload.MaxOutputTokens)
	}
	wantMessages := []cmTestChatMessage{
		{Role: "system", Content: cmTestSystemMessage},
		{Role: "user", Content: cmTestUserMessage},
	}
	if len(payload.Messages) != len(wantMessages) {
		t.Fatalf("request messages = %#v", payload.Messages)
	}
	for index, want := range wantMessages {
		if payload.Messages[index] != want {
			t.Fatalf("request message %d = %#v, want %#v", index, payload.Messages[index], want)
		}
	}
	if payload.Thinking != nil {
		t.Fatalf("non-DeepSeek request included thinking configuration: %#v", payload.Thinking)
	}
}

func TestNewCMTestProviderRequestDisablesThinkingOnlyForDeepSeekV4(t *testing.T) {
	for _, test := range []struct {
		name     string
		baseURL  string
		model    string
		disabled bool
	}{
		{name: "matching host and case-insensitive model", baseURL: "https://api.deepseek.com/v1", model: "DeepSeek-V4-Reasoner", disabled: true},
		{name: "different DeepSeek model", baseURL: "https://api.deepseek.com/v1", model: "deepseek-v3-chat", disabled: false},
		{name: "different provider", baseURL: "https://provider.test/v1", model: "deepseek-v4-reasoner", disabled: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := newCMTestProviderRequest(context.Background(), appconfig.ResolvedCMProfile{
				BaseURL:         test.baseURL,
				Model:           test.model,
				APIKey:          "test-api-key",
				Temperature:     0.2,
				MaxOutputTokens: 1000,
			})
			if err != nil {
				t.Fatalf("newCMTestProviderRequest() returned an error: %v", err)
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read request body: %v", err)
			}
			var payload cmTestChatCompletionRequest
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if test.disabled {
				if payload.Thinking == nil || payload.Thinking.Type != "disabled" {
					t.Fatalf("DeepSeek V4 thinking = %#v, want disabled", payload.Thinking)
				}
				return
			}
			if payload.Thinking != nil {
				t.Fatalf("nonmatching provider request included thinking configuration: %#v", payload.Thinking)
			}
		})
	}
}
