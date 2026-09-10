package root

import (
	"errors"
	"testing"
)

func TestRootDoesNotDuplicateAServiceFailureAlreadyProjectedToLifecycleLog(t *testing.T) {
	app, output, diagnostics, _ := testApp(t, nil)
	original := errors.New("serve failed")
	err := alreadyReportedTestError{error: original}
	outcome := app.execute(func() error { return err })
	if outcome.Code != 1 || !errors.Is(outcome.Err, original) {
		t.Fatalf("outcome = %#v", outcome)
	}
	if output.Len() != 0 || diagnostics.Len() != 0 {
		t.Fatalf("duplicate root output: stdout = %q, stderr = %q", output.String(), diagnostics.String())
	}
}

type alreadyReportedTestError struct{ error }

func (alreadyReportedTestError) AlreadyReported() bool { return true }

func (err alreadyReportedTestError) Unwrap() error { return err.error }
