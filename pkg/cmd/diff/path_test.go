package diff

import (
	"errors"
	"testing"
)

func TestComparisonPathForChildRejectsInvalidUTF8WithoutCoercion(t *testing.T) {
	invalid := string([]byte{'b', 'a', 'd', '-', 0xff})
	if _, err := comparisonPathForChild("nested", invalid); !errors.Is(err, errInvalidUTF8Filename) {
		t.Fatalf("comparisonPathForChild() error = %v, want invalid UTF-8 error", err)
	}
	path, err := comparisonPathForChild("nested", "valid.txt")
	if err != nil || path != "nested/valid.txt" {
		t.Fatalf("comparisonPathForChild() = (%q, %v)", path, err)
	}
}
