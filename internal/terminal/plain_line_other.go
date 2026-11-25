//go:build !darwin && !linux

package terminal

import (
	"context"
	"os"
)

func readPlainFile(ctx context.Context, file *os.File, buffer []byte) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return file.Read(buffer)
}
