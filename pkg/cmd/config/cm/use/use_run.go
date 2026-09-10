package use

import (
	"context"
	"errors"
)

// UseDependencies are the command-owned adapters for config cm use.
type UseDependencies struct {
	Writer UseWriter
}

// UseModule owns config cm use behavior behind its typed command interface.
type UseModule struct {
	writer UseWriter
}

// NewUse constructs a config cm use command module.
func NewUse(dependencies UseDependencies) (*UseModule, error) {
	if dependencies.Writer == nil {
		return nil, errors.New("config cm use writer is required")
	}
	return &UseModule{writer: dependencies.Writer}, nil
}

// Run selects the requested CM profile without presenting its command result.
func (module *UseModule) Run(_ context.Context, request UseRequest) (UseResult, error) {
	if err := SelectDefaultProfile(module.writer, request.Profile); err != nil {
		return UseResult{}, err
	}
	return UseResult{Profile: request.Profile}, nil
}
