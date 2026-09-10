package rm

import "fmt"

// Presenter renders rm command progress and outcomes.
type Presenter interface {
	Intro(string)
	Paths([]string)
	Notice(string)
	ProgressStart(string)
	ProgressStop(string)
	Cancel(string)
	Outro(string)
}

func presentIntroduction(presenter Presenter) {
	presenter.Intro("Remove")
}

func presentMissingPaths(presenter Presenter, paths []string) {
	for _, path := range paths {
		presenter.Notice(fmt.Sprintf("  not found, skipping: %s", path))
	}
}

func presentExplicitPaths(presenter Presenter, paths []string) {
	presenter.Paths(paths)
}

func presentNoValidPaths(presenter Presenter) {
	presenter.Cancel("No valid paths to delete.")
}

func presentCancellation(presenter Presenter) {
	presenter.Cancel("Cancelled.")
}

func presentNothingSelected(presenter Presenter) {
	presenter.Cancel("Nothing selected.")
}

func presentScanStart(presenter Presenter) {
	presenter.ProgressStart("Scanning...")
}

func presentScanStop(presenter Presenter, count int) {
	if count == 0 {
		presenter.ProgressStop("No targets found.")
		return
	}
	presenter.ProgressStop(fmt.Sprintf("Found %d target%s", count, pluralSuffix(count)))
}

func presentNothingToClean(presenter Presenter) {
	presenter.Outro("Nothing to clean.")
}

func presentDeleteStart(presenter Presenter, count int) {
	presenter.ProgressStart(fmt.Sprintf("Deleting %d item%s...", count, pluralSuffix(count)))
}

func presentDeleteResult(presenter Presenter, result deletionResult) {
	presenter.ProgressStop(fmt.Sprintf("Deleted %d item%s", result.succeeded, pluralSuffix(result.succeeded)))
	for _, failure := range result.failures {
		presenter.Notice(fmt.Sprintf("  skipped: %s", failure))
	}
	presenter.Outro("Done!")
}
