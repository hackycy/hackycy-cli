package fs

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/hackycy/hackycy-cli/internal/logging"
)

const fsLifecycleFieldLimit = 1024

// fsLifecycle owns the startup ordering boundary for the foreground service.
// Later service event clusters can use the same gate to defer observations
// until the durable stdout startup checkpoint has committed.
type fsLifecycle struct {
	logger logging.Logger
	now    func() time.Time

	mu              sync.Mutex
	startupBegun    bool
	stdoutCommitted bool
	aborted         bool
	preStartup      []func()
	pending         []func()
	stoppingStarted bool
	terminal        bool
	shutdownReason  string
}

func newFSLifecycle(logger logging.Logger, now func() time.Time) *fsLifecycle {
	if now == nil {
		now = time.Now
	}
	return &fsLifecycle{logger: logger, now: now}
}

// begin emits the fixed startup catalog after the listener and all runtime
// services have been created, but before the stdout startup checkpoint.
func (lifecycle *fsLifecycle) begin(startup Startup) {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.startupBegun || lifecycle.aborted {
		return
	}
	lifecycle.startupBegun = true

	localURL, networkURLs := splitFSStartupURLs(startup.URLs)
	lifecycle.logger.Info("File Browser started", map[string]any{
		"localURL":       sanitizeFSField(localURL),
		"networkURLs":    sanitizeFSFields(networkURLs),
		"bindingAddress": sanitizeFSField(startup.BindingAddress),
		"port":           startup.Port,
	})
	lifecycle.logger.Info("Browse root configured", map[string]any{
		"directory": sanitizeFSField(startup.Directory),
	})
	capabilities := map[string]any{
		"managementEnabled":     startup.ManagementEnabled,
		"chunkedUploadsEnabled": startup.ChunkedUploads,
		"htmlExecutionEnabled":  !startup.SafeHTML,
	}
	if startup.ChunkedUploads {
		capabilities["uploadChunkSizeBytes"] = startup.UploadChunkSize
	}
	lifecycle.logger.Info("File Browser capabilities configured", capabilities)

	authentication := map[string]any{
		"authenticationEnabled": startup.Authentication,
	}
	if startup.Authentication {
		authentication["accountCount"] = startup.AccountCount
		authentication["sessionDirectory"] = sanitizeFSField(startup.SessionDirectory)
		authentication["sessionIdleDurationMs"] = sessionIdleDurationMilliseconds(startup.SessionIdleDuration)
	}
	lifecycle.logger.Info("File Browser authentication configured", authentication)

	if !startup.Authentication && !isFSLoopbackAddress(startup.BindingAddress) {
		lifecycle.logger.Warn("File Browser is accessible without authentication", map[string]any{
			"bindingAddress":    sanitizeFSField(startup.BindingAddress),
			"managementEnabled": startup.ManagementEnabled,
		})
	}
	// A listener can accept a task before the startup checkpoint is written.
	// Keep those observations behind the same ordering boundary as startup.
	lifecycle.pending = append(lifecycle.pending, lifecycle.preStartup...)
	lifecycle.preStartup = nil
}

// commitStartup releases the startup gate after the durable stdout document
// has been accepted and flushes task observations captured while the listener
// was becoming ready.
func (lifecycle *fsLifecycle) commitStartup() {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.aborted || lifecycle.stoppingStarted || lifecycle.terminal || !lifecycle.startupBegun {
		return
	}
	lifecycle.stdoutCommitted = true
	for _, event := range lifecycle.pending {
		if event != nil {
			event()
		}
	}
	lifecycle.pending = nil
}

// abort closes the startup gate when the service cannot publish its startup
// result. No startup observer may emit work after this transition.
func (lifecycle *fsLifecycle) abort() {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	lifecycle.aborted = true
	lifecycle.preStartup = nil
	lifecycle.pending = nil
	lifecycle.mu.Unlock()
}

func (lifecycle *fsLifecycle) startupCommitted() bool {
	if lifecycle == nil {
		return false
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	return lifecycle.stdoutCommitted && !lifecycle.aborted
}

// schedule queues one service observation until the durable startup result is
// committed. The callback is invoked while the lifecycle lock is held so
// concurrent manager transitions retain their service-local order.
func (lifecycle *fsLifecycle) schedule(event func()) {
	if lifecycle == nil || event == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.aborted || lifecycle.stoppingStarted || lifecycle.terminal {
		return
	}
	if !lifecycle.startupBegun {
		lifecycle.preStartup = append(lifecycle.preStartup, event)
		return
	}
	if !lifecycle.stdoutCommitted {
		lifecycle.pending = append(lifecycle.pending, event)
		return
	}
	event()
}

// shutdownStarted folds outstanding work into one service-level stopping
// record before any manager begins its cleanup. Per-task events captured before
// this boundary are discarded rather than emitted after stopping.
func (lifecycle *fsLifecycle) shutdownStarted(reason string, snapshot fsShutdownSnapshot) {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.aborted || lifecycle.stoppingStarted || lifecycle.terminal || !lifecycle.startupBegun {
		return
	}
	lifecycle.stoppingStarted = true
	lifecycle.shutdownReason = reason
	lifecycle.preStartup = nil
	lifecycle.pending = nil
	lifecycle.logger.Info("File Browser stopping", map[string]any{
		"reason":                   sanitizeFSField(reason),
		"queuedDownloads":          snapshot.QueuedDownloads,
		"activeDownloads":          snapshot.ActiveDownloads,
		"queuedExtractions":        snapshot.QueuedExtractions,
		"activeExtractions":        snapshot.ActiveExtractions,
		"incompleteChunkedUploads": snapshot.IncompleteChunkedUploads,
	})
}

// shutdownFinished emits the one terminal service record after all managers
// and the workspace have been released. A failure replaces the clean stopped
// record and is projected as one bounded error.
func (lifecycle *fsLifecycle) shutdownFinished(summary fsShutdownSummary, stage string, err error) {
	if lifecycle == nil {
		return
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.aborted || lifecycle.terminal || !lifecycle.startupBegun {
		return
	}
	lifecycle.terminal = true
	if err != nil {
		lifecycle.logger.Error("File Browser failed", map[string]any{
			"stage": sanitizeFSField(stage),
			"error": safeFSShutdownError(err),
		})
		return
	}
	fields := map[string]any{
		"cancelledDownloads":    summary.CancelledDownloads,
		"cancelledExtractions":  summary.CancelledExtractions,
		"removedChunkedUploads": summary.RemovedChunkedUploads,
	}
	if lifecycle.shutdownReason == "server-stopped" || lifecycle.shutdownReason == "startup-output-failed" {
		fields["reason"] = lifecycle.shutdownReason
	}
	lifecycle.logger.Info("File Browser stopped", fields)
}

func safeFSShutdownError(err error) string {
	if err == nil {
		return "File Browser shutdown failed"
	}
	message := sanitizeFSField(err.Error())
	if message == "" {
		return "File Browser shutdown failed"
	}
	return message
}

type downloadLifecycleTask struct {
	ID              string
	URL             string
	DirectoryPath   string
	Filename        string
	DestinationPath string
	BytesDownloaded int64
	TotalBytes      *int64
	StartedAt       time.Time
	RetryOf         string
}

type extractionLifecycleTask struct {
	ID                string
	ArchivePath       string
	DestinationPath   string
	Progress          *int
	UncompressedBytes *int64
	EntryCount        *int64
	StartedAt         time.Time
	RetryOf           string
}

type chunkedUploadLifecycleTask struct {
	ID              string
	DirectoryPath   string
	Filename        string
	DestinationPath string
	TotalBytes      int64
	UploadedBytes   int64
	ChunkSizeBytes  int64
	StartedAt       time.Time
}

func (lifecycle *fsLifecycle) downloadAccepted(task downloadLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := cloneDownloadLifecycleTask(task)
	lifecycle.schedule(func() {
		lifecycle.logger.Info("Download task accepted", downloadLifecycleFields(snapshot))
	})
}

func (lifecycle *fsLifecycle) downloadStarted(task downloadLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := cloneDownloadLifecycleTask(task)
	lifecycle.schedule(func() {
		lifecycle.logger.Info("Download task started", downloadLifecycleFields(snapshot))
	})
}

func (lifecycle *fsLifecycle) downloadCompleted(task downloadLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := cloneDownloadLifecycleTask(task)
	lifecycle.schedule(func() {
		fields := downloadLifecycleFields(snapshot)
		fields["bytesDownloaded"] = nonNegativeDownloadBytes(snapshot.BytesDownloaded)
		if snapshot.TotalBytes != nil && *snapshot.TotalBytes >= 0 {
			fields["totalBytes"] = *snapshot.TotalBytes
		}
		fields["durationMs"] = lifecycle.downloadDurationMilliseconds(snapshot.StartedAt)
		lifecycle.logger.Info("Download task completed", fields)
	})
}

func (lifecycle *fsLifecycle) downloadCancelled(task downloadLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := cloneDownloadLifecycleTask(task)
	lifecycle.schedule(func() {
		fields := downloadLifecycleFields(snapshot)
		fields["cancelSource"] = "client"
		fields["bytesDownloaded"] = nonNegativeDownloadBytes(snapshot.BytesDownloaded)
		if snapshot.TotalBytes != nil && *snapshot.TotalBytes >= 0 {
			fields["totalBytes"] = *snapshot.TotalBytes
		}
		lifecycle.logger.Info("Download task cancelled", fields)
	})
}

func (lifecycle *fsLifecycle) downloadFailed(task downloadLifecycleTask, err error) {
	if lifecycle == nil {
		return
	}
	snapshot := cloneDownloadLifecycleTask(task)
	code, message := safeDownloadFailure(err)
	lifecycle.schedule(func() {
		fields := downloadLifecycleFields(snapshot)
		fields["code"] = code
		fields["error"] = message
		lifecycle.logger.Warn("Download task failed", fields)
	})
}

func cloneDownloadLifecycleTask(task downloadLifecycleTask) downloadLifecycleTask {
	if task.TotalBytes != nil {
		value := *task.TotalBytes
		task.TotalBytes = &value
	}
	return task
}

func downloadLifecycleFields(task downloadLifecycleTask) map[string]any {
	fields := map[string]any{
		"taskType": "download",
		"taskID":   sanitizeFSField(task.ID),
	}
	if task.RetryOf != "" {
		fields["retryOf"] = sanitizeFSField(task.RetryOf)
	}
	if scheme, host, ok := safeDownloadSource(task.URL); ok {
		fields["sourceScheme"] = scheme
		fields["sourceHost"] = host
	}
	destinationPath := task.DestinationPath
	if destinationPath == "" {
		destinationPath = task.DirectoryPath
	}
	if destination, ok := safeFSWorkspacePath(destinationPath); ok {
		fields["destinationPath"] = destination
	}
	if filename := sanitizeFSField(task.Filename); filename != "" {
		fields["filename"] = filename
	}
	return fields
}

func safeDownloadSource(raw string) (string, string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil {
		return "", "", false
	}
	scheme := sanitizeFSField(strings.ToLower(parsed.Scheme))
	hostname := sanitizeFSField(strings.ToLower(parsed.Hostname()))
	if scheme == "" || hostname == "" {
		return "", "", false
	}
	host := hostname
	if port := parsed.Port(); port != "" {
		if parsedHost, _, portErr := net.SplitHostPort(parsed.Host); portErr == nil {
			host = net.JoinHostPort(sanitizeFSField(parsedHost), sanitizeFSField(port))
		} else {
			host = hostname + ":" + sanitizeFSField(port)
		}
	}
	return scheme, sanitizeFSField(host), true
}

func safeFSWorkspacePath(value string) (string, bool) {
	value = sanitizeFSField(value)
	parsed, err := ParseWorkspacePath(value)
	if err != nil {
		return "", false
	}
	return sanitizeFSField(parsed.String()), true
}

func nonNegativeDownloadBytes(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func (lifecycle *fsLifecycle) downloadDurationMilliseconds(start time.Time) int64 {
	if start.IsZero() {
		return 0
	}
	duration := lifecycle.now().Sub(start)
	if duration < 0 {
		return 0
	}
	return duration.Milliseconds()
}

func safeDownloadFailure(err error) (string, string) {
	code := "DOWNLOAD_UNAVAILABLE"
	message := "Download could not be completed"
	var service *ServiceError
	if errors.As(err, &service) && service != nil {
		if service.Code != "" {
			code = sanitizeFSField(service.Code)
		}
		if service.Message != "" {
			message = sanitizeFSField(service.Message)
		}
	}
	if code == "DOWNLOAD_UNAVAILABLE" {
		message = "Download could not be completed"
	}
	return sanitizeFSField(code), sanitizeFSField(message)
}

func (lifecycle *fsLifecycle) extractionAccepted(task extractionLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := cloneExtractionLifecycleTask(task)
	lifecycle.schedule(func() {
		lifecycle.logger.Info("Extraction task accepted", extractionLifecycleFields(snapshot))
	})
}

func (lifecycle *fsLifecycle) extractionStarted(task extractionLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := cloneExtractionLifecycleTask(task)
	lifecycle.schedule(func() {
		lifecycle.logger.Info("Extraction task started", extractionLifecycleFields(snapshot))
	})
}

func (lifecycle *fsLifecycle) extractionCompleted(task extractionLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := cloneExtractionLifecycleTask(task)
	lifecycle.schedule(func() {
		fields := extractionLifecycleFields(snapshot)
		if snapshot.UncompressedBytes != nil && *snapshot.UncompressedBytes >= 0 {
			fields["uncompressedBytes"] = *snapshot.UncompressedBytes
		}
		if snapshot.EntryCount != nil && *snapshot.EntryCount >= 0 {
			fields["entryCount"] = *snapshot.EntryCount
		}
		fields["durationMs"] = lifecycle.downloadDurationMilliseconds(snapshot.StartedAt)
		lifecycle.logger.Info("Extraction task completed", fields)
	})
}

func (lifecycle *fsLifecycle) extractionCancelled(task extractionLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := cloneExtractionLifecycleTask(task)
	lifecycle.schedule(func() {
		fields := extractionLifecycleFields(snapshot)
		fields["cancelSource"] = "client"
		if snapshot.UncompressedBytes != nil && *snapshot.UncompressedBytes >= 0 {
			fields["uncompressedBytes"] = *snapshot.UncompressedBytes
		}
		if snapshot.EntryCount != nil && *snapshot.EntryCount >= 0 {
			fields["entryCount"] = *snapshot.EntryCount
		}
		lifecycle.logger.Info("Extraction task cancelled", fields)
	})
}

func (lifecycle *fsLifecycle) extractionFailed(task extractionLifecycleTask, err error) {
	if lifecycle == nil {
		return
	}
	snapshot := cloneExtractionLifecycleTask(task)
	code, message := safeExtractionFailure(err)
	lifecycle.schedule(func() {
		fields := extractionLifecycleFields(snapshot)
		fields["code"] = code
		fields["error"] = message
		lifecycle.logger.Warn("Extraction task failed", fields)
	})
}

func cloneExtractionLifecycleTask(task extractionLifecycleTask) extractionLifecycleTask {
	if task.Progress != nil {
		value := *task.Progress
		task.Progress = &value
	}
	if task.UncompressedBytes != nil {
		value := *task.UncompressedBytes
		task.UncompressedBytes = &value
	}
	if task.EntryCount != nil {
		value := *task.EntryCount
		task.EntryCount = &value
	}
	return task
}

func extractionLifecycleFields(task extractionLifecycleTask) map[string]any {
	fields := map[string]any{
		"taskType": "extraction",
		"taskID":   sanitizeFSField(task.ID),
	}
	if task.RetryOf != "" {
		fields["retryOf"] = sanitizeFSField(task.RetryOf)
	}
	if archivePath, ok := safeFSWorkspacePath(task.ArchivePath); ok {
		fields["archivePath"] = archivePath
	}
	if task.DestinationPath != "" {
		if destinationPath, ok := safeFSWorkspacePath(task.DestinationPath); ok {
			fields["destinationPath"] = destinationPath
		}
	}
	return fields
}

func safeExtractionFailure(err error) (string, string) {
	code := "UNAVAILABLE"
	message := "Extraction could not be completed"
	var service *ServiceError
	if errors.As(err, &service) && service != nil {
		if service.Code != "" {
			code = sanitizeFSField(service.Code)
		}
		if service.Message != "" {
			message = sanitizeFSField(service.Message)
		}
	}
	if code == "UNAVAILABLE" {
		message = "Extraction could not be completed"
	}
	return sanitizeFSField(code), sanitizeFSField(message)
}

func (lifecycle *fsLifecycle) chunkedUploadStarted(task chunkedUploadLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := task
	lifecycle.schedule(func() {
		fields := chunkedUploadLifecycleFields(snapshot)
		fields["filename"] = sanitizeFSField(snapshot.Filename)
		if destination, ok := safeFSWorkspacePath(snapshot.DirectoryPath); ok {
			fields["destinationPath"] = destination
		}
		fields["totalBytes"] = nonNegativeChunkedUploadBytes(snapshot.TotalBytes)
		fields["chunkSizeBytes"] = nonNegativeChunkedUploadBytes(snapshot.ChunkSizeBytes)
		lifecycle.logger.Info("Chunked upload started", fields)
	})
}

func (lifecycle *fsLifecycle) chunkedUploadCompleted(task chunkedUploadLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := task
	lifecycle.schedule(func() {
		fields := chunkedUploadLifecycleFields(snapshot)
		if destination, ok := safeFSWorkspacePath(snapshot.DestinationPath); ok {
			fields["destinationPath"] = destination
		}
		fields["totalBytes"] = nonNegativeChunkedUploadBytes(snapshot.TotalBytes)
		fields["durationMs"] = lifecycle.downloadDurationMilliseconds(snapshot.StartedAt)
		lifecycle.logger.Info("Chunked upload completed", fields)
	})
}

func (lifecycle *fsLifecycle) chunkedUploadCancelled(task chunkedUploadLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := task
	lifecycle.schedule(func() {
		fields := chunkedUploadLifecycleFields(snapshot)
		fields["filename"] = sanitizeFSField(snapshot.Filename)
		if destination, ok := safeFSWorkspacePath(snapshot.DirectoryPath); ok {
			fields["destinationPath"] = destination
		}
		fields["cancelSource"] = "client"
		fields["totalBytes"] = nonNegativeChunkedUploadBytes(snapshot.TotalBytes)
		fields["uploadedBytes"] = nonNegativeChunkedUploadBytes(snapshot.UploadedBytes)
		lifecycle.logger.Info("Chunked upload cancelled", fields)
	})
}

func (lifecycle *fsLifecycle) chunkedUploadExpired(task chunkedUploadLifecycleTask) {
	if lifecycle == nil {
		return
	}
	snapshot := task
	lifecycle.schedule(func() {
		fields := chunkedUploadLifecycleFields(snapshot)
		fields["filename"] = sanitizeFSField(snapshot.Filename)
		if destination, ok := safeFSWorkspacePath(snapshot.DirectoryPath); ok {
			fields["destinationPath"] = destination
		}
		fields["totalBytes"] = nonNegativeChunkedUploadBytes(snapshot.TotalBytes)
		fields["uploadedBytes"] = nonNegativeChunkedUploadBytes(snapshot.UploadedBytes)
		lifecycle.logger.Info("Chunked upload expired", fields)
	})
}

func chunkedUploadLifecycleFields(task chunkedUploadLifecycleTask) map[string]any {
	return map[string]any{
		"taskType": "chunkedUpload",
		"taskID":   sanitizeFSField(task.ID),
	}
}

func nonNegativeChunkedUploadBytes(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

func splitFSStartupURLs(urls []StartupURL) (string, []string) {
	var local string
	network := make([]string, 0, len(urls))
	for _, endpoint := range urls {
		if endpoint.Label == "Network" {
			network = append(network, endpoint.URL)
			continue
		}
		if local == "" && endpoint.Label == "Local" {
			local = endpoint.URL
		}
	}
	return local, network
}

func sanitizeFSFields(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, sanitizeFSField(value))
	}
	if result == nil {
		return []string{}
	}
	return result
}

func sanitizeFSField(value string) string {
	value = logging.RedactDiagnostic(value)
	runes := []rune(value)
	if len(runes) <= fsLifecycleFieldLimit {
		return value
	}
	return string(runes[:fsLifecycleFieldLimit])
}

func sessionIdleDurationMilliseconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}

func isFSLoopbackAddress(value string) bool {
	value = strings.TrimSpace(value)
	if strings.EqualFold(value, "localhost") {
		return true
	}
	value = strings.TrimPrefix(value, "[")
	value = strings.TrimSuffix(value, "]")
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	if address, err := netip.ParseAddr(value); err == nil {
		return address.IsLoopback()
	}
	return false
}
