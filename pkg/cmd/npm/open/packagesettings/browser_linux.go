package packagesettings

import (
	"context"
	"errors"
	"os/exec"
	"syscall"
	"time"
)

const xdgStartupWindow = 500 * time.Millisecond

func openURL(ctx context.Context, address string) error {
	gio, err := exec.LookPath("gio")
	if err == nil {
		return contextError(ctx, exec.CommandContext(ctx, gio, "open", address).Run())
	}
	if !errors.Is(err, exec.ErrNotFound) {
		return err
	}
	command := exec.Command("xdg-open", address)
	// A browser kept in the launcher process group should outlive terminal interrupts.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return runXDGOpen(ctx, command, xdgStartupWindow)
}

func runXDGOpen(ctx context.Context, command *exec.Cmd, startupWindow time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	// Some xdg-open handlers stay attached to the browser until it closes.
	// Reap the child if it exits, but don't wait for the browser session.
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	timer := time.NewTimer(startupWindow)
	defer timer.Stop()
	select {
	case err := <-exited:
		return contextError(ctx, err)
	case <-timer.C:
		select {
		case err := <-exited:
			return contextError(ctx, err)
		default:
			return ctx.Err()
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}
