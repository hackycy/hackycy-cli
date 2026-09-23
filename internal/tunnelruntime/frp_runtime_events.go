package tunnelruntime

import (
	"io"
	"time"
)

const (
	frpDownloadProgressPercentInterval = 5
	frpDownloadProgressTimeInterval    = 2 * time.Second
)

// FRPRuntimeEventType identifies one observable runtime preparation step.
type FRPRuntimeEventType string

const (
	FRPRuntimeEventReuse            FRPRuntimeEventType = "reuse"
	FRPRuntimeEventDownloadStart    FRPRuntimeEventType = "download_start"
	FRPRuntimeEventDownloadProgress FRPRuntimeEventType = "download_progress"
	FRPRuntimeEventDownloadDone     FRPRuntimeEventType = "download_done"
	FRPRuntimeEventVerifyArchive    FRPRuntimeEventType = "verify_archive"
	FRPRuntimeEventExtract          FRPRuntimeEventType = "extract"
	FRPRuntimeEventPublish          FRPRuntimeEventType = "publish"
	FRPRuntimeEventProbe            FRPRuntimeEventType = "probe"
	FRPRuntimeEventReady            FRPRuntimeEventType = "ready"
	FRPRuntimeEventFailed           FRPRuntimeEventType = "failed"
)

// FRPRuntimeFailureStage identifies the preparation step that failed.
type FRPRuntimeFailureStage string

const (
	FRPRuntimeFailureValidate      FRPRuntimeFailureStage = "validate"
	FRPRuntimeFailureDownload      FRPRuntimeFailureStage = "download"
	FRPRuntimeFailureVerifyArchive FRPRuntimeFailureStage = "verify_archive"
	FRPRuntimeFailureExtract       FRPRuntimeFailureStage = "extract"
	FRPRuntimeFailurePublish       FRPRuntimeFailureStage = "publish"
	FRPRuntimeFailureProbe         FRPRuntimeFailureStage = "probe"
)

// FRPRuntimeEvent contains the presentation-safe facts for one preparation
// step. Only the fields documented for Type are populated.
type FRPRuntimeEvent struct {
	Type FRPRuntimeEventType

	Version    string
	Archive    string
	URL        string
	Directory  string
	FRPC       string
	FRPS       string
	Binary     string
	SHA256     string
	FRPCSHA256 string
	FRPSSHA256 string

	ReceivedBytes int64
	TotalBytes    *int64
	Percent       *int
	Elapsed       time.Duration
	Downloaded    bool
	FailureStage  FRPRuntimeFailureStage
}

// DiagnosticFields projects an event to its stable structured logging fields.
func (event FRPRuntimeEvent) DiagnosticFields() map[string]any {
	switch event.Type {
	case FRPRuntimeEventReuse:
		return map[string]any{"version": event.Version, "directory": event.Directory}
	case FRPRuntimeEventDownloadStart:
		return map[string]any{"version": event.Version, "archive": event.Archive, "url": event.URL, "directory": event.Directory}
	case FRPRuntimeEventDownloadProgress:
		fields := map[string]any{"receivedBytes": event.ReceivedBytes}
		if event.TotalBytes != nil {
			fields["totalBytes"] = *event.TotalBytes
		}
		if event.Percent != nil {
			fields["percent"] = *event.Percent
		}
		return fields
	case FRPRuntimeEventDownloadDone:
		return map[string]any{"receivedBytes": event.ReceivedBytes, "elapsedMs": event.Elapsed.Milliseconds()}
	case FRPRuntimeEventVerifyArchive:
		return map[string]any{"sha256": event.SHA256}
	case FRPRuntimeEventExtract:
		return map[string]any{"archive": event.Archive, "frpcSha256": event.FRPCSHA256, "frpsSha256": event.FRPSSHA256}
	case FRPRuntimeEventPublish:
		return map[string]any{"directory": event.Directory}
	case FRPRuntimeEventProbe:
		return map[string]any{"binary": event.Binary, "version": event.Version}
	case FRPRuntimeEventReady:
		return map[string]any{"directory": event.Directory, "frpc": event.FRPC, "frps": event.FRPS, "downloaded": event.Downloaded}
	case FRPRuntimeEventFailed:
		return map[string]any{"stage": string(event.FailureStage)}
	default:
		return nil
	}
}

// FRPRuntimeObserver receives synchronous preparation events. A nil observer
// disables reporting without changing runtime preparation behavior.
type FRPRuntimeObserver func(FRPRuntimeEvent)

func (observer FRPRuntimeObserver) report(event FRPRuntimeEvent) {
	if observer != nil {
		observer(event)
	}
}

type frpDownloadProgressReader struct {
	reader            io.Reader
	observer          FRPRuntimeObserver
	totalBytes        *int64
	receivedBytes     int64
	lastReportedBytes int64
	lastPercent       int
	lastReportedAt    time.Time
	reports           int
	now               func() time.Time
}

func newFRPDownloadProgressReader(reader io.Reader, contentLength int64, observer FRPRuntimeObserver, now func() time.Time) *frpDownloadProgressReader {
	if now == nil {
		now = time.Now
	}
	progress := &frpDownloadProgressReader{reader: reader, observer: observer, now: now}
	if contentLength >= 0 {
		totalBytes := contentLength
		progress.totalBytes = &totalBytes
	}
	return progress
}

func (progress *frpDownloadProgressReader) start() {
	progress.report(true)
}

func (progress *frpDownloadProgressReader) Read(buffer []byte) (int, error) {
	count, err := progress.reader.Read(buffer)
	progress.receivedBytes += int64(count)
	progress.report(false)
	return count, err
}

func (progress *frpDownloadProgressReader) finish() {
	if progress.reports == 1 || progress.lastReportedBytes != progress.receivedBytes {
		progress.report(true)
	}
}

func (progress *frpDownloadProgressReader) report(force bool) {
	now := progress.now()
	percent, hasPercent := progress.currentPercent()
	if !force {
		crossedPercent := hasPercent && percent-progress.lastPercent >= frpDownloadProgressPercentInterval
		crossedTime := !progress.lastReportedAt.IsZero() && now.Sub(progress.lastReportedAt) >= frpDownloadProgressTimeInterval
		if !crossedPercent && !crossedTime {
			return
		}
	}
	event := FRPRuntimeEvent{Type: FRPRuntimeEventDownloadProgress, ReceivedBytes: progress.receivedBytes}
	if progress.totalBytes != nil {
		totalBytes := *progress.totalBytes
		event.TotalBytes = &totalBytes
	}
	if hasPercent {
		value := percent
		event.Percent = &value
	}
	progress.observer.report(event)
	progress.lastReportedBytes = progress.receivedBytes
	progress.lastPercent = percent
	progress.lastReportedAt = now
	progress.reports++
}

func (progress *frpDownloadProgressReader) currentPercent() (int, bool) {
	if progress.totalBytes == nil || *progress.totalBytes <= 0 {
		return 0, false
	}
	percent := int(progress.receivedBytes * 100 / *progress.totalBytes)
	return min(percent, 100), true
}
