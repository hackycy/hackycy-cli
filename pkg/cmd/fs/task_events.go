package fs

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func serveTaskEvents[T any](writer http.ResponseWriter, request *http.Request, events <-chan []T, cancel func()) {
	defer cancel()
	headers := writer.Header()
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Connection", "keep-alive")
	headers.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	headers.Set("Content-Type", "text/event-stream; charset=utf-8")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Accel-Buffering", "no")
	headers.Set("X-Content-Type-Options", "nosniff")
	flusher, ok := writer.(http.Flusher)
	if !ok {
		return
	}
	for {
		select {
		case <-request.Context().Done():
			return
		case tasks, open := <-events:
			if !open {
				return
			}
			payload, err := json.Marshal(struct {
				Version int `json:"version"`
				Tasks   []T `json:"tasks"`
			}{Version: 1, Tasks: tasks})
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(writer, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
