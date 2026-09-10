package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

type serverHTTPEvent struct {
	Version int    `json:"version"`
	Event   string `json:"event"`
}

type serverHTTPEventQueue struct {
	mu       sync.Mutex
	events   []serverHTTPEvent
	closed   bool
	terminal bool
	wake     chan struct{}
}

func newServerHTTPEventQueue() *serverHTTPEventQueue {
	return &serverHTTPEventQueue{wake: make(chan struct{}, 1)}
}

func (handler *ServerHTTPHandler) serveEvents(writer http.ResponseWriter, request *http.Request) {
	session, workspace := handler.authenticatedWorkspace(writer, request)
	if session == nil || workspace == nil {
		return
	}
	if request.Method != http.MethodGet {
		writeServerHTTPAuthenticatedError(writer, session, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
		return
	}
	queue := newServerHTTPEventQueue()
	stop, err := workspace.Observe(request.Context(), func(event ServerWorkspaceEvent) {
		queue.enqueue(serverHTTPEvent{Version: 1, Event: string(event)})
	})
	if err != nil {
		writeServerHTTPAuthenticatedDomainError(writer, session, err)
		return
	}
	defer stop()
	defer queue.close()

	writeServerHTTPSecurityHeaders(writer)
	writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	queue.enqueue(serverHTTPEvent{Version: 1, Event: string(ServerWorkspaceChanged)})
	for {
		event, ok := queue.next(request.Context().Done())
		if !ok {
			return
		}
		if err := writeServerHTTPEvent(writer, event); err != nil {
			return
		}
		if err := http.NewResponseController(writer).Flush(); err != nil {
			return
		}
		if event.Event == string(ServerWorkspaceSessionRevoked) {
			return
		}
	}
}

func (queue *serverHTTPEventQueue) enqueue(event serverHTTPEvent) {
	queue.mu.Lock()
	if queue.closed || queue.terminal {
		queue.mu.Unlock()
		return
	}
	queue.events = append(queue.events, event)
	if event.Event == string(ServerWorkspaceSessionRevoked) {
		queue.terminal = true
	}
	queue.mu.Unlock()
	select {
	case queue.wake <- struct{}{}:
	default:
	}
}

func (queue *serverHTTPEventQueue) next(contextDone <-chan struct{}) (serverHTTPEvent, bool) {
	for {
		queue.mu.Lock()
		if len(queue.events) > 0 {
			event := queue.events[0]
			queue.events[0] = serverHTTPEvent{}
			queue.events = queue.events[1:]
			queue.mu.Unlock()
			return event, true
		}
		if queue.closed {
			queue.mu.Unlock()
			return serverHTTPEvent{}, false
		}
		queue.mu.Unlock()

		select {
		case <-queue.wake:
		case <-contextDone:
			return serverHTTPEvent{}, false
		}
	}
}

func (queue *serverHTTPEventQueue) close() {
	queue.mu.Lock()
	queue.closed = true
	queue.events = nil
	queue.mu.Unlock()
	select {
	case queue.wake <- struct{}{}:
	default:
	}
}

func writeServerHTTPEvent(writer io.Writer, event serverHTTPEvent) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "data: %s\n\n", payload)
	return err
}
