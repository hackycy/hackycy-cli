package use

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestUseModuleSelectsTheRequestedProfile(t *testing.T) {
	writer := &recordingCMUseWriter{}
	module, err := NewUse(UseDependencies{Writer: writer})
	if err != nil {
		t.Fatalf("NewUse() returned an error: %v", err)
	}

	result, err := module.Run(context.Background(), UseRequest{Profile: "work"})
	if err != nil || result != (UseResult{Profile: "work"}) {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
	if got, want := writer.names, []string{"work"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("writer names = %#v, want %#v", got, want)
	}
}

func TestUseModuleReturnsSelectionFailuresWithoutAResult(t *testing.T) {
	failure := errors.New("CM profile not found: missing")
	module, err := NewUse(UseDependencies{
		Writer: &recordingCMUseWriter{err: failure},
	})
	if err != nil {
		t.Fatalf("NewUse() returned an error: %v", err)
	}

	result, err := module.Run(context.Background(), UseRequest{Profile: "missing"})
	if !errors.Is(err, failure) || result != (UseResult{}) {
		t.Fatalf("Run() = (%#v, %v)", result, err)
	}
}

func TestNewUseRequiresTheCommandOwnedWriter(t *testing.T) {
	if _, err := NewUse(UseDependencies{}); err == nil {
		t.Fatal("NewUse() accepted a nil Writer")
	}
}
