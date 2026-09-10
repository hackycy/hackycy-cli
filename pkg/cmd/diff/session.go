package diff

import (
	"context"
	"errors"
	"sync"
)

type comparisonSession struct {
	workspace      *Workspace
	server         *RunningServer
	bindingAddress string
	lifecycle      *diffLifecycle
	shutdownOnce   sync.Once
	shutdownMu     sync.Mutex
	shutdownErr    error
}

// reportedDiffError preserves the original error chain while telling the root
// command that the service already projected the failure as a Lifecycle Log.
type reportedDiffError struct{ error }

func (reportedDiffError) AlreadyReported() bool { return true }

func (err reportedDiffError) Unwrap() error { return err.error }

func startComparison(input Input) (*comparisonSession, error) {
	return startComparisonWithLifecycle(input, nil)
}

func startComparisonWithLifecycle(input Input, lifecycle *diffLifecycle) (*comparisonSession, error) {
	workspace, err := NewWorkspace(WorkspaceOptions{
		BaselineDirectory: input.BaselineDirectory,
		TargetDirectory:   input.TargetDirectory,
		NoGitIgnore:       input.NoGitIgnore,
		Exclusions:        input.Exclusions,
	})
	if err != nil {
		return nil, err
	}
	bindingAddress := diffBindingAddress(input.Public)
	server, err := startServerWithLifecycle(workspace, bindingAddress, input.Port, lifecycle)
	if err != nil {
		return nil, err
	}
	return &comparisonSession{
		workspace:      workspace,
		server:         server,
		bindingAddress: bindingAddress,
		lifecycle:      lifecycle,
	}, nil
}

func (session *comparisonSession) wait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return session.shutdown("context-cancelled")
	case <-session.server.done:
		return session.observeServerStop()
	}
}

func (session *comparisonSession) shutdown(reason string) error {
	if session == nil || session.server == nil {
		return nil
	}
	session.shutdownOnce.Do(func() {
		if session.lifecycle != nil {
			session.lifecycle.stopping(reason)
		}
		session.server.refresh.StopAndCancel()
		closeErr := session.server.Close()
		serveErr := session.server.Wait()
		session.shutdownMu.Lock()
		session.shutdownErr = errors.Join(closeErr, serveErr)
		shutdownErr := session.shutdownErr
		session.shutdownMu.Unlock()
		if session.lifecycle != nil {
			if shutdownErr != nil {
				stage := "close"
				if closeErr == nil && serveErr != nil {
					stage = "serve"
				}
				session.lifecycle.failed(stage, shutdownErr)
			} else {
				session.lifecycle.stopped("")
			}
		}
	})
	session.shutdownMu.Lock()
	err := session.shutdownErr
	session.shutdownMu.Unlock()
	if err != nil && session.lifecycle != nil {
		return reportedDiffError{error: err}
	}
	return err
}

func (session *comparisonSession) observeServerStop() error {
	if session == nil || session.server == nil {
		return nil
	}
	session.shutdownOnce.Do(func() {
		err := session.server.Wait()
		session.shutdownMu.Lock()
		session.shutdownErr = err
		session.shutdownMu.Unlock()
		if session.lifecycle != nil {
			if err != nil {
				session.lifecycle.failed("serve", err)
			} else {
				session.lifecycle.stopped("server-stopped")
			}
		}
	})
	session.shutdownMu.Lock()
	err := session.shutdownErr
	session.shutdownMu.Unlock()
	if err != nil && session.lifecycle != nil {
		return reportedDiffError{error: err}
	}
	return err
}

func (session *comparisonSession) startupOutputFailure() error {
	if session == nil || session.server == nil {
		return nil
	}
	if session.lifecycle != nil {
		session.lifecycle.startupOutputFailureStarted()
	}
	session.server.refresh.StopAndCancel()
	closeErr := session.server.Close()
	serveErr := session.server.Wait()
	if session.lifecycle != nil {
		session.lifecycle.startupOutputFailureFinished()
	}
	return errors.Join(closeErr, serveErr)
}

func diffBindingAddress(public bool) string {
	if public {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}
