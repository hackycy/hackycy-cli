package fs

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (handler *readOnlyHandler) serveExtractions(writer http.ResponseWriter, request *http.Request) {
	if handler.options.Extractions == nil {
		writeFSError(writer, http.StatusNotFound, "NOT_FOUND", "API route not found")
		return
	}
	if !handler.options.ManagementEnabled {
		writeFSError(writer, http.StatusForbidden, "MANAGEMENT_DISABLED", "Start fs with --manage to enable archive extraction")
		return
	}
	if request.URL.Path == "/api/extractions/events" {
		if request.Method != http.MethodGet {
			writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
			return
		}
		events, cancel := handler.options.Extractions.Subscribe()
		stopObserving := handler.options.Authentication.Observe(sessionToken(request), cancel)
		serveTaskEvents(writer, request, events, func() {
			stopObserving()
			cancel()
		})
		return
	}
	if request.URL.Path == "/api/extractions" {
		switch request.Method {
		case http.MethodGet:
			writeFSJSON(writer, http.StatusOK, struct {
				Version int              `json:"version"`
				Tasks   []ExtractionTask `json:"tasks"`
			}{Version: 1, Tasks: handler.options.Extractions.List()})
		case http.MethodPost:
			handler.createExtraction(writer, request)
		case http.MethodDelete:
			if !validFSOrigin(request, handler.options.BindingAddress) {
				writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
				return
			}
			if request.URL.Query().Get("terminal") != "1" {
				writeFSError(writer, http.StatusBadRequest, "INVALID_EXTRACTION", "Use terminal=1 to clear completed extractions")
				return
			}
			handler.options.Extractions.ClearTerminal()
			writeFSNoContent(writer)
		default:
			writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET, POST, or DELETE")
		}
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/extractions/"), "/")
	if len(parts) != 2 || (parts[1] != "cancel" && parts[1] != "retry") || request.Method != http.MethodPost {
		writeFSError(writer, http.StatusNotFound, "EXTRACTION_NOT_FOUND", "Extraction task was not found")
		return
	}
	if !validFSOrigin(request, handler.options.BindingAddress) {
		writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
		return
	}
	var task ExtractionTask
	var err error
	if parts[1] == "cancel" {
		task, err = handler.options.Extractions.Cancel(parts[0])
	} else {
		task, err = handler.options.Extractions.Retry(parts[0])
	}
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	status := http.StatusOK
	if parts[1] == "retry" {
		status = http.StatusAccepted
	}
	writeFSJSON(writer, status, struct {
		Version int            `json:"version"`
		Task    ExtractionTask `json:"task"`
	}{Version: 1, Task: task})
}

func (handler *readOnlyHandler) createExtraction(writer http.ResponseWriter, request *http.Request) {
	if !validFSOrigin(request, handler.options.BindingAddress) {
		writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
		return
	}
	if mediaType(request.Header.Get("Content-Type")) != "application/json" {
		writeFSError(writer, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Extraction requests must use JSON")
		return
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	if err := decoder.Decode(&fields); err != nil || decoder.Decode(&struct{}{}) != io.EOF || len(fields) != 1 {
		writeFSError(writer, http.StatusBadRequest, "INVALID_EXTRACTION", "Extraction request is invalid")
		return
	}
	rawPaths, found := fields["paths"]
	var paths []string
	if !found || string(rawPaths) == "null" || json.Unmarshal(rawPaths, &paths) != nil {
		writeFSError(writer, http.StatusBadRequest, "INVALID_EXTRACTION", "Extraction request is invalid")
		return
	}
	tasks, err := handler.options.Extractions.Enqueue(paths)
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	writeFSJSON(writer, http.StatusAccepted, struct {
		Version int              `json:"version"`
		Tasks   []ExtractionTask `json:"tasks"`
	}{Version: 1, Tasks: tasks})
}
