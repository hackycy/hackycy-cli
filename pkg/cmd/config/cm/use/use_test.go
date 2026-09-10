package use

import (
	"errors"
	"reflect"
	"testing"
)

func TestSelectDefaultProfileDelegatesTheExactProfileIdentity(t *testing.T) {
	writer := &recordingCMUseWriter{}

	if err := SelectDefaultProfile(writer, " work "); err != nil {
		t.Fatalf("SelectDefaultProfile() returned an error: %v", err)
	}

	if got, want := writer.names, []string{" work "}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writer names = %#v, want %#v", got, want)
	}
}

func TestSelectDefaultProfileReturnsTheAppconfigResult(t *testing.T) {
	failure := errors.New("CM profile not found: missing")
	writer := &recordingCMUseWriter{err: failure}

	err := SelectDefaultProfile(writer, "missing")
	if !errors.Is(err, failure) {
		t.Fatalf("SelectDefaultProfile() error = %v, want %v", err, failure)
	}
	if got, want := writer.names, []string{"missing"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writer names = %#v, want %#v", got, want)
	}
}

type recordingCMUseWriter struct {
	names []string
	err   error
}

func (writer *recordingCMUseWriter) SetDefaultCMProfile(name string) error {
	writer.names = append(writer.names, name)
	return writer.err
}
