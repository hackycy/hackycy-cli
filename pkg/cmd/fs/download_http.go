package fs

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (handler *readOnlyHandler) serveDownloads(writer http.ResponseWriter, request *http.Request) {
	if handler.options.Downloads == nil {
		writeFSError(writer, http.StatusNotFound, "NOT_FOUND", "API route not found")
		return
	}
	if !handler.options.ManagementEnabled {
		writeFSError(writer, http.StatusForbidden, "MANAGEMENT_DISABLED", "Start fs with --manage to enable filesystem management")
		return
	}
	if request.URL.Path == "/api/downloads/events" {
		if request.Method != http.MethodGet {
			writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
			return
		}
		events, cancel := handler.options.Downloads.Subscribe()
		stopObserving := handler.options.Authentication.Observe(sessionToken(request), cancel)
		serveTaskEvents(writer, request, events, func() {
			stopObserving()
			cancel()
		})
		return
	}
	if request.URL.Path == "/api/downloads" {
		switch request.Method {
		case http.MethodGet:
			writeFSJSON(writer, http.StatusOK, struct {
				Version int            `json:"version"`
				Tasks   []DownloadTask `json:"tasks"`
			}{Version: 1, Tasks: handler.options.Downloads.List()})
		case http.MethodPost:
			handler.createDownload(writer, request)
		case http.MethodDelete:
			if !validFSOrigin(request, handler.options.BindingAddress) {
				writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
				return
			}
			if request.URL.Query().Get("terminal") != "1" {
				writeFSError(writer, http.StatusBadRequest, "INVALID_DOWNLOAD", "terminal=1 is required")
				return
			}
			handler.options.Downloads.ClearTerminal()
			writer.WriteHeader(http.StatusNoContent)
		default:
			writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET, POST, or DELETE")
		}
		return
	}
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/api/downloads/"), "/")
	if len(parts) != 2 || (parts[1] != "cancel" && parts[1] != "retry") || request.Method != http.MethodPost {
		writeFSError(writer, http.StatusNotFound, "DOWNLOAD_NOT_FOUND", "Download task was not found")
		return
	}
	if !validFSOrigin(request, handler.options.BindingAddress) {
		writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
		return
	}
	var task DownloadTask
	var err error
	if parts[1] == "cancel" {
		task, err = handler.options.Downloads.Cancel(parts[0])
	} else {
		task, err = handler.options.Downloads.Retry(parts[0])
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
		Version int          `json:"version"`
		Task    DownloadTask `json:"task"`
	}{Version: 1, Task: task})
}

func (handler *readOnlyHandler) createDownload(writer http.ResponseWriter, request *http.Request) {
	if !validFSOrigin(request, handler.options.BindingAddress) {
		writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
		return
	}
	if mediaType(request.Header.Get("Content-Type")) != "application/json" {
		writeFSError(writer, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Download requests must use JSON")
		return
	}
	var input map[string]json.RawMessage
	decoder := json.NewDecoder(io.LimitReader(request.Body, 1<<20))
	if err := decoder.Decode(&input); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		writeFSError(writer, http.StatusBadRequest, "INVALID_DOWNLOAD", "Download request is invalid")
		return
	}
	for field := range input {
		if field != "url" && field != "directoryPath" && field != "filename" {
			writeFSError(writer, http.StatusBadRequest, "INVALID_DOWNLOAD", "Download request is invalid")
			return
		}
	}
	var inputURL, directoryPath, filename string
	if !decodeDownloadString(input, "url", &inputURL, true) || !decodeDownloadString(input, "directoryPath", &directoryPath, true) || !decodeDownloadString(input, "filename", &filename, false) {
		writeFSError(writer, http.StatusBadRequest, "INVALID_DOWNLOAD", "Download request is invalid")
		return
	}
	task, err := handler.options.Downloads.Enqueue(DownloadRequest{URL: inputURL, DirectoryPath: directoryPath, Filename: filename})
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	writeFSJSON(writer, http.StatusAccepted, struct {
		Version int          `json:"version"`
		Task    DownloadTask `json:"task"`
	}{Version: 1, Task: task})
}

func decodeDownloadString(fields map[string]json.RawMessage, name string, destination *string, required bool) bool {
	value, found := fields[name]
	if !found {
		return !required
	}
	if string(value) == "null" || json.Unmarshal(value, destination) != nil {
		return false
	}
	return true
}
