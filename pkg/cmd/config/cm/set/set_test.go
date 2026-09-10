package set

import (
	"errors"
	"reflect"
	"testing"
)

func TestSetProfileValueDelegatesStringFieldRequestsUnchanged(t *testing.T) {
	writer := &recordingCMSetWriter{}
	requests := []SetRequest{
		{Profile: "work", Key: "baseURL", Value: " https://provider.example/v2/// "},
		{Profile: "work", Key: "model", Value: "  "},
	}

	for _, request := range requests {
		if err := SetProfileValue(writer, request); err != nil {
			t.Fatalf("SetProfileValue(%#v) returned an error: %v", request, err)
		}
	}

	if got, want := writer.requests, requests; !reflect.DeepEqual(got, want) {
		t.Fatalf("writer requests = %#v, want %#v", got, want)
	}
}

func TestSetProfileValueReturnsTheAppconfigResult(t *testing.T) {
	failure := errors.New("save failed")
	writer := &recordingCMSetWriter{err: failure}
	request := SetRequest{Profile: "work", Key: "model", Value: "next"}

	err := SetProfileValue(writer, request)
	if !errors.Is(err, failure) {
		t.Fatalf("SetProfileValue() error = %v, want %v", err, failure)
	}
	if got, want := writer.requests, []SetRequest{request}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writer requests = %#v, want %#v", got, want)
	}
}

type recordingCMSetWriter struct {
	requests []SetRequest
	err      error
}

func (writer *recordingCMSetWriter) SetCMProfileValue(name, key, value string) error {
	writer.requests = append(writer.requests, SetRequest{Profile: name, Key: key, Value: value})
	return writer.err
}
