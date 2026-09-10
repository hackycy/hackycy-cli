package fs

import (
	"container/list"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/hackycy/hackycy-cli/internal/fsthumbnail"
)

const (
	thumbnailCacheEntries = 1000
	thumbnailCacheBytes   = 32 << 20
)

type ThumbnailResult struct {
	Bytes    []byte
	Identity FileIdentity
}

type ThumbnailService struct {
	workspace *Workspace
	converter fsthumbnail.Converter

	mu         sync.Mutex
	cache      map[string]*list.Element
	lru        *list.List
	cacheBytes int
	pending    map[string]*thumbnailPending
	closed     bool
}

type thumbnailCacheEntry struct {
	key      string
	bytes    []byte
	identity FileIdentity
}

type thumbnailPending struct {
	done   chan struct{}
	result ThumbnailResult
	err    error
}

func NewThumbnailService(workspace *Workspace) (*ThumbnailService, error) {
	pool, err := fsthumbnail.NewPool()
	if err != nil {
		return nil, err
	}
	return newThumbnailService(workspace, pool), nil
}

func newThumbnailService(workspace *Workspace, converter fsthumbnail.Converter) *ThumbnailService {
	return &ThumbnailService{
		workspace: workspace,
		converter: converter,
		cache:     make(map[string]*list.Element),
		lru:       list.New(),
		pending:   make(map[string]*thumbnailPending),
	}
}

func (service *ThumbnailService) Get(path WorkspacePath) (ThumbnailResult, error) {
	file, err := service.workspace.OpenFile(path)
	if err != nil {
		return ThumbnailResult{}, err
	}
	defer file.Close()
	identity := file.Identity()
	mimeType := workspaceMIMEType(identity.Name)
	if !thumbnailSupported(identity.Name) {
		return ThumbnailResult{}, &ServiceError{Code: "THUMBNAIL_UNSUPPORTED", Message: "Thumbnail format is not supported"}
	}
	if identity.Size > fsthumbnail.MaxSourceBytes {
		return ThumbnailResult{}, &ServiceError{Code: "THUMBNAIL_TOO_LARGE", Message: "Thumbnail source exceeds the 64 MiB limit"}
	}
	key := thumbnailCacheKey(identity)
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return ThumbnailResult{}, &ServiceError{Code: "THUMBNAIL_STOPPED", Message: "Thumbnail service is stopped"}
	}
	if cached, ok := service.cache[key]; ok {
		service.lru.MoveToBack(cached)
		entry := cached.Value.(thumbnailCacheEntry)
		service.mu.Unlock()
		return ThumbnailResult{Bytes: append([]byte(nil), entry.bytes...), Identity: entry.identity}, nil
	}
	if pending, ok := service.pending[key]; ok {
		service.mu.Unlock()
		<-pending.done
		return thumbnailPendingResult(pending)
	}
	pending := &thumbnailPending{done: make(chan struct{})}
	service.pending[key] = pending
	service.mu.Unlock()

	result, conversionErr := service.convertOpenedFile(file, identity, mimeType)
	service.mu.Lock()
	delete(service.pending, key)
	pending.result = result
	pending.err = conversionErr
	if conversionErr == nil && !service.closed {
		service.storeLocked(key, result)
	}
	close(pending.done)
	service.mu.Unlock()
	return thumbnailPendingResult(pending)
}

func (service *ThumbnailService) Close() {
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return
	}
	service.closed = true
	service.cache = make(map[string]*list.Element)
	service.lru.Init()
	service.cacheBytes = 0
	service.mu.Unlock()
	if pool, ok := service.converter.(fsthumbnail.Pool); ok {
		pool.Close()
	}
}

func (service *ThumbnailService) convertOpenedFile(file *OpenedFile, identity FileIdentity, mimeType string) (ThumbnailResult, error) {
	source, err := io.ReadAll(io.LimitReader(file, fsthumbnail.MaxSourceBytes+1))
	if err != nil {
		return ThumbnailResult{}, fmt.Errorf("%w: read thumbnail source: %v", ErrWorkspaceUnavailable, err)
	}
	if len(source) > fsthumbnail.MaxSourceBytes {
		return ThumbnailResult{}, &ServiceError{Code: "THUMBNAIL_TOO_LARGE", Message: "Thumbnail source exceeds the 64 MiB limit"}
	}
	if err := fsthumbnail.Validate(mimeType, source); err != nil {
		return ThumbnailResult{}, mapThumbnailRuntimeError(err)
	}
	thumbnail, err := service.converter.Convert(mimeType, source)
	if err != nil {
		return ThumbnailResult{}, mapThumbnailRuntimeError(err)
	}
	return ThumbnailResult{Bytes: append([]byte(nil), thumbnail...), Identity: identity}, nil
}

func (service *ThumbnailService) storeLocked(key string, result ThumbnailResult) {
	entry := thumbnailCacheEntry{key: key, bytes: append([]byte(nil), result.Bytes...), identity: result.Identity}
	service.cache[key] = service.lru.PushBack(entry)
	service.cacheBytes += len(entry.bytes)
	for service.lru.Len() > thumbnailCacheEntries || service.cacheBytes > thumbnailCacheBytes {
		oldest := service.lru.Front()
		if oldest == nil {
			break
		}
		removed := oldest.Value.(thumbnailCacheEntry)
		delete(service.cache, removed.key)
		service.lru.Remove(oldest)
		service.cacheBytes -= len(removed.bytes)
	}
}

func thumbnailPendingResult(pending *thumbnailPending) (ThumbnailResult, error) {
	if pending.err != nil {
		return ThumbnailResult{}, pending.err
	}
	return ThumbnailResult{Bytes: append([]byte(nil), pending.result.Bytes...), Identity: pending.result.Identity}, nil
}

func mapThumbnailRuntimeError(err error) error {
	var thumbnail *fsthumbnail.Error
	if errors.As(err, &thumbnail) {
		return &ServiceError{Code: thumbnail.Code, Message: thumbnail.Message, Cause: err}
	}
	return err
}

func thumbnailCacheKey(identity FileIdentity) string {
	return identity.Path.String() + "\x00" + fmt.Sprintf("%d\x00%d", identity.Size, identity.ModifiedAt.UnixMilli())
}
