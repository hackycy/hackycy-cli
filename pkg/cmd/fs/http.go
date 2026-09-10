package fs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	webassets "github.com/hackycy/hackycy-cli/web"
)

const fileSandboxCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

type ReadOnlyServerOptions struct {
	ManagementEnabled bool
	SafeHTML          bool
	Authentication    *Authentication
	BindingAddress    string
	ChunkedUploads    *ChunkedUploadManager
	Downloads         *DownloadManager
	Extractions       *ExtractionManager
	Thumbnails        *ThumbnailService
}

type readOnlyHandler struct {
	workspace *Workspace
	options   ReadOnlyServerOptions
}

// NewReadOnlyHandler exposes only the first FS migration slice. Mutating,
// session, task, thumbnail, and React routes remain absent until their owners
// are added in later slices.
func NewReadOnlyHandler(workspace *Workspace, options ReadOnlyServerOptions) http.Handler {
	return &readOnlyHandler{workspace: workspace, options: options}
}

// NewServerHandler composes the FS protocol adapter with its retained Vite
// application. Listener and process lifecycle remain command-owned.
func NewServerHandler(workspace *Workspace, options ReadOnlyServerOptions) (http.Handler, error) {
	if workspace == nil {
		return nil, errors.New("FS server handler requires a workspace")
	}
	return webassets.NewFSProductionHandler(NewReadOnlyHandler(workspace, options))
}

func (handler *readOnlyHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/api/session" {
		handler.serveSession(writer, request)
		return
	}
	if handler.options.Authentication != nil && protectedFSPath(request.URL.Path) {
		token := sessionToken(request)
		session, err := handler.options.Authentication.Resume(token)
		if err != nil {
			writeFSError(writer, http.StatusServiceUnavailable, "SESSION_UNAVAILABLE", "Session storage is unavailable")
			return
		}
		if session == nil {
			if token != "" {
				writer.Header().Set("Set-Cookie", expiredSessionCookie())
			}
			writeFSError(writer, http.StatusUnauthorized, "AUTHENTICATION_REQUIRED", "Authenticated session is required")
			return
		}
		writer.Header().Set("Set-Cookie", activeSessionCookie(token, session.ExpiresAt))
	}
	switch {
	case request.URL.Path == "/api/directory":
		handler.serveDirectory(writer, request)
	case request.URL.Path == "/api/text":
		handler.serveText(writer, request)
	case request.URL.Path == "/api/operations":
		handler.serveOperations(writer, request)
	case request.URL.Path == "/api/upload":
		handler.serveUpload(writer, request)
	case request.URL.Path == "/api/uploads" || strings.HasPrefix(request.URL.Path, "/api/uploads/"):
		handler.serveChunkedUpload(writer, request)
	case request.URL.Path == "/api/downloads" || strings.HasPrefix(request.URL.Path, "/api/downloads/"):
		handler.serveDownloads(writer, request)
	case request.URL.Path == "/api/extractions" || strings.HasPrefix(request.URL.Path, "/api/extractions/"):
		handler.serveExtractions(writer, request)
	case request.URL.Path == "/files" || strings.HasPrefix(request.URL.Path, "/files/"):
		handler.serveFile(writer, request)
	case request.URL.Path == "/thumbnails" || strings.HasPrefix(request.URL.Path, "/thumbnails/"):
		handler.serveThumbnail(writer, request)
	case strings.HasPrefix(request.URL.Path, "/api/"):
		writeFSError(writer, http.StatusNotFound, "NOT_FOUND", "API route not found")
	default:
		writeFSError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}
}

func (handler *readOnlyHandler) serveThumbnail(writer http.ResponseWriter, request *http.Request) {
	if handler.options.Thumbnails == nil {
		writeFSError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET or HEAD")
		return
	}
	path, err := resourceWorkspacePath(request.URL, "/thumbnails")
	if err != nil {
		writeThumbnailError(writer, err)
		return
	}
	thumbnail, err := handler.options.Thumbnails.Get(path)
	if err != nil {
		writeThumbnailError(writer, err)
		return
	}
	etag := fmt.Sprintf("W/\"thumb-%d-%d-160-72\"", thumbnail.Identity.Size, thumbnail.Identity.ModifiedAt.UnixMilli())
	headers := writer.Header()
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Content-Length", strconv.Itoa(len(thumbnail.Bytes)))
	headers.Set("Content-Type", "image/webp")
	headers.Set("ETag", etag)
	headers.Set("Last-Modified", thumbnail.Identity.ModifiedAt.UTC().Format(http.TimeFormat))
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Content-Type-Options", "nosniff")
	if isFSNotModified(request, etag, thumbnail.Identity.ModifiedAt) {
		headers.Del("Content-Length")
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = writer.Write(thumbnail.Bytes)
	}
}

func (handler *readOnlyHandler) serveDirectory(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
		return
	}
	path, err := queryWorkspacePath(request, "path")
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	listing, err := handler.workspace.ReadDirectory(path, handler.options.ManagementEnabled, handler.options.ChunkedUploads)
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	writeFSJSON(writer, http.StatusOK, struct {
		Version int `json:"version"`
		DirectoryListing
	}{Version: 1, DirectoryListing: listing})
}

func (handler *readOnlyHandler) serveSession(writer http.ResponseWriter, request *http.Request) {
	token := sessionToken(request)
	if handler.options.Authentication == nil {
		if request.Method != http.MethodGet {
			writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
			return
		}
		if token != "" {
			writer.Header().Set("Set-Cookie", expiredSessionCookie())
		}
		writeFSJSON(writer, http.StatusOK, struct {
			Version               int  `json:"version"`
			AuthenticationEnabled bool `json:"authenticationEnabled"`
		}{Version: 1, AuthenticationEnabled: false})
		return
	}
	switch request.Method {
	case http.MethodGet:
		session, err := handler.options.Authentication.Resume(token)
		if err != nil {
			writeFSError(writer, http.StatusServiceUnavailable, "SESSION_UNAVAILABLE", "Session storage is unavailable")
			return
		}
		if session == nil {
			if token != "" {
				writer.Header().Set("Set-Cookie", expiredSessionCookie())
			}
			writeFSJSON(writer, http.StatusOK, struct {
				Version               int  `json:"version"`
				AuthenticationEnabled bool `json:"authenticationEnabled"`
				Authenticated         bool `json:"authenticated"`
			}{Version: 1, AuthenticationEnabled: true, Authenticated: false})
			return
		}
		writer.Header().Set("Set-Cookie", activeSessionCookie(token, session.ExpiresAt))
		writeFSJSON(writer, http.StatusOK, struct {
			Version               int            `json:"version"`
			AuthenticationEnabled bool           `json:"authenticationEnabled"`
			Authenticated         bool           `json:"authenticated"`
			Account               SessionAccount `json:"account"`
		}{Version: 1, AuthenticationEnabled: true, Authenticated: true, Account: session.Account})
	case http.MethodPost:
		if !validFSOrigin(request, handler.options.BindingAddress) {
			writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
			return
		}
		if mediaType(request.Header.Get("Content-Type")) != "application/json" {
			writeFSError(writer, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Login requests must use JSON")
			return
		}
		var input struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 1<<20))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil || input.Username == "" || input.Password == "" || decoder.Decode(&struct{}{}) != io.EOF {
			writeFSError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Login request is invalid")
			return
		}
		grant, err := handler.options.Authentication.SignIn(input.Username, input.Password)
		if err != nil {
			writeFSError(writer, http.StatusServiceUnavailable, "SESSION_UNAVAILABLE", "Session storage is unavailable")
			return
		}
		if grant == nil {
			writeFSError(writer, http.StatusUnauthorized, "AUTHENTICATION_FAILED", "Account credentials are invalid")
			return
		}
		writer.Header().Set("Set-Cookie", activeSessionCookie(grant.Token, grant.ExpiresAt))
		writeFSJSON(writer, http.StatusOK, struct {
			Version               int            `json:"version"`
			AuthenticationEnabled bool           `json:"authenticationEnabled"`
			Authenticated         bool           `json:"authenticated"`
			Account               SessionAccount `json:"account"`
		}{Version: 1, AuthenticationEnabled: true, Authenticated: true, Account: grant.Account})
	case http.MethodDelete:
		if !validFSOrigin(request, handler.options.BindingAddress) {
			writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
			return
		}
		if err := handler.options.Authentication.SignOut(token); err != nil {
			writeFSError(writer, http.StatusServiceUnavailable, "SESSION_UNAVAILABLE", "Session storage is unavailable")
			return
		}
		writer.Header().Set("Set-Cookie", expiredSessionCookie())
		writer.Header().Set("Cache-Control", "no-store")
		writer.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.WriteHeader(http.StatusNoContent)
	default:
		writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET, POST, or DELETE")
	}
}

func (handler *readOnlyHandler) serveText(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPut {
		handler.saveText(writer, request)
		return
	}
	if request.Method != http.MethodGet {
		writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
		return
	}
	path, err := queryWorkspacePath(request, "path")
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	preview, err := handler.workspace.ReadText(path)
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	writeFSJSON(writer, http.StatusOK, struct {
		Version int `json:"version"`
		TextPreview
	}{Version: 1, TextPreview: preview})
}

func (handler *readOnlyHandler) saveText(writer http.ResponseWriter, request *http.Request) {
	if !handler.options.ManagementEnabled {
		writeFSError(writer, http.StatusForbidden, "MANAGEMENT_DISABLED", "Start fs with --manage to enable filesystem management")
		return
	}
	if !validFSOrigin(request, handler.options.BindingAddress) {
		writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
		return
	}
	if !validTextMediaType(request.Header.Get("Content-Type")) {
		writeFSError(writer, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Text saves must use UTF-8 text/plain")
		return
	}
	revision := request.Header.Get("If-Match")
	if revision == "" {
		writeFSError(writer, http.StatusPreconditionRequired, "PRECONDITION_REQUIRED", "If-Match is required when saving text")
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, MaxTextPreviewBytes+1))
	if err != nil {
		writeFSError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Edited text could not be read")
		return
	}
	if int64(len(body)) > MaxTextPreviewBytes {
		writeFSError(writer, http.StatusRequestEntityTooLarge, "TOO_LARGE", "Edited text exceeds the 10 MiB limit")
		return
	}
	path, err := queryWorkspacePath(request, "path")
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	result, err := handler.workspace.SaveText(path, string(body), revision)
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	writeFSJSON(writer, http.StatusOK, struct {
		Version int `json:"version"`
		TextSaveResult
	}{Version: 1, TextSaveResult: result})
}

func (handler *readOnlyHandler) serveOperations(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST")
		return
	}
	if !handler.options.ManagementEnabled {
		writeFSError(writer, http.StatusForbidden, "MANAGEMENT_DISABLED", "Start fs with --manage to enable filesystem management")
		return
	}
	if !validFSOrigin(request, handler.options.BindingAddress) {
		writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
		return
	}
	if mediaType(request.Header.Get("Content-Type")) != "application/json" {
		writeFSError(writer, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Operations must use JSON")
		return
	}
	operation, err := decodeOperation(request.Body)
	if err != nil {
		writeFSError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Operation request is invalid")
		return
	}
	result := handler.workspace.ApplyOperation(operation)
	writeFSJSON(writer, http.StatusOK, struct {
		Version int `json:"version"`
		OperationResult
	}{Version: 1, OperationResult: result})
}

func (handler *readOnlyHandler) serveUpload(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use POST")
		return
	}
	if !handler.options.ManagementEnabled {
		writeFSError(writer, http.StatusForbidden, "MANAGEMENT_DISABLED", "Start fs with --manage to enable filesystem management")
		return
	}
	if !validFSOrigin(request, handler.options.BindingAddress) {
		writeFSError(writer, http.StatusForbidden, "ORIGIN_FORBIDDEN", "Mutation requests must come from the bound same origin")
		return
	}
	reader, err := request.MultipartReader()
	if err != nil {
		writeFSError(writer, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "Upload requests must use multipart form data")
		return
	}
	directory, err := queryWorkspacePath(request, "path")
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeFSError(writer, http.StatusBadRequest, "INVALID_UPLOAD", "Upload request is invalid")
			return
		}
		if part.FormName() == "file" && part.FileName() != "" {
			result, uploadErr := handler.workspace.Upload(directory, part.FileName(), part)
			_ = part.Close()
			if uploadErr != nil {
				writeWorkspaceError(writer, uploadErr)
				return
			}
			writeFSJSON(writer, http.StatusOK, struct {
				Version int `json:"version"`
				UploadResult
			}{Version: 1, UploadResult: result})
			return
		}
		_ = part.Close()
	}
	writeFSError(writer, http.StatusBadRequest, "INVALID_UPLOAD", "Upload request must contain a file")
}

func (handler *readOnlyHandler) serveFile(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodOptions {
		if handler.options.Authentication == nil {
			writer.Header().Set("Access-Control-Allow-Headers", "Range, If-None-Match, If-Modified-Since, If-Range")
			writer.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
			writer.Header().Set("Access-Control-Allow-Origin", "*")
			writer.Header().Set("Access-Control-Max-Age", "86400")
		}
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writeFSError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET or HEAD")
		return
	}
	path, err := resourceWorkspacePath(request.URL, "/files")
	if err != nil {
		writeWorkspaceError(writer, err)
		return
	}
	file, err := handler.workspace.OpenFile(path)
	if err != nil {
		if errors.Is(err, ErrWorkspacePathNotFile) {
			if _, directoryErr := handler.workspace.List(path); directoryErr == nil {
				location := "/"
				if path.String() != "" {
					location = "/browse/" + encodeWorkspacePath(path.String())
				}
				http.Redirect(writer, request, location, http.StatusFound)
				return
			}
		}
		writeWorkspaceError(writer, err)
		return
	}
	defer file.Close()
	identity := file.Identity()
	mimeType := workspaceMIMEType(identity.Name)
	etag := fmt.Sprintf("W/\"%d-%d\"", identity.Size, identity.ModifiedAt.UnixMilli())
	setFileHeaders(writer.Header(), identity, mimeType, etag, request.URL.Query().Get("download") == "1", handler.options.SafeHTML, handler.options.Authentication != nil)
	if isFSNotModified(request, etag, identity.ModifiedAt) {
		writer.Header().Del("Content-Length")
		writer.WriteHeader(http.StatusNotModified)
		return
	}
	start, end, ranged, valid := requestedFSRange(request, etag, identity.ModifiedAt, identity.Size)
	if !valid {
		writer.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(identity.Size, 10))
		writer.Header().Del("Content-Length")
		writer.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	if ranged {
		writer.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, identity.Size))
		writer.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
		writer.WriteHeader(http.StatusPartialContent)
		if request.Method != http.MethodHead {
			_, _ = file.Seek(start, io.SeekStart)
			_, _ = io.CopyN(writer, file, end-start+1)
		}
		return
	}
	writer.WriteHeader(http.StatusOK)
	if request.Method != http.MethodHead {
		_, _ = io.Copy(writer, file)
	}
}

func queryWorkspacePath(request *http.Request, key string) (WorkspacePath, error) {
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return WorkspacePath{}, ErrInvalidWorkspacePath
	}
	return ParseWorkspacePath(values.Get(key))
}

func resourceWorkspacePath(requestURL *url.URL, prefix string) (WorkspacePath, error) {
	escaped := requestURL.EscapedPath()
	if escaped == prefix {
		return ParseWorkspacePath("")
	}
	if !strings.HasPrefix(escaped, prefix+"/") {
		return WorkspacePath{}, ErrInvalidWorkspacePath
	}
	parts := strings.Split(strings.TrimPrefix(escaped, prefix+"/"), "/")
	decoded := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		value, err := url.PathUnescape(part)
		if err != nil {
			return WorkspacePath{}, ErrInvalidWorkspacePath
		}
		decoded = append(decoded, value)
	}
	return ParseWorkspacePath(strings.Join(decoded, "/"))
}

func setFileHeaders(headers http.Header, identity FileIdentity, mimeType, etag string, download, safeHTML, authentication bool) {
	base := strings.ToLower(strings.TrimSpace(strings.Split(mimeType, ";")[0]))
	inline := inlineWorkspaceMIME(base) && !download && !(safeHTML && htmlWorkspaceMIME(base))
	headers.Set("Accept-Ranges", "bytes")
	headers.Set("Cache-Control", "no-cache")
	headers.Set("Content-Disposition", contentDisposition(identity.Name, inline))
	headers.Set("Content-Length", strconv.FormatInt(identity.Size, 10))
	headers.Set("Content-Type", mimeType)
	headers.Set("ETag", etag)
	headers.Set("Last-Modified", identity.ModifiedAt.UTC().Format(http.TimeFormat))
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Content-Type-Options", "nosniff")
	if !authentication {
		headers.Set("Access-Control-Allow-Origin", "*")
	}
	if documentSandboxRequired(base, safeHTML) {
		headers.Set("Content-Security-Policy", fileSandboxCSP)
	}
}

func inlineWorkspaceMIME(base string) bool {
	return strings.HasPrefix(base, "text/") || strings.HasPrefix(base, "image/") || strings.HasPrefix(base, "video/") || strings.HasPrefix(base, "audio/") || base == "application/pdf" || base == "application/json" || base == "application/xml" || base == "application/javascript" || base == "application/xhtml+xml" || base == "application/ld+json"
}

func htmlWorkspaceMIME(base string) bool {
	return base == "text/html" || base == "application/xhtml+xml"
}

func documentSandboxRequired(base string, safeHTML bool) bool {
	if !safeHTML && htmlWorkspaceMIME(base) {
		return false
	}
	return base == "text/html" || base == "application/xhtml+xml" || base == "application/xml" || base == "text/xml" || base == "image/svg+xml"
}

func contentDisposition(filename string, inline bool) string {
	disposition := "attachment"
	if inline {
		disposition = "inline"
	}
	encoded := encodeFilename(filename)
	return disposition + `; filename="` + encoded + `"; filename*=UTF-8''` + encoded
}

func encodeFilename(value string) string {
	const hexadecimal = "0123456789ABCDEF"
	var builder strings.Builder
	for _, byteValue := range []byte(value) {
		if (byteValue >= 'a' && byteValue <= 'z') || (byteValue >= 'A' && byteValue <= 'Z') || (byteValue >= '0' && byteValue <= '9') || strings.ContainsRune("-_.!~*()", rune(byteValue)) {
			builder.WriteByte(byteValue)
			continue
		}
		builder.WriteByte('%')
		builder.WriteByte(hexadecimal[byteValue>>4])
		builder.WriteByte(hexadecimal[byteValue&0x0f])
	}
	return builder.String()
}

func encodeWorkspacePath(value string) string {
	parts := strings.Split(value, "/")
	for index := range parts {
		parts[index] = url.PathEscape(parts[index])
	}
	return strings.Join(parts, "/")
}

func isFSNotModified(request *http.Request, etag string, modifiedAt time.Time) bool {
	if value := request.Header.Get("If-None-Match"); value != "" {
		for _, candidate := range strings.Split(value, ",") {
			if strings.TrimSpace(candidate) == "*" || strings.TrimSpace(candidate) == etag {
				return true
			}
		}
		return false
	}
	if value := request.Header.Get("If-Modified-Since"); value != "" {
		if timestamp, err := http.ParseTime(value); err == nil {
			return modifiedAt.Before(timestamp.Add(time.Second))
		}
	}
	return false
}

func requestedFSRange(request *http.Request, etag string, modifiedAt time.Time, size int64) (int64, int64, bool, bool) {
	value := request.Header.Get("Range")
	if value == "" || !fsRangeAllowed(request.Header.Get("If-Range"), etag, modifiedAt) {
		return 0, size - 1, false, true
	}
	if strings.Contains(value, ",") || !strings.HasPrefix(value, "bytes=") || size == 0 {
		return 0, 0, true, false
	}
	parts := strings.Split(strings.TrimPrefix(value, "bytes="), "-")
	if len(parts) != 2 || (parts[0] == "" && parts[1] == "") {
		return 0, 0, true, false
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, true, false
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true, true
	}
	start, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, true, false
	}
	end := size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, true, false
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true, true
}

func fsRangeAllowed(value, etag string, modifiedAt time.Time) bool {
	if value == "" {
		return true
	}
	if strings.HasPrefix(value, "W/") {
		return false
	}
	if strings.HasPrefix(value, "\"") {
		return value == etag
	}
	timestamp, err := http.ParseTime(value)
	return err == nil && modifiedAt.Before(timestamp.Add(time.Second))
}

func protectedFSPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/") || path == "/files" || strings.HasPrefix(path, "/files/") || path == "/thumbnails" || strings.HasPrefix(path, "/thumbnails/")
}

func sessionToken(request *http.Request) string {
	for _, cookie := range request.Cookies() {
		if cookie.Name == "ycy_fs_session" {
			return cookie.Value
		}
	}
	return ""
}

func activeSessionCookie(token string, expiresAt time.Time) string {
	seconds := int(time.Until(expiresAt).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return "ycy_fs_session=" + token + "; HttpOnly; SameSite=Strict; Path=/; Max-Age=" + strconv.Itoa(seconds)
}

func expiredSessionCookie() string {
	return "ycy_fs_session=; HttpOnly; SameSite=Strict; Path=/; Max-Age=0"
}

func mediaType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
}

func validTextMediaType(value string) bool {
	parts := strings.Split(value, ";")
	if len(parts) == 0 || strings.ToLower(strings.TrimSpace(parts[0])) != "text/plain" {
		return false
	}
	for _, parameter := range parts[1:] {
		if strings.ToLower(strings.TrimSpace(parameter)) != "charset=utf-8" {
			return false
		}
	}
	return true
}

func validFSOrigin(request *http.Request, bindingAddress string) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return false
	}
	if bindingAddress == "" {
		bindingAddress = request.URL.Hostname()
	}
	requestOrigin := "http://" + request.Host
	if request.TLS != nil {
		requestOrigin = "https://" + request.Host
	}
	if origin != requestOrigin {
		return false
	}
	host := request.URL.Hostname()
	if host == "" {
		host, _, _ = strings.Cut(request.Host, ":")
	}
	return bindingAllowsFSHost(bindingAddress, host)
}

func bindingAllowsFSHost(bindingAddress, host string) bool {
	switch bindingAddress {
	case "0.0.0.0":
		return host == "localhost" || net.ParseIP(host).To4() != nil
	case "::":
		return host == "localhost" || net.ParseIP(host) != nil
	case "127.0.0.1", "::1":
		return host == bindingAddress || host == "localhost"
	default:
		return host == bindingAddress
	}
}
