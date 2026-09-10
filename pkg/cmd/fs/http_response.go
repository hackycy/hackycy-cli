package fs

import (
	"encoding/json"
	"errors"
	"net/http"
)

func writeFSJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeFSError(writer http.ResponseWriter, status int, code, message string) {
	writeFSJSON(writer, status, struct {
		Version int `json:"version"`
		Error   struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Version: 1, Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: code, Message: message}})
}

func writeFSNoContent(writer http.ResponseWriter) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusNoContent)
}

func writeWorkspaceError(writer http.ResponseWriter, err error) {
	var service *ServiceError
	if errors.As(err, &service) {
		switch service.Code {
		case "TOO_LARGE":
			writeFSError(writer, http.StatusRequestEntityTooLarge, service.Code, service.Message)
		case "REVISION_MISMATCH":
			writeFSError(writer, http.StatusPreconditionFailed, service.Code, service.Message)
		case "UNSUPPORTED_TEXT":
			writeFSError(writer, http.StatusConflict, service.Code, service.Message)
		case "INVALID_OPERATION", "ALREADY_EXISTS", "ROOT_IMMUTABLE", "NAME_EXHAUSTED":
			writeFSError(writer, http.StatusConflict, service.Code, service.Message)
		case "NOT_FOUND":
			writeFSError(writer, http.StatusNotFound, service.Code, service.Message)
		case "INVALID_UPLOAD", "INVALID_NAME":
			writeFSError(writer, http.StatusBadRequest, service.Code, service.Message)
		case "CHUNKED_UPLOAD_NOT_FOUND":
			writeFSError(writer, http.StatusNotFound, service.Code, service.Message)
		case "CHUNKED_UPLOAD_LIMIT_REACHED":
			writeFSError(writer, http.StatusTooManyRequests, service.Code, service.Message)
		case "CHUNKED_UPLOAD_OFFSET_MISMATCH", "CHUNKED_UPLOAD_INCOMPLETE":
			writeFSError(writer, http.StatusConflict, service.Code, service.Message)
		case "DOWNLOAD_NOT_FOUND":
			writeFSError(writer, http.StatusNotFound, service.Code, service.Message)
		case "DOWNLOAD_QUEUE_FULL":
			writeFSError(writer, http.StatusTooManyRequests, service.Code, service.Message)
		case "DOWNLOAD_ACTIVE":
			writeFSError(writer, http.StatusConflict, service.Code, service.Message)
		case "DOWNLOAD_SERVICE_STOPPED":
			writeFSError(writer, http.StatusServiceUnavailable, service.Code, service.Message)
		case "DOWNLOAD_UNAVAILABLE":
			writeFSError(writer, http.StatusBadGateway, service.Code, service.Message)
		case "INVALID_DOWNLOAD":
			writeFSError(writer, http.StatusBadRequest, service.Code, service.Message)
		case "URL_FORBIDDEN":
			writeFSError(writer, http.StatusForbidden, service.Code, service.Message)
		case "EXTRACTION_NOT_FOUND":
			writeFSError(writer, http.StatusNotFound, service.Code, service.Message)
		case "EXTRACTION_QUEUE_FULL":
			writeFSError(writer, http.StatusTooManyRequests, service.Code, service.Message)
		case "EXTRACTION_ACTIVE":
			writeFSError(writer, http.StatusConflict, service.Code, service.Message)
		case "EXTRACTION_SERVICE_STOPPED":
			writeFSError(writer, http.StatusServiceUnavailable, service.Code, service.Message)
		case "INVALID_EXTRACTION":
			writeFSError(writer, http.StatusBadRequest, service.Code, service.Message)
		default:
			writeFSError(writer, http.StatusInternalServerError, service.Code, service.Message)
		}
		return
	}
	switch {
	case errors.Is(err, ErrInvalidWorkspacePath):
		writeFSError(writer, http.StatusBadRequest, "INVALID_PATH", "Path must be relative to the file browser directory")
	case errors.Is(err, ErrWorkspacePathNotDir):
		writeFSError(writer, http.StatusConflict, "NOT_DIRECTORY", "Path is not a directory")
	case errors.Is(err, ErrWorkspacePathNotFile):
		writeFSError(writer, http.StatusConflict, "NOT_FILE", "Path is not a file")
	case errors.Is(err, ErrWorkspaceUnavailable):
		writeFSError(writer, http.StatusInternalServerError, "UNAVAILABLE", "Filesystem operation failed")
	default:
		writeFSError(writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
	}
}

func writeThumbnailError(writer http.ResponseWriter, err error) {
	var service *ServiceError
	if errors.As(err, &service) {
		switch service.Code {
		case "THUMBNAIL_UNSUPPORTED":
			writeFSError(writer, http.StatusNotFound, "THUMBNAIL_ERROR", service.Message)
		case "THUMBNAIL_TOO_LARGE":
			writeFSError(writer, http.StatusRequestEntityTooLarge, "THUMBNAIL_ERROR", service.Message)
		case "THUMBNAIL_INVALID":
			writeFSError(writer, http.StatusUnprocessableEntity, "THUMBNAIL_ERROR", service.Message)
		case "THUMBNAIL_TIMEOUT":
			writeFSError(writer, http.StatusGatewayTimeout, "THUMBNAIL_ERROR", service.Message)
		default:
			writeFSError(writer, http.StatusServiceUnavailable, "THUMBNAIL_ERROR", service.Message)
		}
		return
	}
	if errors.Is(err, ErrWorkspacePathNotFile) || errors.Is(err, ErrWorkspaceNotFound) {
		writeFSError(writer, http.StatusNotFound, "THUMBNAIL_ERROR", "Thumbnail source was not found")
		return
	}
	if errors.Is(err, ErrInvalidWorkspacePath) {
		writeFSError(writer, http.StatusBadRequest, "THUMBNAIL_ERROR", "Thumbnail path is invalid")
		return
	}
	writeFSError(writer, http.StatusInternalServerError, "THUMBNAIL_ERROR", "Thumbnail could not be generated")
}
