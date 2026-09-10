package fs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sync"
	"time"
)

const chunkedUploadThreshold int64 = 20 * 1024 * 1024

type ChunkedUploadManager struct {
	workspace *Workspace
	chunkSize int64
	mu        sync.Mutex
	uploads   map[string]*chunkedUpload
	closed    bool
	now       func() time.Time
	observer  chunkedUploadLifecycleObserver
}

type chunkedUpload struct {
	id                string
	owner             string
	directory         WorkspacePath
	filename          string
	temporary         WorkspacePath
	size              int64
	uploaded          int64
	complete          *UploadResult
	started           time.Time
	updated           time.Time
	lifecycleTerminal bool
}

// chunkedUploadLifecycleObserver receives only real Managed Task transitions.
// It is private so the existing upload HTTP representation stays unchanged.
type chunkedUploadLifecycleObserver interface {
	chunkedUploadStarted(chunkedUploadLifecycleTask)
	chunkedUploadCompleted(chunkedUploadLifecycleTask)
	chunkedUploadCancelled(chunkedUploadLifecycleTask)
	chunkedUploadExpired(chunkedUploadLifecycleTask)
}

type ChunkedUpload struct {
	ID             string        `json:"id"`
	Status         string        `json:"status"`
	Size           int64         `json:"size"`
	UploadedBytes  int64         `json:"uploadedBytes"`
	ChunkSizeBytes int64         `json:"chunkSizeBytes"`
	Result         *UploadResult `json:"result,omitempty"`
}

func NewChunkedUploadManager(workspace *Workspace, chunkSize int64) *ChunkedUploadManager {
	if chunkSize == 0 {
		chunkSize = 8 * 1024 * 1024
	}
	return newChunkedUploadManager(workspace, chunkSize, time.Now, nil)
}

func newChunkedUploadManager(workspace *Workspace, chunkSize int64, now func() time.Time, observer chunkedUploadLifecycleObserver) *ChunkedUploadManager {
	if chunkSize == 0 {
		chunkSize = 8 * 1024 * 1024
	}
	if now == nil {
		now = time.Now
	}
	return &ChunkedUploadManager{workspace: workspace, chunkSize: chunkSize, uploads: make(map[string]*chunkedUpload), now: now, observer: observer}
}

// setLifecycle installs the service observer before the listener is exposed.
func (manager *ChunkedUploadManager) setLifecycle(observer chunkedUploadLifecycleObserver, now func() time.Time) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.observer = observer
	if now != nil {
		manager.now = now
	}
}

func (manager *ChunkedUploadManager) Create(owner string, directory WorkspacePath, filename string, size int64) (ChunkedUpload, error) {
	filename, err := uploadFilename(filename)
	if err != nil {
		return ChunkedUpload{}, err
	}
	if size <= chunkedUploadThreshold || size > int64(^uint(0)>>1) {
		return ChunkedUpload{}, &ServiceError{Code: "INVALID_UPLOAD", Message: "Chunked upload size must be larger than 20 MiB"}
	}
	manager.mu.Lock()
	closed := manager.closed
	manager.mu.Unlock()
	if closed {
		return ChunkedUpload{}, &ServiceError{Code: "CHUNKED_UPLOAD_STOPPED", Message: "Chunked upload service is stopped"}
	}
	manager.workspace.writes.Lock()
	defer manager.workspace.writes.Unlock()
	if err := manager.workspace.requireDirectory(directory); err != nil {
		return ChunkedUpload{}, err
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return ChunkedUpload{}, &ServiceError{Code: "CHUNKED_UPLOAD_STOPPED", Message: "Chunked upload service is stopped"}
	}
	manager.pruneLocked(manager.now())
	count := 0
	for _, upload := range manager.uploads {
		if upload.owner == owner && upload.complete == nil {
			count++
		}
	}
	if count >= 3 || len(manager.uploads) >= 100 {
		return ChunkedUpload{}, &ServiceError{Code: "CHUNKED_UPLOAD_LIMIT_REACHED", Message: "Too many chunked uploads are active"}
	}
	id, err := newChunkedUploadID()
	if err != nil {
		return ChunkedUpload{}, err
	}
	temporary := directory.child(".upload-" + id + ".tmp")
	file, err := manager.workspace.root.OpenFile(temporary.rootName(), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return ChunkedUpload{}, workspaceUnavailable("create chunked upload staging file", err)
	}
	if err := file.Close(); err != nil {
		return ChunkedUpload{}, workspaceUnavailable("close chunked upload staging file", err)
	}
	started := manager.now()
	upload := &chunkedUpload{id: id, owner: owner, directory: directory, filename: filename, temporary: temporary, size: size, started: started, updated: started}
	manager.uploads[id] = upload
	manager.emitChunkedUploadStartedLocked(upload)
	return manager.describe(upload), nil
}

// Close removes process-local incomplete staging entries and refuses future
// upload creation. Completed publications remain in the workspace.
func (manager *ChunkedUploadManager) Close() error {
	_, err := manager.closeWithStats()
	return err
}

func (manager *ChunkedUploadManager) closeWithStats() (removed int, result error) {
	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return 0, nil
	}
	manager.closed = true
	manager.observer = nil
	temporary := make([]WorkspacePath, 0, len(manager.uploads))
	for _, upload := range manager.uploads {
		if upload.complete == nil {
			removed++
			temporary = append(temporary, upload.temporary)
		}
	}
	manager.uploads = make(map[string]*chunkedUpload)
	manager.mu.Unlock()

	for _, path := range temporary {
		if err := manager.workspace.root.Remove(path.rootName()); err != nil && !os.IsNotExist(err) {
			result = errors.Join(result, workspaceUnavailable("remove chunked upload staging file", err))
		}
	}
	return removed, result
}

func (manager *ChunkedUploadManager) lifecycleSnapshot() fsShutdownSnapshot {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	snapshot := fsShutdownSnapshot{}
	for _, upload := range manager.uploads {
		if upload.complete == nil {
			snapshot.IncompleteChunkedUploads++
		}
	}
	return snapshot
}

func (manager *ChunkedUploadManager) Get(owner, id string) (ChunkedUpload, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pruneLocked(manager.now())
	upload, err := manager.ownedLocked(owner, id)
	if err != nil {
		return ChunkedUpload{}, err
	}
	upload.updated = manager.now()
	return manager.describe(upload), nil
}

func (manager *ChunkedUploadManager) Append(owner, id string, start, end, total int64, body io.Reader) (ChunkedUpload, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pruneLocked(manager.now())
	upload, err := manager.ownedLocked(owner, id)
	if err != nil {
		return ChunkedUpload{}, err
	}
	if upload.complete != nil {
		return ChunkedUpload{}, &ServiceError{Code: "CHUNKED_UPLOAD_OFFSET_MISMATCH", Message: "Chunked upload is already complete"}
	}
	if total != upload.size || start != upload.uploaded || end < start || end-start+1 > manager.chunkSize {
		return ChunkedUpload{}, &ServiceError{Code: "CHUNKED_UPLOAD_OFFSET_MISMATCH", Message: "Chunk range does not match the confirmed upload offset"}
	}
	file, err := manager.workspace.root.OpenFile(upload.temporary.rootName(), os.O_WRONLY, 0)
	if err != nil {
		return ChunkedUpload{}, workspaceUnavailable("open chunked upload staging file", err)
	}
	defer file.Close()
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return ChunkedUpload{}, workspaceUnavailable("seek chunked upload staging file", err)
	}
	written, err := io.Copy(file, io.LimitReader(body, manager.chunkSize+1))
	if err != nil {
		return ChunkedUpload{}, workspaceUnavailable("write upload chunk", err)
	}
	if written != end-start+1 || written > manager.chunkSize {
		return ChunkedUpload{}, &ServiceError{Code: "CHUNKED_UPLOAD_OFFSET_MISMATCH", Message: "Chunk body length does not match Content-Range"}
	}
	upload.uploaded += written
	upload.updated = manager.now()
	return manager.describe(upload), nil
}

func (manager *ChunkedUploadManager) Complete(owner, id string) (ChunkedUpload, error) {
	manager.workspace.writes.Lock()
	defer manager.workspace.writes.Unlock()
	manager.mu.Lock()
	defer manager.mu.Unlock()
	manager.pruneLocked(manager.now())
	upload, err := manager.ownedLocked(owner, id)
	if err != nil {
		return ChunkedUpload{}, err
	}
	if upload.complete != nil {
		return manager.describe(upload), nil
	}
	if upload.uploaded != upload.size {
		return ChunkedUpload{}, &ServiceError{Code: "CHUNKED_UPLOAD_INCOMPLETE", Message: "Chunked upload has not received every byte"}
	}
	for index := 0; index <= 9999; index++ {
		name := upload.filename
		if index > 0 {
			name = copyFilename(upload.filename, index)
		}
		destination := upload.directory.child(name)
		if err := manager.workspace.root.Link(upload.temporary.rootName(), destination.rootName()); err != nil {
			if os.IsExist(err) {
				continue
			}
			return ChunkedUpload{}, workspaceUnavailable("publish chunked upload", err)
		}
		if err := manager.workspace.root.Remove(upload.temporary.rootName()); err != nil {
			return ChunkedUpload{}, workspaceUnavailable("remove chunked upload staging file", err)
		}
		result := UploadResult{Filename: name, Path: destination.String(), Size: upload.size}
		upload.complete = &result
		upload.updated = manager.now()
		manager.emitChunkedUploadCompletedLocked(upload)
		return manager.describe(upload), nil
	}
	return ChunkedUpload{}, &ServiceError{Code: "NAME_EXHAUSTED", Message: "Too many files have the same name"}
}

func (manager *ChunkedUploadManager) Cancel(owner, id string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	upload, err := manager.ownedLocked(owner, id)
	if err != nil {
		return err
	}
	if upload.complete != nil {
		return nil
	}
	if err := manager.workspace.root.Remove(upload.temporary.rootName()); err != nil && !os.IsNotExist(err) {
		return workspaceUnavailable("remove chunked upload staging file", err)
	}
	manager.emitChunkedUploadCancelledLocked(upload)
	delete(manager.uploads, id)
	return nil
}

func (manager *ChunkedUploadManager) describe(upload *chunkedUpload) ChunkedUpload {
	status := "uploading"
	if upload.complete != nil {
		status = "complete"
	}
	return ChunkedUpload{ID: upload.id, Status: status, Size: upload.size, UploadedBytes: upload.uploaded, ChunkSizeBytes: manager.chunkSize, Result: upload.complete}
}

func (manager *ChunkedUploadManager) ownedLocked(owner, id string) (*chunkedUpload, error) {
	upload, found := manager.uploads[id]
	if !found || upload.owner != owner {
		return nil, &ServiceError{Code: "CHUNKED_UPLOAD_NOT_FOUND", Message: "Chunked upload was not found"}
	}
	return upload, nil
}

func (manager *ChunkedUploadManager) pruneLocked(now time.Time) {
	for id, upload := range manager.uploads {
		limit := 30 * time.Minute
		if upload.complete != nil {
			limit = 5 * time.Minute
		}
		if now.Sub(upload.updated) <= limit {
			continue
		}
		if upload.complete == nil {
			manager.emitChunkedUploadExpiredLocked(upload)
		}
		if upload.complete == nil {
			_ = manager.workspace.root.Remove(upload.temporary.rootName())
		}
		delete(manager.uploads, id)
	}
}

func (manager *ChunkedUploadManager) chunkedUploadLifecycleSnapshotLocked(upload *chunkedUpload) chunkedUploadLifecycleTask {
	if upload == nil {
		return chunkedUploadLifecycleTask{}
	}
	return chunkedUploadLifecycleTask{
		ID:            upload.id,
		DirectoryPath: upload.directory.String(),
		Filename:      upload.filename,
		DestinationPath: func() string {
			if upload.complete == nil {
				return ""
			}
			return upload.complete.Path
		}(),
		TotalBytes:     upload.size,
		UploadedBytes:  upload.uploaded,
		ChunkSizeBytes: manager.chunkSize,
		StartedAt:      upload.started,
	}
}

func (manager *ChunkedUploadManager) emitChunkedUploadStartedLocked(upload *chunkedUpload) {
	if manager.observer != nil {
		manager.observer.chunkedUploadStarted(manager.chunkedUploadLifecycleSnapshotLocked(upload))
	}
}

func (manager *ChunkedUploadManager) emitChunkedUploadCompletedLocked(upload *chunkedUpload) {
	if upload == nil || upload.lifecycleTerminal {
		return
	}
	upload.lifecycleTerminal = true
	if manager.observer != nil {
		manager.observer.chunkedUploadCompleted(manager.chunkedUploadLifecycleSnapshotLocked(upload))
	}
}

func (manager *ChunkedUploadManager) emitChunkedUploadCancelledLocked(upload *chunkedUpload) {
	if upload == nil || upload.lifecycleTerminal {
		return
	}
	upload.lifecycleTerminal = true
	if manager.observer != nil {
		manager.observer.chunkedUploadCancelled(manager.chunkedUploadLifecycleSnapshotLocked(upload))
	}
}

func (manager *ChunkedUploadManager) emitChunkedUploadExpiredLocked(upload *chunkedUpload) {
	if upload == nil || upload.lifecycleTerminal {
		return
	}
	upload.lifecycleTerminal = true
	if manager.observer != nil {
		manager.observer.chunkedUploadExpired(manager.chunkedUploadLifecycleSnapshotLocked(upload))
	}
}

func newChunkedUploadID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", workspaceUnavailable("generate chunked upload ID", err)
	}
	hexValue := hex.EncodeToString(bytes)
	return hexValue[:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:], nil
}
