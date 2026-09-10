package pulse

import "context"

// PhaseKind identifies one command-owned contiguous git pulse work segment.
type PhaseKind uint8

const (
	PhaseScan PhaseKind = iota
	PhaseFetch
	PhasePrepare
	PhaseBuild
)

// PhaseState records the command-owned terminal state of one phase update.
type PhaseState uint8

const (
	PhaseActive PhaseState = iota
	PhaseCompleted
	PhaseCancelled
	PhaseFailed
)

// Phase is one externally meaningful git pulse progress update.
type Phase struct {
	Kind            PhaseKind
	State           PhaseState
	Root            string
	Repository      string
	Completed       int
	Total           int
	Successful      int
	CommitCount     int
	RepositoryCount int
	Detail          string
}

// PhaseReporter receives typed phase updates for one contiguous work segment.
type PhaseReporter interface {
	Report(Phase)
	Close() error
}

// Tracker opens one command-owned phase segment without choosing a terminal runtime.
type Tracker interface {
	Start(context.Context, PhaseKind) (PhaseReporter, error)
}

func (module *Module) track(ctx context.Context, initial Phase, work func(func(Phase)) error) (err error) {
	reporter, err := module.tracker.Start(ctx, initial.Kind)
	if err != nil {
		return err
	}
	last := initial
	reporter.Report(last)
	defer func() {
		final := last
		switch {
		case err == nil:
			final.State = PhaseCompleted
		case ctx.Err() != nil:
			final.State = PhaseCancelled
		default:
			final.State = PhaseFailed
		}
		reporter.Report(final)
		if closeErr := reporter.Close(); err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	return work(func(phase Phase) {
		last = phase
		reporter.Report(phase)
	})
}
