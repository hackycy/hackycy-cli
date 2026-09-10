package cmdutil

import "io"

// IOStreams preserves the inherited process streams supplied to command construction.
type IOStreams struct {
	In     io.Reader
	Out    io.Writer
	ErrOut io.Writer
}
