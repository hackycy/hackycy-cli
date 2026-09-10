package test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

func TestExecuteCMTestProviderUsesTheInjectedLocalTransport(t *testing.T) {
	transport := &recordingCMTestTransport{response: &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Body:       io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"}}]}`)),
	}}
	result, err := executeCMTestProvider(context.Background(), cmTestProfile(), transport)
	if err != nil {
		t.Fatalf("executeCMTestProvider() returned an error: %v", err)
	}
	if result.Content != "ok" {
		t.Fatalf("result content = %q, want ok", result.Content)
	}
	if transport.request == nil || transport.request.URL.String() != "https://provider.test/v1/chat/completions" {
		t.Fatalf("transport request = %#v", transport.request)
	}
}

func TestExecuteCMTestProviderReportsTimeoutUsingTheEffectiveMilliseconds(t *testing.T) {
	profile := cmTestProfile()
	profile.TimeoutMS = 1.5
	transport := cmTestTransportFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})

	_, err := executeCMTestProvider(context.Background(), profile, transport)
	if err == nil || err.Error() != "Provider request timed out after 1.5ms" {
		t.Fatalf("executeCMTestProvider() error = %v, want timeout", err)
	}
}

func TestExecuteCMTestProviderRetainsHTTPStatusTextWithoutResponseBody(t *testing.T) {
	body := &unreadCMTestBody{}
	transport := cmTestTransportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusTooManyRequests,
			Status:     "429 Provider Busy",
			Body:       body,
		}, nil
	})

	_, err := executeCMTestProvider(context.Background(), cmTestProfile(), transport)
	if err == nil || err.Error() != "429 Provider Busy" || strings.Contains(err.Error(), "try later") {
		t.Fatalf("executeCMTestProvider() error = %v, want status without response body", err)
	}
	if got, want := cmTestProviderFailureKind(err), cmTestProviderFailureHTTPStatus; got != want {
		t.Fatalf("provider failure category = %q, want %q", got, want)
	}
	if body.reads != 0 || !body.closed {
		t.Fatalf("HTTP error body use = reads=%d closed=%t", body.reads, body.closed)
	}
}

func TestExecuteCMTestProviderReturnsResponseReadAndJSONFailures(t *testing.T) {
	t.Run("response read", func(t *testing.T) {
		failure := errors.New("read failed")
		transport := cmTestTransportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: failingCMTestBody{err: failure}}, nil
		})

		_, err := executeCMTestProvider(context.Background(), cmTestProfile(), transport)
		if !errors.Is(err, failure) {
			t.Fatalf("executeCMTestProvider() error = %v, want %v", err, failure)
		}
		if got, want := cmTestProviderFailureKind(err), cmTestProviderFailureRead; got != want {
			t.Fatalf("provider failure category = %q, want %q", got, want)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		transport := cmTestTransportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":`))}, nil
		})

		_, err := executeCMTestProvider(context.Background(), cmTestProfile(), transport)
		if err == nil || !strings.Contains(err.Error(), "decode CM test provider response") {
			t.Fatalf("executeCMTestProvider() error = %v, want JSON decoding failure", err)
		}
		if got, want := cmTestProviderFailureKind(err), cmTestProviderFailureDecode; got != want {
			t.Fatalf("provider failure category = %q, want %q", got, want)
		}
	})
}

func TestExecuteCMTestProviderClassifiesEmptyAndCancellationOutcomes(t *testing.T) {
	t.Run("empty response", func(t *testing.T) {
		transport := cmTestTransportFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"choices":[]}`))}, nil
		})
		_, err := executeCMTestProvider(context.Background(), cmTestProfile(), transport)
		if err == nil || cmTestProviderFailureKind(err) != cmTestProviderFailureEmpty {
			t.Fatalf("executeCMTestProvider() error = %v, want empty response category", err)
		}
	})

	t.Run("cancelled exchange", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		transport := cmTestTransportFunc(func(request *http.Request) (*http.Response, error) {
			cancel()
			<-request.Context().Done()
			return nil, request.Context().Err()
		})
		_, err := executeCMTestProvider(ctx, cmTestProfile(), transport)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("executeCMTestProvider() error = %v, want context cancellation", err)
		}
	})

	t.Run("timeout while reading", func(t *testing.T) {
		profile := cmTestProfile()
		profile.TimeoutMS = 1.5
		transport := cmTestTransportFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: waitingCMTestBody{context: request.Context()}}, nil
		})
		_, err := executeCMTestProvider(context.Background(), profile, transport)
		if err == nil || cmTestProviderFailureKind(err) != cmTestProviderFailureTimeout {
			t.Fatalf("executeCMTestProvider() error = %v, want timeout category", err)
		}
	})
}

func cmTestProfile() appconfig.ResolvedCMProfile {
	return appconfig.ResolvedCMProfile{
		Name:            "work",
		BaseURL:         "https://provider.test/v1",
		Model:           "provider-model",
		APIKey:          "test-api-key",
		Temperature:     0.2,
		TimeoutMS:       300000,
		MaxOutputTokens: 1000,
	}
}

type recordingCMTestTransport struct {
	request  *http.Request
	response *http.Response
	err      error
}

func (transport *recordingCMTestTransport) Do(request *http.Request) (*http.Response, error) {
	transport.request = request
	return transport.response, transport.err
}

type cmTestTransportFunc func(*http.Request) (*http.Response, error)

func (transport cmTestTransportFunc) Do(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type failingCMTestBody struct {
	err error
}

func (body failingCMTestBody) Read([]byte) (int, error) {
	return 0, body.err
}

func (failingCMTestBody) Close() error {
	return nil
}

type unreadCMTestBody struct {
	reads  int
	closed bool
}

func (body *unreadCMTestBody) Read([]byte) (int, error) {
	body.reads++
	return 0, errors.New("HTTP error body must not be read")
}

func (body *unreadCMTestBody) Close() error {
	body.closed = true
	return nil
}

type waitingCMTestBody struct {
	context context.Context
}

func (body waitingCMTestBody) Read([]byte) (int, error) {
	<-body.context.Done()
	return 0, body.context.Err()
}

func (waitingCMTestBody) Close() error {
	return nil
}
