package zip

import "fmt"

// Presenter renders ZIP planning, progress, and normal-result outcomes.
type Presenter interface {
	Intro()
	Note(PlanningNote)
	Progress(string)
	Cancel(string)
	Outro(string)
}

type discardPresenter struct{}

func (discardPresenter) Intro()            {}
func (discardPresenter) Note(PlanningNote) {}
func (discardPresenter) Progress(string)   {}
func (discardPresenter) Cancel(string)     {}
func (discardPresenter) Outro(string)      {}

func presentIntroduction(presenter Presenter) {
	presenter.Intro()
}

func presentPlanningNote(presenter Presenter, note PlanningNote) {
	presenter.Note(note)
}

func presentCancellation(presenter Presenter) {
	presenter.Cancel("Operation cancelled.")
}

func presentDirectoryNotFound(presenter Presenter, path string) {
	presenter.Cancel("Directory not found: " + path)
}

func presentPathNotDirectory(presenter Presenter, path string) {
	presenter.Cancel("Path is not a directory: " + path)
}

func presentCollectionStart(presenter Presenter) {
	presenter.Progress("Collecting files...")
}

func presentCollectionFailure(presenter Presenter, err error) {
	presenter.Progress("File collection failed.")
	presenter.Cancel(fmt.Sprintf("Error reading directory: %s", err))
}

func presentNoFiles(presenter Presenter) {
	presenter.Progress("No files found to zip.")
	presenter.Cancel("No files matched the selected patterns.")
}

func presentCollectedFiles(presenter Presenter, count int) {
	presenter.Progress(fmt.Sprintf("Collected %d file%s", count, pluralSuffix(count)))
}

func presentCompressionStart(presenter Presenter) {
	presenter.Progress("Compressing...")
}

func presentNoValidFiles(presenter Presenter) {
	presenter.Progress("No files available to compress.")
	presenter.Cancel(errNoValidArchiveFiles.Error())
}

func presentCompressionFailure(presenter Presenter, err error) {
	presenter.Progress("Compression failed.")
	presenter.Cancel(err.Error())
}

func presentCompressionComplete(presenter Presenter, count int) {
	presenter.Progress(fmt.Sprintf("Compression complete (%d file%s)", count, pluralSuffix(count)))
}

func presentWritingStart(presenter Presenter) {
	presenter.Progress("Writing zip file...")
}

func presentWriteFailure(presenter Presenter, err error) {
	presenter.Progress("Write failed.")
	presenter.Cancel(fmt.Sprintf("Failed to write zip: %s", err))
}

func presentSavedArchive(presenter Presenter, outputPath string) {
	presenter.Progress("Saved " + outputPath)
	presenter.Outro("Done!")
}
