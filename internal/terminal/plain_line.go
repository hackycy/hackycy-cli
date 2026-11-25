package terminal

import (
	"context"
	"io"
	"os"
)

// plainLineReader retains the invocation's buffered input between prompts.
// The context is scoped to a single ReadString by the interaction handler.
type plainLineReader struct {
	input io.Reader
	ctx   context.Context
}

func (reader *plainLineReader) Read(buffer []byte) (int, error) {
	if file, ok := reader.input.(*os.File); ok && reader.ctx != nil && reader.ctx.Done() != nil {
		return readPlainFile(reader.ctx, file, buffer)
	}
	return reader.input.Read(buffer)
}
