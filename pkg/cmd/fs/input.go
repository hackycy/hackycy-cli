package fs

import "time"

// Input is the typed FS command request after the CLI binder applies defaults.
type Input struct {
	Directory          string
	Port               int
	Address            string
	ManagementEnabled  bool
	SafeHTML           bool
	Accounts           []string
	SessionDirectory   string
	SessionIdleTimeout time.Duration
	ChunkedUploads     bool
	UploadChunkSize    int64
}

// Result records a completed FS command outcome.
type Result struct{}
