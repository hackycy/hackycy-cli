package run

// Input is the typed request for run.
type Input struct {
	Directory string
}

// Result records a completed run command outcome.
type Result struct {
	ExitCode int
}
