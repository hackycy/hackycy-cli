package diff

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
)

type httpEventQueue struct {
	mu      sync.Mutex
	payload []httpStatePayload
	wake    chan struct{}
	closed  bool
}

func newHTTPEventQueue() *httpEventQueue {
	return &httpEventQueue{wake: make(chan struct{}, 1)}
}

func (handler *diffHTTPHandler) serveEvents(writer http.ResponseWriter, request *http.Request) {
	if !requireHTTPMethod(writer, request, http.MethodGet) {
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", diffAPICSP)
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)

	queue := newHTTPEventQueue()
	unsubscribe := handler.workspace.Subscribe(func(state WorkspaceState) {
		queue.enqueue(handler.makeHTTPStatePayload(state))
	})
	defer unsubscribe()
	defer queue.close()

	for {
		payload, ok := queue.next(request.Context().Done())
		if !ok {
			return
		}
		if err := writeHTTPEventState(writer, payload); err != nil {
			return
		}
		if err := http.NewResponseController(writer).Flush(); err != nil {
			return
		}
	}
}

func (queue *httpEventQueue) enqueue(payload httpStatePayload) {
	queue.mu.Lock()
	if queue.closed {
		queue.mu.Unlock()
		return
	}
	queue.payload = append(queue.payload, payload)
	queue.mu.Unlock()
	select {
	case queue.wake <- struct{}{}:
	default:
	}
}

func (queue *httpEventQueue) next(contextDone <-chan struct{}) (httpStatePayload, bool) {
	for {
		queue.mu.Lock()
		if len(queue.payload) > 0 {
			payload := queue.payload[0]
			queue.payload[0] = httpStatePayload{}
			queue.payload = queue.payload[1:]
			queue.mu.Unlock()
			return payload, true
		}
		if queue.closed {
			queue.mu.Unlock()
			return httpStatePayload{}, false
		}
		queue.mu.Unlock()

		select {
		case <-queue.wake:
		case <-contextDone:
			return httpStatePayload{}, false
		}
	}
}

func (queue *httpEventQueue) close() {
	queue.mu.Lock()
	queue.closed = true
	queue.payload = nil
	queue.mu.Unlock()
	select {
	case queue.wake <- struct{}{}:
	default:
	}
}

func writeHTTPEventState(writer io.Writer, payload httpStatePayload) error {
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return err
	}
	encoded := bytes.TrimSuffix(body.Bytes(), []byte{'\n'})
	if _, err := io.WriteString(writer, "data: "); err != nil {
		return err
	}
	if _, err := writer.Write(encoded); err != nil {
		return err
	}
	_, err := io.WriteString(writer, "\n\n")
	return err
}
