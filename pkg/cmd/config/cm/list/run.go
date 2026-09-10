package list

import (
	"context"
	"errors"
)

// Dependencies are the command-owned external adapters.
type Dependencies struct {
	Reader Reader
}

// Module owns config cm list behavior behind one typed command interface.
type Module struct {
	reader Reader
}

// New constructs a config cm list command module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.Reader == nil {
		return nil, errors.New("config cm reader is required")
	}
	return &Module{reader: dependencies.Reader}, nil
}

// Run lists configured CM profiles without mutating configuration.
func (module *Module) Run(_ context.Context, _ Input) (Result, error) {
	profiles, err := Read(module.reader)
	if err != nil {
		return Result{}, err
	}
	return Result{Profiles: profiles}, nil
}
