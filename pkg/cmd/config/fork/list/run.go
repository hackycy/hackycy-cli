package list

import (
	"context"
	"errors"
)

// Dependencies are the command-owned external adapters.
type Dependencies struct {
	Reader Reader
}

// Module owns config fork list behavior behind one typed command interface.
type Module struct {
	reader Reader
}

// New constructs a config fork list command module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.Reader == nil {
		return nil, errors.New("config fork reader is required")
	}
	return &Module{reader: dependencies.Reader}, nil
}

// Run lists configured Fork instances without mutating configuration.
func (module *Module) Run(_ context.Context, _ Input) (Result, error) {
	instances, err := Read(module.reader)
	if err != nil {
		return Result{}, err
	}
	return Result{Instances: instances}, nil
}
