package gitprocess

import (
	"errors"
	"io/fs"
	"os/exec"
	"testing"
)

func TestNormalizeProcessStartErrorPreservesMessageAndMissingContract(t *testing.T) {
	original := &exec.Error{Name: "missing-tool", Err: exec.ErrNotFound}
	normalized := normalizeProcessStartError(original)
	if normalized.Error() != original.Error() {
		t.Fatalf("error text = %q, want %q", normalized, original)
	}
	if !errors.Is(normalized, fs.ErrNotExist) || !errors.Is(normalized, exec.ErrNotFound) {
		t.Fatalf("normalized error = %v, want both filesystem and exec missing identities", normalized)
	}
	if normalizeProcessStartError(errors.New("permission denied")).Error() != "permission denied" {
		t.Fatal("non-missing start error was rewritten")
	}
}
