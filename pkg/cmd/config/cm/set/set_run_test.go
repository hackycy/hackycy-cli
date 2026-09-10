package set

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSetModuleUpdatesOnlyTheProfile(t *testing.T) {
	writer := &recordingCMSetWriter{}
	module, err := NewSet(SetDependencies{Writer: writer})
	if err != nil {
		t.Fatalf("NewSet() returned an error: %v", err)
	}
	request := SetRequest{Profile: "work", Key: "apiKey", Value: "must-not-appear"}

	result, err := module.Run(context.Background(), request)
	if err != nil || result != (SetResult{Profile: "work"}) {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
	if got, want := writer.requests, []SetRequest{request}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writer requests = %#v, want %#v", got, want)
	}
}

func TestSetModuleReturnsFailuresWithoutAResult(t *testing.T) {
	failure := errors.New("Unsupported key. Use baseURL, model, apiKey, temperature, timeoutMs, or maxOutputTokens.")
	module, err := NewSet(SetDependencies{
		Writer: &recordingCMSetWriter{err: failure},
	})
	if err != nil {
		t.Fatalf("NewSet() returned an error: %v", err)
	}

	result, err := module.Run(context.Background(), SetRequest{Profile: "work", Key: "unsupported", Value: "value"})
	if !errors.Is(err, failure) || result != (SetResult{}) {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
}

func TestNewSetRequiresTheCommandOwnedWriter(t *testing.T) {
	if _, err := NewSet(SetDependencies{}); err == nil {
		t.Fatal("NewSet() accepted a nil Writer")
	}
}
