//go:build darwin || linux

package terminal

import (
	"context"
	"os"

	"golang.org/x/sys/unix"
)

func readPlainFile(ctx context.Context, file *os.File, buffer []byte) (int, error) {
	fd := []unix.PollFd{{Fd: int32(file.Fd()), Events: unix.POLLIN}}
	// CLI signal handling cancels the context without closing inherited stdin.
	// Poll in bounded intervals so an unanswered prompt can return on a signal.
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		ready, err := unix.Poll(fd, 100)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return 0, err
		}
		if ready > 0 {
			return file.Read(buffer)
		}
	}
}
