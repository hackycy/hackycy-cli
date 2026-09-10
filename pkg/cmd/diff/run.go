package diff

import (
	"context"
	"errors"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
)

// Dependencies are the external host facts required by Diff.
type Dependencies struct {
	NetworkInterfaces func() ([]NetworkInterface, error)
	Logger            logging.Logger
	Now               func() time.Time
}

// Module owns Diff command lifecycle behind its typed command interface.
type Module struct {
	networkInterfaces func() ([]NetworkInterface, error)
	logger            logging.Logger
	now               func() time.Time
}

// New constructs an unregistered Diff command module.
func New(dependencies Dependencies) (*Module, error) {
	if dependencies.NetworkInterfaces == nil {
		return nil, errors.New("diff network interface provider is required")
	}
	now := dependencies.Now
	if now == nil {
		now = time.Now
	}
	return &Module{
		networkInterfaces: dependencies.NetworkInterfaces,
		logger:            dependencies.Logger,
		now:               now,
	}, nil
}

// Operation holds a started comparison open until its caller waits or closes it.
type Operation struct {
	Startup Startup
	session *comparisonSession
}

// Start binds a fixed comparison and returns its safe startup facts without
// assigning terminal presentation or process exit behavior.
func (module *Module) Start(ctx context.Context, input Input) (*Operation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return nil, nil
	}
	lifecycle := newDiffLifecycle(module.logger, module.now)
	session, err := startComparisonWithLifecycle(input, lifecycle)
	if err != nil {
		return nil, err
	}
	interfaces, err := module.networkInterfaces()
	if err != nil {
		lifecycle.abort()
		_ = session.server.Close()
		return nil, err
	}
	operation := &Operation{session: session, Startup: session.startupPresentation(interfaces)}
	if ctx.Err() != nil {
		lifecycle.abort()
		if err := operation.Wait(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}
	lifecycle.begin(operation.Startup, session.server.initialRun)
	return operation, nil
}

// Wait keeps the foreground server alive until its context ends or the server
// stops on its own.
func (operation *Operation) Wait(ctx context.Context) error {
	if operation == nil {
		return nil
	}
	return operation.session.wait(ctx)
}

// Close stops the foreground server when presentation cannot continue.
func (operation *Operation) Close() error {
	if operation == nil {
		return nil
	}
	return operation.session.server.Close()
}
