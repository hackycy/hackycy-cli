package fs

import (
	"errors"
	"sync"
	"time"
)

// RuntimeOptions selects all command-owned services for one FS process.
// CLI parsing and terminal presentation are intentionally outside this type.
type RuntimeOptions struct {
	Directory          string
	BindingAddress     string
	Port               int
	ManagementEnabled  bool
	SafeHTML           bool
	Accounts           []string
	SessionDirectory   string
	SessionIdleTimeout time.Duration
	ChunkedUploads     bool
	UploadChunkSize    int64
}

// Runtime owns the FS services that share one root-confined workspace.
type Runtime struct {
	workspace         *Workspace
	authentication    *Authentication
	chunkedUploads    *ChunkedUploadManager
	downloads         *DownloadManager
	extractions       *ExtractionManager
	thumbnails        *ThumbnailService
	bindingAddress    string
	port              int
	managementEnabled bool
	safeHTML          bool

	close           sync.Once
	closeErr        error
	shutdownMu      sync.Mutex
	shutdownSummary fsShutdownSummary
	shutdownReason  string
	lifecycle       fsRuntimeLifecycle
	failureStage    string
	failureErr      error
	shutdownCause   error
}

type fsRuntimeLifecycle interface {
	shutdownStarted(string, fsShutdownSnapshot)
	shutdownFinished(fsShutdownSummary, string, error)
}

type fsShutdownSnapshot struct {
	QueuedDownloads          int
	ActiveDownloads          int
	QueuedExtractions        int
	ActiveExtractions        int
	IncompleteChunkedUploads int
}

type fsShutdownSummary struct {
	CancelledDownloads    int
	CancelledExtractions  int
	RemovedChunkedUploads int
}

// NewRuntime constructs the unregistered FS service graph without opening a
// listener. The returned Runtime must be closed or passed to Start.
func NewRuntime(options RuntimeOptions) (*Runtime, error) {
	workspace, err := OpenWorkspace(options.Directory)
	if err != nil {
		return nil, err
	}
	authentication, err := NewAuthentication(options.Accounts, AuthenticationOptions{
		SessionDirectory: options.SessionDirectory,
		IdleLifetime:     options.SessionIdleTimeout,
	})
	if err != nil {
		_ = workspace.Close()
		return nil, err
	}
	chunkSize := options.UploadChunkSize
	if chunkSize == 0 {
		chunkSize = 8 * 1024 * 1024
	}
	thumbnails, err := NewThumbnailService(workspace)
	if err != nil {
		_ = authentication.Close()
		_ = workspace.Close()
		return nil, err
	}
	return &Runtime{
		workspace:         workspace,
		authentication:    authentication,
		downloads:         NewDownloadManager(workspace),
		extractions:       NewExtractionManager(workspace, ArchiveExtractionOptions{Inspector: NewSevenZipArchiveInspector(), Capacity: defaultArchiveCapacity}),
		thumbnails:        thumbnails,
		bindingAddress:    options.BindingAddress,
		port:              options.Port,
		managementEnabled: options.ManagementEnabled,
		safeHTML:          options.SafeHTML,
		chunkedUploads: func() *ChunkedUploadManager {
			if !options.ChunkedUploads {
				return nil
			}
			return NewChunkedUploadManager(workspace, chunkSize)
		}(),
		shutdownReason: "server-stopped",
	}, nil
}

// Start opens the runtime's listener. Server shutdown releases every
// Runtime-owned service exactly once.
func (runtime *Runtime) Start() (*RunningServer, error) {
	if runtime == nil {
		return nil, errors.New("FS runtime is required")
	}
	return StartServer(runtime.workspace, ServerOptions{
		BindingAddress: runtime.bindingAddress,
		Port:           runtime.port,
		ReadOnly: ReadOnlyServerOptions{
			ManagementEnabled: runtime.managementEnabled,
			SafeHTML:          runtime.safeHTML,
			Authentication:    runtime.authentication,
			ChunkedUploads:    runtime.chunkedUploads,
			Downloads:         runtime.downloads,
			Extractions:       runtime.extractions,
			Thumbnails:        runtime.thumbnails,
			BindingAddress:    runtime.bindingAddress,
		},
		Release:       runtime.Close,
		BeforeRelease: runtime.markShutdownFailure,
	})
}

func (runtime *Runtime) setLifecycle(lifecycle fsRuntimeLifecycle) {
	if runtime == nil {
		return
	}
	runtime.shutdownMu.Lock()
	runtime.lifecycle = lifecycle
	runtime.shutdownMu.Unlock()
}

func (runtime *Runtime) setShutdownReason(reason string) {
	if runtime == nil || reason == "" {
		return
	}
	runtime.shutdownMu.Lock()
	runtime.shutdownReason = reason
	runtime.shutdownMu.Unlock()
}

func (runtime *Runtime) markShutdownFailure(stage string, err error) {
	if runtime == nil || err == nil {
		return
	}
	runtime.shutdownMu.Lock()
	if runtime.failureErr == nil {
		runtime.failureStage = stage
		runtime.failureErr = err
	}
	runtime.shutdownMu.Unlock()
}

// recordShutdownCause retains a pre-shutdown checkpoint failure so it can be
// joined into the one lifecycle error if resource release also fails.
func (runtime *Runtime) recordShutdownCause(err error) {
	if runtime == nil || err == nil {
		return
	}
	runtime.shutdownMu.Lock()
	runtime.shutdownCause = errors.Join(runtime.shutdownCause, err)
	runtime.shutdownMu.Unlock()
}

func (runtime *Runtime) lifecycleSnapshot() fsShutdownSnapshot {
	if runtime == nil {
		return fsShutdownSnapshot{}
	}
	snapshot := fsShutdownSnapshot{}
	if runtime.downloads != nil {
		value := runtime.downloads.lifecycleSnapshot()
		snapshot.QueuedDownloads += value.QueuedDownloads
		snapshot.ActiveDownloads += value.ActiveDownloads
	}
	if runtime.extractions != nil {
		value := runtime.extractions.lifecycleSnapshot()
		snapshot.QueuedExtractions += value.QueuedExtractions
		snapshot.ActiveExtractions += value.ActiveExtractions
	}
	if runtime.chunkedUploads != nil {
		value := runtime.chunkedUploads.lifecycleSnapshot()
		snapshot.IncompleteChunkedUploads += value.IncompleteChunkedUploads
	}
	return snapshot
}

func (runtime *Runtime) shutdownResult() fsShutdownSummary {
	if runtime == nil {
		return fsShutdownSummary{}
	}
	runtime.shutdownMu.Lock()
	defer runtime.shutdownMu.Unlock()
	return runtime.shutdownSummary
}

// Close stops all owned services before releasing the Browse Root handle.
func (runtime *Runtime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.close.Do(func() {
		runtime.shutdownMu.Lock()
		lifecycle := runtime.lifecycle
		reason := runtime.shutdownReason
		failureStage := runtime.failureStage
		failureErr := runtime.failureErr
		shutdownCause := runtime.shutdownCause
		runtime.shutdownMu.Unlock()
		if lifecycle != nil {
			lifecycle.shutdownStarted(reason, runtime.lifecycleSnapshot())
		}
		var result error
		summary := fsShutdownSummary{}
		if runtime.chunkedUploads != nil {
			removed, closeErr := runtime.chunkedUploads.closeWithStats()
			summary.RemovedChunkedUploads = removed
			result = errors.Join(result, closeErr)
		}
		if runtime.downloads != nil {
			queued, active := runtime.downloads.closeWithStats()
			summary.CancelledDownloads = queued + active
		}
		if runtime.extractions != nil {
			queued, active := runtime.extractions.closeWithStats()
			summary.CancelledExtractions = queued + active
		}
		if runtime.thumbnails != nil {
			runtime.thumbnails.Close()
		}
		if runtime.authentication != nil {
			result = errors.Join(result, runtime.authentication.Close())
		}
		if runtime.workspace != nil {
			result = errors.Join(result, runtime.workspace.Close())
		}
		runtime.shutdownMu.Lock()
		runtime.closeErr = result
		runtime.shutdownSummary = summary
		if failureErr != nil || result != nil {
			if failureStage == "" {
				failureStage = "release"
			}
			failureErr = errors.Join(shutdownCause, failureErr, result)
		}
		runtime.shutdownMu.Unlock()
		if lifecycle != nil {
			lifecycle.shutdownFinished(summary, failureStage, failureErr)
		}
	})
	runtime.shutdownMu.Lock()
	defer runtime.shutdownMu.Unlock()
	return runtime.closeErr
}
