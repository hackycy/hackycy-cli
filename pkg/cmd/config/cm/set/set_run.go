package set

import (
	"context"
	"errors"
)

// SetDependencies are the command-owned adapters for config cm set.
type SetDependencies struct {
	Writer SetWriter
}

// SetModule owns config cm set behavior behind its typed command interface.
type SetModule struct {
	writer SetWriter
}

// NewSet constructs a config cm set command module.
func NewSet(dependencies SetDependencies) (*SetModule, error) {
	if dependencies.Writer == nil {
		return nil, errors.New("config cm set writer is required")
	}
	return &SetModule{writer: dependencies.Writer}, nil
}

// Run updates one CM profile field without presenting its command result.
func (module *SetModule) Run(_ context.Context, request SetRequest) (SetResult, error) {
	if err := SetProfileValue(module.writer, request); err != nil {
		return SetResult{}, err
	}
	return SetResult{Profile: request.Profile}, nil
}
