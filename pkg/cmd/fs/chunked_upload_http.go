package fs

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var chunkedUploadIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func (handler *readOnlyHandler) serveChunkedUpload(writer http.ResponseWriter, request *http.Request) {
	if handler.options.ChunkedUploads == nil {
		writeFSError(writer, http.StatusNotFound, "NOT_FOUND", "API route not found")
		return
	}
	if !handler.options.ManagementEnabled {
		writeFSError(writer, http.StatusForbidden, "MANAGEMENT_DISABLED", "Start fs with --manage to enable filesystem management")
		return
	}
	owner := "anonymous"
	if handler.options.Authentication != nil {
		owner = sessionToken(request)
	}
	if request.URL.Path == "/api/uploads" {
		handler.createChunkedUpload(writer, request, owner)
		return
	}
	remaining := strings.TrimPrefix(request.URL.Path, "/api/uploads/")
	parts := strings.Split(remaining, "/")
	if len(parts) == 0 || !chunkedUploadIDPattern.MatchString(parts[0]) {
		writeFSError(writer, http.StatusNotFound, "CHUNKED_UPLOAD_NOT_FOUND", "Chunked upload was not found")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch request.Method {
		case http.MethodGet:
			upload, err := handler.options.ChunkedUploads.Get(owner, id)
			handler.writeChunkedUpload(writer, upload, err, http.StatusOK)
		case http.MethodPut:
			handler.appendChunkedUpload(writer, request, owner, id)
		case http.MethodDelete:
			if !validFSOrigin(request, handler.options.BindingAddress) {
				writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
				return
			}
			if err := handler.options.ChunkedUploads.Cancel(owner, id); err != nil {
				writeWorkspaceError(writer, err)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		default:
			writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET, PUT, or DELETE")
		}
		return
	}
	if len(parts) == 2 && parts[1] == "complete" && request.Method == http.MethodPost {
		if !validFSOrigin(request, handler.options.BindingAddress) {
			writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
			return
		}
		upload, err := handler.options.ChunkedUploads.Complete(owner, id)
		handler.writeChunkedUpload(writer, upload, err, http.StatusOK)
		return
	}
	writeFSError(writer, http.StatusNotFound, "CHUNKED_UPLOAD_NOT_FOUND", "Chunked upload was not found")
}

func (handler *readOnlyHandler) createChunkedUpload(writer http.ResponseWriter, request *http.Request, owner string) {
	if request.Method != http.MethodPost {
		writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST")
		return
	}
	if !validFSOrigin(request, handler.options.BindingAddress) {
		writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
		return
	}
	if mediaType(request.Header.Get("Content-Type")) != "application/json" {
		writeFSError(writer, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Chunked upload creation must use JSON")
		return
	}
	var input struct {
		DirectoryPath string `json:"directoryPath"`
		Filename      string `json:"filename"`
		Size          int64  `json:"size"`
	}
	decoder := json.NewDecoder(io.LimitReader(request.Body, 64*1024+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Size == 0 || decoder.Decode(&struct{}{}) != io.EOF {
		writeFSError(writer, http.StatusBadRequest, "INVALID_UPLOAD", "Chunked upload request is invalid")
		return
	}
	directory, err := ParseWorkspacePath(input.DirectoryPath)
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	upload, err := handler.options.ChunkedUploads.Create(owner, directory, input.Filename, input.Size)
	handler.writeChunkedUpload(writer, upload, err, http.StatusCreated)
}

func (handler *readOnlyHandler) appendChunkedUpload(writer http.ResponseWriter, request *http.Request, owner, id string) {
	if !validFSOrigin(request, handler.options.BindingAddress) {
		writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
		return
	}
	if mediaType(request.Header.Get("Content-Type")) != "application/octet-stream" {
		writeFSError(writer, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Upload chunks must use application/octet-stream")
		return
	}
	start, end, total, ok := parseChunkContentRange(request.Header.Get("Content-Range"))
	if !ok {
		writeFSError(writer, http.StatusBadRequest, "INVALID_UPLOAD", "Content-Range is invalid")
		return
	}
	if length := request.ContentLength; length >= 0 && length != end-start+1 {
		writeFSError(writer, http.StatusBadRequest, "INVALID_UPLOAD", "Content-Length does not match Content-Range")
		return
	}
	upload, err := handler.options.ChunkedUploads.Append(owner, id, start, end, total, request.Body)
	handler.writeChunkedUpload(writer, upload, err, http.StatusOK)
}

func (handler *readOnlyHandler) writeChunkedUpload(writer http.ResponseWriter, upload ChunkedUpload, err error, status int) {
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	writeFSJSON(writer, status, struct {
		Version int           `json:"version"`
		Upload  ChunkedUpload `json:"upload"`
	}{Version: 1, Upload: upload})
}

func parseChunkContentRange(value string) (int64, int64, int64, bool) {
	match := regexp.MustCompile(`^bytes ([0-9]+)-([0-9]+)/([0-9]+)$`).FindStringSubmatch(value)
	if match == nil {
		return 0, 0, 0, false
	}
	start, startErr := strconv.ParseInt(match[1], 10, 64)
	end, endErr := strconv.ParseInt(match[2], 10, 64)
	total, totalErr := strconv.ParseInt(match[3], 10, 64)
	return start, end, total, startErr == nil && endErr == nil && totalErr == nil && start >= 0 && end >= start && total > end
}
