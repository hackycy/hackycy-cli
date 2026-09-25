package connect

import (
	"context"
	"os"
	"testing"

	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestClientReconcilerRejectsSameRevisionWithDifferentRuntimeDigest(t *testing.T) {
	directory := t.TempDir()
	runtime := &clientFRPRuntimeStub{}
	reconciler, err := NewClientReconciler(ClientReconcilerOptions{StateDirectory: directory, Runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	first := clientDesiredState(3, true)
	if err := reconciler.Apply(context.Background(), first); err != nil {
		t.Fatalf("first Apply() error = %v", err)
	}
	second := first
	second.Runtime.AdvertisedFRPHost = "other.example.test"
	second.Runtime.Digest, _ = tunnelruntime.RuntimeDigest(second.Runtime)
	if err := reconciler.Apply(context.Background(), second); clientReconciliationErrorCode(err) != "PROTOCOL_FAILED" {
		t.Fatalf("same revision different runtime error = %v", err)
	}
	if accepted, found := ReadClientAcceptedState(directory); !found || accepted.Revision != 3 {
		t.Fatalf("accepted state = (%#v, %t)", accepted, found)
	}
}

func TestClientReconcilerNeverAppliesBelowPersistedHighestAcceptedRuntime(t *testing.T) {
	directory := t.TempDir()
	runtime := &clientFRPRuntimeStub{}
	highest := clientDesiredState(5, true)
	if err := WriteClientAcceptedState(directory, ClientAppliedState{ClientDesiredConfiguration: highest, Revision: 5}); err != nil {
		t.Fatal(err)
	}
	reconciler, err := NewClientReconciler(ClientReconcilerOptions{StateDirectory: directory, Runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	_, err = reconciler.ApplyWithResult(context.Background(), clientDesiredState(4, true))
	if clientReconciliationErrorCode(err) != "PROTOCOL_FAILED" {
		t.Fatalf("older runtime error = %v, want protocol failure", err)
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("runtime calls = %v, want none", runtime.calls)
	}
}

func TestClientReconcilerDoesNotPersistConflictingRevisionBeforeRejectingIt(t *testing.T) {
	directory := t.TempDir()
	reconciler, err := NewClientReconciler(ClientReconcilerOptions{StateDirectory: directory, Runtime: &clientFRPRuntimeStub{}})
	if err != nil {
		t.Fatal(err)
	}
	first := clientDesiredState(3, true)
	if err := reconciler.Apply(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(clientAcceptedStatePath(directory)); err != nil {
		t.Fatal(err)
	}
	conflict := first
	conflict.Runtime.AdvertisedFRPHost = "other.example.test"
	conflict.Runtime.Digest, _ = tunnelruntime.RuntimeDigest(conflict.Runtime)
	if err := reconciler.Apply(context.Background(), conflict); clientReconciliationErrorCode(err) != "PROTOCOL_FAILED" {
		t.Fatalf("conflicting revision error = %v", err)
	}
	if _, found := ReadClientAcceptedState(directory); found {
		t.Fatal("conflicting revision created a new high-water record")
	}
}

func TestClientReconcilerRejectsCorruptHighestAcceptedState(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(clientAcceptedStatePath(directory), []byte(`{"revision":9,"runtime":`), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := &clientFRPRuntimeStub{}
	reconciler, err := NewClientReconciler(ClientReconcilerOptions{StateDirectory: directory, Runtime: runtime})
	if err != nil {
		t.Fatal(err)
	}
	if err := reconciler.Apply(context.Background(), clientDesiredState(1, true)); clientReconciliationErrorCode(err) != "STATE_CORRUPT" {
		t.Fatalf("corrupt high-water error = %v", err)
	}
	if len(runtime.calls) != 0 {
		t.Fatalf("runtime calls after corrupt high-water = %v", runtime.calls)
	}
}
