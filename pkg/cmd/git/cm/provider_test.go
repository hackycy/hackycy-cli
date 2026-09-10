package cm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestCommitMessageProviderRequestUsesTheFixedOpenAICompatibleContract(t *testing.T) {
	profile := commitMessageProfile()
	input := ModelInput{System: "system instruction", Evidence: "token=ordinary-fixture-value"}
	request, err := newCommitMessageProviderRequest(context.Background(), profile, input)
	if err != nil {
		t.Fatalf("newCommitMessageProviderRequest() error = %v", err)
	}
	if request.Method != http.MethodPost || request.URL.String() != "https://provider.test/v1/chat/completions" {
		t.Fatalf("request = (%s, %s)", request.Method, request.URL)
	}
	if request.Header.Get("Authorization") != "Bearer provider-api-key" || request.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("request headers = %#v", request.Header)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if strings.Contains(string(body), profile.APIKey) || !strings.Contains(string(body), "ordinary-fixture-value") {
		t.Fatalf("request body = %q", body)
	}
	var payload chatCompletionRequest
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if payload.Model != profile.Model || payload.Temperature != profile.Temperature || payload.MaxOutputTokens != commitMessageMaxOutputTokens || payload.Thinking != nil {
		t.Fatalf("payload = %#v", payload)
	}
	if got, want := payload.Messages, []chatCompletionMessage{{Role: "system", Content: input.System}, {Role: "user", Content: input.Evidence}}; !equalChatMessages(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
}

func TestCommitMessageProviderDisablesThinkingOnlyForDeepSeekV4(t *testing.T) {
	for _, testCase := range []struct {
		baseURL  string
		model    string
		disabled bool
	}{
		{baseURL: "https://api.deepseek.com/v1", model: "DeepSeek-V4-Reasoner", disabled: true},
		{baseURL: "https://api.deepseek.com/v1", model: "deepseek-v3-chat", disabled: false},
		{baseURL: "https://provider.test/v1", model: "deepseek-v4-reasoner", disabled: false},
	} {
		t.Run(testCase.baseURL+"/"+testCase.model, func(t *testing.T) {
			profile := commitMessageProfile()
			profile.BaseURL = testCase.baseURL
			profile.Model = testCase.model
			request, err := newCommitMessageProviderRequest(context.Background(), profile, ModelInput{})
			if err != nil {
				t.Fatalf("newCommitMessageProviderRequest() error = %v", err)
			}
			body, _ := io.ReadAll(request.Body)
			var payload chatCompletionRequest
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if testCase.disabled && (payload.Thinking == nil || payload.Thinking.Type != "disabled") {
				t.Fatalf("thinking = %#v", payload.Thinking)
			}
			if !testCase.disabled && payload.Thinking != nil {
				t.Fatalf("thinking = %#v", payload.Thinking)
			}
		})
	}
}

func TestOpenAICompatibleModelMakesOneRequestAndNormalizesUsage(t *testing.T) {
	transport := &recordingProviderTransport{response: &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"  feat(cm): generate message  "}}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)),
	}}
	model, err := NewOpenAICompatibleModel(commitMessageProfile(), transport)
	if err != nil {
		t.Fatalf("NewOpenAICompatibleModel() error = %v", err)
	}
	result, err := model.Generate(context.Background(), ModelInput{System: "system", Evidence: "evidence"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if transport.calls != 1 || transport.request == nil || result.Content != "feat(cm): generate message" {
		t.Fatalf("transport = %#v, result = %#v", transport, result)
	}
	if result.Usage == nil || result.Usage.PromptTokens == nil || *result.Usage.PromptTokens != 3 || result.Usage.CompletionTokens == nil || *result.Usage.CompletionTokens != 2 || result.Usage.TotalTokens == nil || *result.Usage.TotalTokens != 5 {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestOpenAICompatibleModelRedactsItsAPIKeyFromProviderFailures(t *testing.T) {
	profile := commitMessageProfile()
	for _, transport := range []ProviderTransport{
		providerTransportFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("transport rejected provider-api-key")
		}),
		providerTransportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", Body: io.NopCloser(strings.NewReader(`{"error":"provider-api-key"}`))}, nil
		}),
	} {
		model, err := NewOpenAICompatibleModel(profile, transport)
		if err != nil {
			t.Fatalf("NewOpenAICompatibleModel() error = %v", err)
		}
		_, err = model.Generate(context.Background(), ModelInput{})
		if err == nil || strings.Contains(err.Error(), profile.APIKey) || !strings.Contains(err.Error(), "[REDACTED]") {
			t.Fatalf("Generate() error = %v", err)
		}
	}
}

func TestOpenAICompatibleModelReportsTimeoutAndResponseFailures(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		profile := commitMessageProfile()
		profile.TimeoutMS = 1.5
		model, err := NewOpenAICompatibleModel(profile, providerTransportFunc(func(request *http.Request) (*http.Response, error) {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}))
		if err != nil {
			t.Fatalf("NewOpenAICompatibleModel() error = %v", err)
		}
		_, err = model.Generate(context.Background(), ModelInput{})
		if err == nil || err.Error() != "Provider request timed out after 1.5ms" {
			t.Fatalf("Generate() error = %v", err)
		}
	})
	t.Run("malformed JSON", func(t *testing.T) {
		model, err := NewOpenAICompatibleModel(commitMessageProfile(), providerTransportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":`))}, nil
		}))
		if err != nil {
			t.Fatalf("NewOpenAICompatibleModel() error = %v", err)
		}
		_, err = model.Generate(context.Background(), ModelInput{})
		if err == nil || !strings.Contains(err.Error(), "decode commit-message provider response") {
			t.Fatalf("Generate() error = %v", err)
		}
	})
	t.Run("empty content", func(t *testing.T) {
		model, err := NewOpenAICompatibleModel(commitMessageProfile(), providerTransportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[{"finish_reason":"length","message":{"content":" "}}]}`))}, nil
		}))
		if err != nil {
			t.Fatalf("NewOpenAICompatibleModel() error = %v", err)
		}
		_, err = model.Generate(context.Background(), ModelInput{})
		if err == nil || !strings.Contains(err.Error(), "finish_reason=length") {
			t.Fatalf("Generate() error = %v", err)
		}
	})
}

func TestNewOpenAICompatibleModelRequiresTransport(t *testing.T) {
	if _, err := NewOpenAICompatibleModel(commitMessageProfile(), nil); err == nil {
		t.Fatal("NewOpenAICompatibleModel() error = nil")
	}
}

func commitMessageProfile() appconfig.ResolvedCMProfile {
	return appconfig.ResolvedCMProfile{
		Name: "work", BaseURL: "https://provider.test/v1", Model: "provider-model", APIKey: "provider-api-key", Temperature: 0.75, TimeoutMS: 300000, MaxOutputTokens: 77,
	}
}

func equalChatMessages(left, right []chatCompletionMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type recordingProviderTransport struct {
	calls    int
	request  *http.Request
	response *http.Response
	err      error
}

func (transport *recordingProviderTransport) Do(request *http.Request) (*http.Response, error) {
	transport.calls++
	transport.request = request
	return transport.response, transport.err
}

type providerTransportFunc func(*http.Request) (*http.Response, error)

func (function providerTransportFunc) Do(request *http.Request) (*http.Response, error) {
	return function(request)
}
