package env

import "testing"

func TestPresentReportsAndPrintsStdoutOutput(t *testing.T) {
	presenter := &recordingPresenter{}

	Present(presenter, "{\"VALUE\":\"value\"}", "")

	if len(presenter.outros) != 1 || presenter.outros[0] != "Exported variables:" {
		t.Fatalf("outros = %#v", presenter.outros)
	}
	if len(presenter.printed) != 1 || presenter.printed[0] != "{\"VALUE\":\"value\"}" {
		t.Fatalf("printed = %#v", presenter.printed)
	}
}

func TestPresentReportsOutputTargetWithoutPrintingJSON(t *testing.T) {
	presenter := &recordingPresenter{}

	Present(presenter, "{\"VALUE\":\"value\"}", "nested/output.json")

	if len(presenter.outros) != 1 || presenter.outros[0] != "Writing output to nested/output.json" {
		t.Fatalf("outros = %#v", presenter.outros)
	}
	if len(presenter.printed) != 0 {
		t.Fatalf("printed = %#v", presenter.printed)
	}
}

func TestPresentCancellationReportsLegacyMessage(t *testing.T) {
	presenter := &recordingPresenter{}

	PresentCancellation(presenter)

	if len(presenter.alerts) != 1 || presenter.alerts[0] != "Cancelled" {
		t.Fatalf("alerts = %#v", presenter.alerts)
	}
}

type recordingPresenter struct {
	outros  []string
	printed []string
	alerts  []string
}

func (presenter *recordingPresenter) Outro(message string) {
	presenter.outros = append(presenter.outros, message)
}

func (presenter *recordingPresenter) Print(value string) {
	presenter.printed = append(presenter.printed, value)
}

func (presenter *recordingPresenter) Cancel(message string) {
	presenter.alerts = append(presenter.alerts, message)
}
