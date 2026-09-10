package test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hackycy/hackycy-cli/internal/appconfig"
)

// cmTestProviderTransport is the command-owned HTTP boundary for config cm test.
type cmTestProviderTransport interface {
	Do(*http.Request) (*http.Response, error)
}

type cmTestProviderFailureCategory string

const (
	cmTestProviderFailureHTTPStatus cmTestProviderFailureCategory = "http-status"
	cmTestProviderFailureTimeout    cmTestProviderFailureCategory = "timeout"
	cmTestProviderFailureRead       cmTestProviderFailureCategory = "read"
	cmTestProviderFailureDecode     cmTestProviderFailureCategory = "decode"
	cmTestProviderFailureEmpty      cmTestProviderFailureCategory = "empty-response"
)

type cmTestProviderError struct {
	category cmTestProviderFailureCategory
	message  string
	source   error
}

func (err *cmTestProviderError) Error() string {
	return err.message
}

func (err *cmTestProviderError) Unwrap() error {
	return err.source
}

func newCMTestProviderError(category cmTestProviderFailureCategory, message string, source error) error {
	return &cmTestProviderError{category: category, message: message, source: source}
}

func cmTestProviderFailureKind(err error) cmTestProviderFailureCategory {
	var providerErr *cmTestProviderError
	if errors.As(err, &providerErr) {
		return providerErr.category
	}
	return cmTestProviderFailureRead
}

func executeCMTestProvider(ctx context.Context, profile appconfig.ResolvedCMProfile, transport cmTestProviderTransport) (cmTestProviderResult, error) {
	timeout := time.Duration(profile.TimeoutMS * float64(time.Millisecond))
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := newCMTestProviderRequest(requestContext, profile)
	if err != nil {
		return cmTestProviderResult{}, newCMTestProviderError(cmTestProviderFailureRead, "Unable to create CM test provider request", err)
	}
	response, err := transport.Do(request)
	if err != nil {
		if errors.Is(err, context.Canceled) && errors.Is(requestContext.Err(), context.Canceled) {
			return cmTestProviderResult{}, err
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return cmTestProviderResult{}, newCMTestProviderError(cmTestProviderFailureTimeout, fmt.Sprintf("Provider request timed out after %sms", strconv.FormatFloat(profile.TimeoutMS, 'f', -1, 64)), err)
		}
		return cmTestProviderResult{}, newCMTestProviderError(cmTestProviderFailureRead, "Provider request failed", err)
	}
	if response == nil {
		return cmTestProviderResult{}, newCMTestProviderError(cmTestProviderFailureRead, "CM test provider returned no response", nil)
	}
	if response.Body == nil {
		return cmTestProviderResult{}, newCMTestProviderError(cmTestProviderFailureRead, "CM test provider returned no response body", nil)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return cmTestProviderResult{}, cmTestHTTPError(response)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		if errors.Is(err, context.Canceled) && errors.Is(requestContext.Err(), context.Canceled) {
			return cmTestProviderResult{}, err
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return cmTestProviderResult{}, newCMTestProviderError(cmTestProviderFailureTimeout, fmt.Sprintf("Provider request timed out after %sms", strconv.FormatFloat(profile.TimeoutMS, 'f', -1, 64)), err)
		}
		return cmTestProviderResult{}, newCMTestProviderError(cmTestProviderFailureRead, "Unable to read CM test provider response", err)
	}
	return decodeCMTestProviderResponse(body)
}

func cmTestHTTPError(response *http.Response, _ ...[]byte) error {
	statusText := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(response.Status), strconv.Itoa(response.StatusCode)))
	if statusText == "" {
		statusText = http.StatusText(response.StatusCode)
	}
	return newCMTestProviderError(cmTestProviderFailureHTTPStatus, fmt.Sprintf("%d %s", response.StatusCode, safeCMTestStatus(statusText)), nil)
}
