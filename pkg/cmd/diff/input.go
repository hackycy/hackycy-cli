package diff

// Input is the typed command request after the CLI binder applies defaults.
type Input struct {
	BaselineDirectory string
	TargetDirectory   string
	Port              int
	Public            bool
	Exclusions        []string
	NoGitIgnore       bool
}

// Result records a completed Diff command outcome.
type Result struct{}
