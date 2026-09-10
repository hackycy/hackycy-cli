package diff

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
)

const diffAPICSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; worker-src 'self'; img-src 'self' blob: data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'"

type diffHTTPHandler struct {
	workspace *Workspace
	refresh   *refreshCoordinator
}

// NewHTTPHandler creates the command-owned REST boundary. It deliberately has
// no filesystem-path inputs; every read is resolved through a current snapshot.
func NewHTTPHandler(workspace *Workspace) http.Handler {
	return newHTTPHandler(workspace, newRefreshCoordinator(workspace))
}

// ProtocolHandlers contains the paired command-owned REST and MCP boundaries.
// Both handlers share one Refresh lifecycle so either protocol can cancel work
// started by the other.
type ProtocolHandlers struct {
	REST http.Handler
	MCP  http.Handler

	refresh *refreshCoordinator
}

// NewProtocolHandlers creates the paired Diff protocol boundaries for one
// fixed Comparison Workspace.
func NewProtocolHandlers(workspace *Workspace, bindingAddress string) ProtocolHandlers {
	return newProtocolHandlers(workspace, bindingAddress, nil)
}

func newProtocolHandlers(workspace *Workspace, bindingAddress string, lifecycle *diffLifecycle) ProtocolHandlers {
	refresh := newRefreshCoordinator(workspace, lifecycle)
	return ProtocolHandlers{
		REST:    newHTTPHandler(workspace, refresh),
		MCP:     newMCPHandler(workspace, bindingAddress, refresh),
		refresh: refresh,
	}
}

func newHTTPHandler(workspace *Workspace, refresh *refreshCoordinator) *diffHTTPHandler {
	return &diffHTTPHandler{workspace: workspace, refresh: refresh}
}

func (handler *diffHTTPHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	switch request.URL.Path {
	case "/api/state":
		handler.serveState(writer, request)
	case "/api/events":
		handler.serveEvents(writer, request)
	case "/api/refresh":
		handler.serveRefresh(writer, request)
	case "/api/entries":
		handler.serveEntries(writer, request)
	case "/api/tree":
		handler.serveTree(writer, request)
	case "/api/search":
		handler.serveSearch(writer, request)
	default:
		if strings.HasPrefix(request.URL.Path, "/api/entries/") {
			handler.serveEntryResource(writer, request)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/") {
			writeHTTPError(writer, http.StatusNotFound, "NOT_FOUND", "API route not found")
			return
		}
		writeHTTPError(writer, http.StatusNotFound, "NOT_FOUND", "Route not found")
	}
}

func (handler *diffHTTPHandler) serveState(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeHTTPError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
		return
	}

	response := handler.makeHTTPStatePayload(handler.workspace.State())
	writeHTTPJSON(writer, http.StatusOK, response)
}

func (handler *diffHTTPHandler) makeHTTPStatePayload(state WorkspaceState) httpStatePayload {
	response := httpStatePayload{
		Version:   1,
		Workspace: makeHTTPWorkspaceState(state),
	}
	if snapshot := handler.workspace.Snapshot(); snapshot != nil {
		summary := makeHTTPSnapshotSummary(snapshot.Summary())
		response.Snapshot = &summary
	}
	return response
}

type httpErrorPayload struct {
	Version int `json:"version"`
	Error   struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type httpStatePayload struct {
	Version   int                `json:"version"`
	Workspace httpWorkspaceState `json:"workspace"`
	Snapshot  *httpSnapshot      `json:"snapshot,omitempty"`
}

type httpWorkspaceState struct {
	Phase      WorkspacePhase         `json:"phase"`
	SnapshotID string                 `json:"snapshotId,omitempty"`
	Error      string                 `json:"error,omitempty"`
	Progress   *httpWorkspaceProgress `json:"progress,omitempty"`
}

type httpWorkspaceProgress struct {
	DiscoveredEntries int    `json:"discoveredEntries"`
	ComparedEntries   int    `json:"comparedEntries"`
	TotalEntries      *int   `json:"totalEntries,omitempty"`
	ComparedBytes     int64  `json:"comparedBytes"`
	TotalBytes        *int64 `json:"totalBytes,omitempty"`
	Issues            int    `json:"issues"`
}

type httpStatusCounts struct {
	Added     int `json:"added"`
	Deleted   int `json:"deleted"`
	Modified  int `json:"modified"`
	Unchanged int `json:"unchanged"`
}

type httpSnapshot struct {
	ID                string           `json:"id"`
	BaselineDirectory string           `json:"baselineDirectory"`
	TargetDirectory   string           `json:"targetDirectory"`
	CreatedAt         string           `json:"createdAt"`
	Counts            httpStatusCounts `json:"counts"`
	Issues            int              `json:"issues"`
}

func makeHTTPWorkspaceState(state WorkspaceState) httpWorkspaceState {
	result := httpWorkspaceState{
		Phase:      state.Phase,
		SnapshotID: state.SnapshotID,
		Error:      state.Error,
	}
	if state.Progress != nil {
		result.Progress = &httpWorkspaceProgress{
			DiscoveredEntries: state.Progress.DiscoveredEntries,
			ComparedEntries:   state.Progress.ComparedEntries,
			TotalEntries:      state.Progress.TotalEntries,
			ComparedBytes:     state.Progress.ComparedBytes,
			TotalBytes:        state.Progress.TotalBytes,
			Issues:            state.Progress.Issues,
		}
	}
	return result
}

func makeHTTPSnapshotSummary(summary SnapshotSummary) httpSnapshot {
	return httpSnapshot{
		ID:                summary.ID,
		BaselineDirectory: summary.BaselineDirectory,
		TargetDirectory:   summary.TargetDirectory,
		CreatedAt:         summary.CreatedAt,
		Counts: httpStatusCounts{
			Added:     summary.Counts.Added,
			Deleted:   summary.Counts.Deleted,
			Modified:  summary.Counts.Modified,
			Unchanged: summary.Counts.Unchanged,
		},
		Issues: summary.Issues,
	}
}

func writeHTTPError(writer http.ResponseWriter, status int, code, message string) {
	payload := httpErrorPayload{Version: 1}
	payload.Error.Code = code
	payload.Error.Message = message
	writeHTTPJSON(writer, status, payload)
}

func writeHTTPJSON(writer http.ResponseWriter, status int, value any) {
	setHTTPAPIHeaders(writer.Header())
	writer.Header().Set("Content-Type", "application/json;charset=utf-8")

	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"version":1,"error":{"code":"INTERNAL_ERROR","message":"Internal server error"}}`))
		return
	}
	contents := bytes.TrimSuffix(body.Bytes(), []byte{'\n'})
	writer.WriteHeader(status)
	_, _ = writer.Write(contents)
}

func writeHTTPNoContent(writer http.ResponseWriter) {
	setHTTPAPIHeaders(writer.Header())
	writer.WriteHeader(http.StatusNoContent)
}

func setHTTPAPIHeaders(headers http.Header) {
	headers.Set("Cache-Control", "no-store")
	headers.Set("Content-Security-Policy", diffAPICSP)
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Content-Type-Options", "nosniff")
}
