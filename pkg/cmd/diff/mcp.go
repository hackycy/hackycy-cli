package diff

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewMCPHandler creates the command-owned stateless MCP boundary. Its tools
// only observe the fixed Comparison Workspace; callers never supply paths.
func NewMCPHandler(workspace *Workspace, bindingAddress string) http.Handler {
	return newMCPHandler(workspace, bindingAddress, newRefreshCoordinator(workspace))
}

func newMCPHandler(workspace *Workspace, bindingAddress string, refresh *refreshCoordinator) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return newMCPServer(workspace, refresh)
	}, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})

	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !isMCPOriginAllowed(request, bindingAddress) {
			writeMCPOriginError(writer)
			return
		}
		if serveMCPTransportCompatibility(writer, request) {
			return
		}
		streamable.ServeHTTP(&mcpSecurityResponseWriter{ResponseWriter: writer}, request)
	})
}

type mcpSecurityResponseWriter struct {
	http.ResponseWriter
}

func (writer *mcpSecurityResponseWriter) WriteHeader(status int) {
	setHTTPAPIHeaders(writer.Header())
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *mcpSecurityResponseWriter) Write(contents []byte) (int, error) {
	setHTTPAPIHeaders(writer.Header())
	return writer.ResponseWriter.Write(contents)
}

func (writer *mcpSecurityResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func newMCPServer(workspace *Workspace, refresh *refreshCoordinator) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "ycy-directory-diff",
		Version: "1.0.0",
	}, &mcp.ServerOptions{
		GetSessionID: func() string { return "" },
	})
	server.AddReceivingMiddleware(preserveMCPToolOrder)
	openWorld := false
	mcp.AddTool[mcpGetComparisonInput, mcpGetComparisonOutput](server, &mcp.Tool{
		Name:        "get_comparison",
		Description: "Return the diff service phase and current immutable Comparison Snapshot.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: &openWorld,
		},
		OutputSchema: mcpGetComparisonOutputSchema,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ mcpGetComparisonInput) (*mcp.CallToolResult, mcpGetComparisonOutput, error) {
		state := workspace.State()
		output := mcpGetComparisonOutput{Phase: state.Phase, Error: state.Error}
		snapshot := workspace.Snapshot()
		if snapshot == nil {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
				Text: "Comparison " + string(state.Phase) + ": no published snapshot",
			}}}, output, nil
		}

		summary := snapshot.Summary()
		output.Snapshot = &mcpSnapshot{
			SnapshotID:        summary.ID,
			BaselineDirectory: summary.BaselineDirectory,
			TargetDirectory:   summary.TargetDirectory,
			CreatedAt:         summary.CreatedAt,
			Counts:            makeMCPStatusCounts(summary.Counts),
			Issues:            summary.Issues,
		}
		changes := summary.Counts.Added + summary.Counts.Deleted + summary.Counts.Modified
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
			Text: "Comparison " + string(state.Phase) + ": " + mcpCount(changes, "change") + ", " + mcpCount(summary.Issues, "issue"),
		}}}, output, nil
	})
	destructive := false
	mcp.AddTool[mcpRefreshComparisonInput, mcpRefreshComparisonOutput](server, &mcp.Tool{
		Name:        "refresh_comparison",
		Description: "Start an asynchronous Refresh of the fixed Comparison Workspace.",
		Annotations: &mcp.ToolAnnotations{
			DestructiveHint: &destructive,
			OpenWorldHint:   &openWorld,
		},
		OutputSchema: mcpRefreshComparisonOutputSchema,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ mcpRefreshComparisonInput) (*mcp.CallToolResult, mcpRefreshComparisonOutput, error) {
		err := refresh.StartSource("mcp")
		accepted := err == nil
		output := mcpRefreshComparisonOutput{Accepted: accepted, AlreadyRunning: !accepted}
		text := "Refresh accepted"
		if !accepted {
			text = "Refresh already running"
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, output, nil
	})
	mcp.AddTool[mcpListChangesInput, any](server, &mcp.Tool{
		Name:        "list_changes",
		Description: "List changed Comparison Entries from one immutable Comparison Snapshot. Errors include snapshot_changed and invalid_cursor.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: &openWorld,
		},
		InputSchema:  mcpListChangesInputSchema,
		OutputSchema: mcpListChangesOutputSchema,
	}, func(_ context.Context, _ *mcp.CallToolRequest, input mcpListChangesInput) (*mcp.CallToolResult, any, error) {
		snapshot := workspace.Snapshot(input.SnapshotID)
		if snapshot == nil {
			return mcpToolErrorResult("snapshot_changed", "The requested Comparison Snapshot is no longer available"), nil, nil
		}
		statuses := input.Statuses
		if statuses == nil {
			statuses = []ComparisonStatus{StatusAdded, StatusDeleted, StatusModified}
		}
		page, err := snapshot.List(EntryQuery{
			Statuses: statuses,
			Kinds:    input.Kinds,
			Path:     input.Path,
			Cursor:   input.Cursor,
			Limit:    input.Limit,
		})
		if err != nil {
			return mcpToolErrorResult("invalid_cursor", "The cursor is invalid"), nil, nil
		}
		changes := make([]mcpChange, 0, len(page.Entries))
		for _, entry := range page.Entries {
			changes = append(changes, makeMCPChange(entry))
		}
		output := mcpListChangesOutput{Changes: &changes}
		if page.NextCursor != "" {
			nextCursor := page.NextCursor
			output.NextCursor = &nextCursor
		}
		text := "Listed " + strconv.Itoa(len(changes)) + " changed "
		if len(changes) == 1 {
			text += "entry"
		} else {
			text += "entries"
		}
		if page.NextCursor != "" {
			text += "; more available"
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, output, nil
	})
	mcp.AddTool[mcpListIssuesInput, any](server, &mcp.Tool{
		Name:        "list_issues",
		Description: "List Comparison Issues from one immutable Comparison Snapshot. Errors include snapshot_changed and invalid_cursor.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: &openWorld,
		},
		InputSchema:  mcpListIssuesInputSchema,
		OutputSchema: mcpListIssuesOutputSchema,
	}, func(_ context.Context, _ *mcp.CallToolRequest, input mcpListIssuesInput) (*mcp.CallToolResult, any, error) {
		snapshot := workspace.Snapshot(input.SnapshotID)
		if snapshot == nil {
			return mcpToolErrorResult("snapshot_changed", "The requested Comparison Snapshot is no longer available"), nil, nil
		}
		page, err := snapshot.List(EntryQuery{
			Statuses: []ComparisonStatus{StatusIssue},
			Path:     input.Path,
			Cursor:   input.Cursor,
			Limit:    input.Limit,
		})
		if err != nil {
			return mcpToolErrorResult("invalid_cursor", "The cursor is invalid"), nil, nil
		}
		issues := make([]mcpIssue, 0, len(page.Entries))
		for _, entry := range page.Entries {
			issues = append(issues, mcpIssue{Path: entry.Path, Message: entry.Message})
		}
		output := mcpListIssuesOutput{Issues: &issues}
		if page.NextCursor != "" {
			nextCursor := page.NextCursor
			output.NextCursor = &nextCursor
		}
		text := "Listed " + mcpCount(len(issues), "Comparison Issue")
		if page.NextCursor != "" {
			text += "; more available"
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, output, nil
	})
	mcp.AddTool[mcpSearchChangesInput, any](server, &mcp.Tool{
		Name:        "search_changes",
		Description: "Search changed Comparison Paths by case-insensitive substring without reading file content. Returns snapshot_changed when the snapshot was replaced.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: &openWorld,
		},
		InputSchema:  mcpSearchChangesInputSchema,
		OutputSchema: mcpSearchChangesOutputSchema,
	}, func(_ context.Context, _ *mcp.CallToolRequest, input mcpSearchChangesInput) (*mcp.CallToolResult, any, error) {
		snapshot := workspace.Snapshot(input.SnapshotID)
		if snapshot == nil {
			return mcpToolErrorResult("snapshot_changed", "The requested Comparison Snapshot is no longer available"), nil, nil
		}
		statuses := input.Statuses
		if statuses == nil {
			statuses = []ComparisonStatus{StatusAdded, StatusDeleted, StatusModified}
		}
		limit := input.Limit
		if limit == 0 {
			limit = 20
		}
		page, err := snapshot.List(EntryQuery{
			Statuses: statuses,
			Kinds:    input.Kinds,
			Path:     strings.TrimSpace(input.Query),
			Limit:    limit + 1,
		})
		if err != nil {
			return nil, nil, err
		}
		truncated := len(page.Entries) > limit || page.NextCursor != ""
		entries := page.Entries
		if len(entries) > limit {
			entries = entries[:limit]
		}
		changes := make([]mcpChange, 0, len(entries))
		for _, entry := range entries {
			changes = append(changes, makeMCPChange(entry))
		}
		output := mcpSearchChangesOutput{Changes: &changes, Truncated: &truncated}
		text := "Found " + strconv.Itoa(len(changes)) + " changed "
		if len(changes) == 1 {
			text += "entry"
		} else {
			text += "entries"
		}
		if truncated {
			text += "; result truncated"
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, output, nil
	})
	mcp.AddTool[mcpGetTextDiffInput, any](server, &mcp.Tool{
		Name:        "get_text_diff",
		Description: "Generate a bounded analysis-only Unified Diff for one changed text Comparison Entry. Errors include snapshot_changed and entry_not_found.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: &openWorld,
		},
		InputSchema:  mcpGetTextDiffInputSchema,
		OutputSchema: mcpTextDiffOutputSchema,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input mcpGetTextDiffInput) (*mcp.CallToolResult, any, error) {
		snapshot := workspace.Snapshot(input.SnapshotID)
		if snapshot == nil {
			return mcpToolErrorResult("snapshot_changed", "The requested Comparison Snapshot is no longer available"), nil, nil
		}
		result, err := snapshot.TextDiff(ctx, input.EntryID, &TextDiffOptions{ContextLines: input.ContextLines})
		if errors.Is(err, errComparisonEntryNotFound) {
			return mcpToolErrorResult("entry_not_found", "The Comparison Entry does not exist in this snapshot"), nil, nil
		}
		if err != nil {
			return nil, nil, err
		}
		output := makeMCPTextDiff(result)
		text := "Text Difference unavailable for " + output.Path + ": " + string(output.Reason)
		switch output.Status {
		case TextDiffReady:
			text = "Text Difference ready for " + output.Path + ": +" + strconv.Itoa(*output.AddedLines) + " -" + strconv.Itoa(*output.DeletedLines)
		case TextDiffNoTextualChanges:
			text = "No textual changes for " + output.Path
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, output, nil
	})
	return server
}

var mcpToolOrder = map[string]int{
	"get_comparison":     0,
	"refresh_comparison": 1,
	"list_changes":       2,
	"list_issues":        3,
	"search_changes":     4,
	"get_text_diff":      5,
}

func preserveMCPToolOrder(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		result, err := next(ctx, method, request)
		if method != "tools/list" || err != nil {
			return result, err
		}
		list, ok := result.(*mcp.ListToolsResult)
		if !ok {
			return result, nil
		}
		sort.SliceStable(list.Tools, func(left, right int) bool {
			leftOrder, leftKnown := mcpToolOrder[list.Tools[left].Name]
			rightOrder, rightKnown := mcpToolOrder[list.Tools[right].Name]
			if leftKnown && rightKnown {
				return leftOrder < rightOrder
			}
			return leftKnown || (!rightKnown && list.Tools[left].Name < list.Tools[right].Name)
		})
		return list, nil
	}
}

func isMCPOriginAllowed(request *http.Request, bindingAddress string) bool {
	origin := request.Header.Get("Origin")
	if origin == "" {
		return true
	}
	originValue, originHost, ok := canonicalMCPOrigin(origin)
	if !ok || origin != originValue {
		return false
	}

	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	requestOrigin, _, ok := canonicalMCPOrigin(scheme + "://" + request.Host)
	if !ok || originValue != requestOrigin {
		return false
	}
	return isMCPOriginHostAllowed(originHost, bindingAddress)
}

func canonicalMCPOrigin(raw string) (string, string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", "", false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", "", false
	}
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "" {
		return "", "", false
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	port := parsed.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return scheme + "://" + host, hostname, true
}

func isMCPOriginHostAllowed(hostname, bindingAddress string) bool {
	bindingAddress = strings.TrimPrefix(strings.TrimSuffix(bindingAddress, "]"), "[")
	if hostname == "localhost" || strings.EqualFold(hostname, bindingAddress) {
		return true
	}
	return bindingAddress == "0.0.0.0" && net.ParseIP(hostname) != nil
}

func writeMCPOriginError(writer http.ResponseWriter) {
	writeMCPProtocolError(writer, http.StatusForbidden, -32000, "MCP requests must be same-origin")
}

type mcpGetComparisonInput struct{}

type mcpRefreshComparisonInput struct{}

type mcpListChangesInput struct {
	SnapshotID string             `json:"snapshot_id"`
	Statuses   []ComparisonStatus `json:"statuses,omitempty"`
	Kinds      []EntryItemKind    `json:"kinds,omitempty"`
	Path       string             `json:"path,omitempty"`
	Cursor     string             `json:"cursor,omitempty"`
	Limit      int                `json:"limit,omitempty"`
}

type mcpListIssuesInput struct {
	SnapshotID string `json:"snapshot_id"`
	Path       string `json:"path,omitempty"`
	Cursor     string `json:"cursor,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type mcpSearchChangesInput struct {
	SnapshotID string             `json:"snapshot_id"`
	Query      string             `json:"query"`
	Statuses   []ComparisonStatus `json:"statuses,omitempty"`
	Kinds      []EntryItemKind    `json:"kinds,omitempty"`
	Limit      int                `json:"limit,omitempty"`
}

type mcpGetTextDiffInput struct {
	SnapshotID   string `json:"snapshot_id"`
	EntryID      int    `json:"entry_id"`
	ContextLines *int   `json:"context_lines,omitempty"`
}

type mcpGetComparisonOutput struct {
	Phase    WorkspacePhase `json:"phase"`
	Error    string         `json:"error,omitempty"`
	Snapshot *mcpSnapshot   `json:"snapshot,omitempty"`
}

type mcpSnapshot struct {
	SnapshotID        string          `json:"snapshot_id"`
	BaselineDirectory string          `json:"baseline_directory"`
	TargetDirectory   string          `json:"target_directory"`
	CreatedAt         string          `json:"created_at"`
	Counts            mcpStatusCounts `json:"counts"`
	Issues            int             `json:"issues"`
}

type mcpStatusCounts struct {
	Added     int `json:"added"`
	Deleted   int `json:"deleted"`
	Modified  int `json:"modified"`
	Unchanged int `json:"unchanged"`
}

type mcpRefreshComparisonOutput struct {
	Accepted       bool `json:"accepted"`
	AlreadyRunning bool `json:"already_running"`
}

type mcpListChangesOutput struct {
	Changes    *[]mcpChange `json:"changes,omitempty"`
	NextCursor *string      `json:"next_cursor,omitempty"`
}

type mcpListIssuesOutput struct {
	Issues     *[]mcpIssue `json:"issues,omitempty"`
	NextCursor *string     `json:"next_cursor,omitempty"`
}

type mcpSearchChangesOutput struct {
	Changes   *[]mcpChange `json:"changes,omitempty"`
	Truncated *bool        `json:"truncated,omitempty"`
}

type mcpTextDiffOutput struct {
	Status            TextDiffStatus   `json:"status,omitempty"`
	Path              string           `json:"path,omitempty"`
	ComparisonStatus  ComparisonStatus `json:"comparison_status,omitempty"`
	Reason            TextDiffReason   `json:"reason,omitempty"`
	ContextLines      *int             `json:"context_lines,omitempty"`
	BaselineEncoding  *TextEncoding    `json:"baseline_encoding,omitempty"`
	TargetEncoding    *TextEncoding    `json:"target_encoding,omitempty"`
	BaselineSize      *int64           `json:"baseline_size,omitempty"`
	BaselineLineCount *int             `json:"baseline_line_count,omitempty"`
	TargetSize        *int64           `json:"target_size,omitempty"`
	TargetLineCount   *int             `json:"target_line_count,omitempty"`
	AddedLines        *int             `json:"added_lines,omitempty"`
	DeletedLines      *int             `json:"deleted_lines,omitempty"`
	OutputBytes       *int             `json:"output_bytes,omitempty"`
	Patch             *string          `json:"patch,omitempty"`
}

type mcpChange struct {
	EntryID  int                     `json:"entry_id"`
	Path     string                  `json:"path"`
	Status   ComparisonStatus        `json:"status"`
	Baseline *mcpChangeSnapshotState `json:"baseline,omitempty"`
	Target   *mcpChangeSnapshotState `json:"target,omitempty"`
}

type mcpChangeSnapshotState struct {
	Kind       EntryKind `json:"kind"`
	Size       *int64    `json:"size,omitempty"`
	LinkTarget *string   `json:"link_target,omitempty"`
}

type mcpIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type mcpToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var mcpListChangesInputSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"snapshot_id": {
			Type:      "string",
			MinLength: jsonschema.Ptr(1),
		},
		"statuses": {
			Type:     "array",
			MinItems: jsonschema.Ptr(1),
			Items: &jsonschema.Schema{
				Type: "string",
				Enum: []any{string(StatusAdded), string(StatusDeleted), string(StatusModified)},
			},
		},
		"kinds": {
			Type:     "array",
			MinItems: jsonschema.Ptr(1),
			Items: &jsonschema.Schema{
				Type: "string",
				Enum: []any{string(ItemKindFile), string(ItemKindSymlink)},
			},
		},
		"path": {Type: "string"},
		"cursor": {
			Type:      "string",
			MinLength: jsonschema.Ptr(1),
		},
		"limit": {
			Type:    "integer",
			Minimum: jsonschema.Ptr(1.0),
			Maximum: jsonschema.Ptr(500.0),
			Default: json.RawMessage("100"),
		},
	},
	Required: []string{"snapshot_id"},
}

var mcpListIssuesInputSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"snapshot_id": {
			Type:      "string",
			MinLength: jsonschema.Ptr(1),
		},
		"path": {Type: "string"},
		"cursor": {
			Type:      "string",
			MinLength: jsonschema.Ptr(1),
		},
		"limit": {
			Type:    "integer",
			Minimum: jsonschema.Ptr(1.0),
			Maximum: jsonschema.Ptr(500.0),
			Default: json.RawMessage("100"),
		},
	},
	Required: []string{"snapshot_id"},
}

var mcpSearchChangesInputSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"snapshot_id": {
			Type:      "string",
			MinLength: jsonschema.Ptr(1),
		},
		"query": {
			Type:      "string",
			MinLength: jsonschema.Ptr(1),
			Pattern:   `\S`,
		},
		"statuses": {
			Type:     "array",
			MinItems: jsonschema.Ptr(1),
			Items: &jsonschema.Schema{
				Type: "string",
				Enum: []any{string(StatusAdded), string(StatusDeleted), string(StatusModified)},
			},
		},
		"kinds": {
			Type:     "array",
			MinItems: jsonschema.Ptr(1),
			Items: &jsonschema.Schema{
				Type: "string",
				Enum: []any{string(ItemKindFile), string(ItemKindSymlink)},
			},
		},
		"limit": {
			Type:    "integer",
			Minimum: jsonschema.Ptr(1.0),
			Maximum: jsonschema.Ptr(100.0),
			Default: json.RawMessage("20"),
		},
	},
	Required: []string{"snapshot_id", "query"},
}

var mcpGetTextDiffInputSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"snapshot_id": {
			Type:      "string",
			MinLength: jsonschema.Ptr(1),
		},
		"entry_id": {
			Type:    "integer",
			Minimum: jsonschema.Ptr(1.0),
			Maximum: jsonschema.Ptr(9007199254740991.0),
		},
		"context_lines": {
			Type:    "integer",
			Minimum: jsonschema.Ptr(0.0),
			Maximum: jsonschema.Ptr(20.0),
			Default: json.RawMessage("3"),
		},
	},
	Required: []string{"snapshot_id", "entry_id"},
}

var mcpGetComparisonOutputSchema = mcpStrictObjectSchema(map[string]*jsonschema.Schema{
	"phase": mcpStringEnumSchema(
		string(PhaseIdle),
		string(PhaseDiscovering),
		string(PhaseComparing),
		string(PhasePublishing),
		string(PhaseReady),
		string(PhaseCanceled),
		string(PhaseError),
	),
	"error": {Type: "string"},
	"snapshot": mcpStrictObjectSchema(map[string]*jsonschema.Schema{
		"snapshot_id":        {Type: "string"},
		"baseline_directory": {Type: "string"},
		"target_directory":   {Type: "string"},
		"created_at":         {Type: "string"},
		"counts": mcpStrictObjectSchema(map[string]*jsonschema.Schema{
			"added":     mcpNonnegativeIntegerSchema(),
			"deleted":   mcpNonnegativeIntegerSchema(),
			"modified":  mcpNonnegativeIntegerSchema(),
			"unchanged": mcpNonnegativeIntegerSchema(),
		}, "added", "deleted", "modified", "unchanged"),
		"issues": mcpNonnegativeIntegerSchema(),
	}, "snapshot_id", "baseline_directory", "target_directory", "created_at", "counts", "issues"),
}, "phase")

var mcpRefreshComparisonOutputSchema = mcpStrictObjectSchema(map[string]*jsonschema.Schema{
	"accepted":        {Type: "boolean"},
	"already_running": {Type: "boolean"},
}, "accepted", "already_running")

var mcpChangeStateOutputSchema = &jsonschema.Schema{
	Type: "object",
	OneOf: []*jsonschema.Schema{
		mcpStrictObjectSchema(map[string]*jsonschema.Schema{
			"kind": mcpStringEnumSchema(string(EntryKindFile)),
			"size": mcpNonnegativeIntegerSchema(),
		}, "kind", "size"),
		mcpStrictObjectSchema(map[string]*jsonschema.Schema{
			"kind":        mcpStringEnumSchema(string(EntryKindSymlink)),
			"link_target": {Type: "string"},
		}, "kind", "link_target"),
	},
}

var mcpChangeOutputSchema = mcpStrictObjectSchema(map[string]*jsonschema.Schema{
	"entry_id": mcpPositiveSafeIntegerSchema(),
	"path":     {Type: "string"},
	"status":   mcpChangedStatusSchema(),
	"baseline": mcpChangeStateOutputSchema,
	"target":   mcpChangeStateOutputSchema,
}, "entry_id", "path", "status")

var mcpIssueOutputSchema = mcpStrictObjectSchema(map[string]*jsonschema.Schema{
	"path":    {Type: "string"},
	"message": {Type: "string"},
}, "path", "message")

var mcpListChangesOutputSchema = mcpStrictObjectSchema(map[string]*jsonschema.Schema{
	"changes":     mcpArraySchema(mcpChangeOutputSchema),
	"next_cursor": {Type: "string"},
}, "changes")

var mcpListIssuesOutputSchema = mcpStrictObjectSchema(map[string]*jsonschema.Schema{
	"issues":      mcpArraySchema(mcpIssueOutputSchema),
	"next_cursor": {Type: "string"},
}, "issues")

var mcpSearchChangesOutputSchema = mcpStrictObjectSchema(map[string]*jsonschema.Schema{
	"changes":   mcpArraySchema(mcpChangeOutputSchema),
	"truncated": {Type: "boolean"},
}, "changes", "truncated")

var mcpTextDiffOutputSchema = mcpStrictObjectSchema(map[string]*jsonschema.Schema{
	"status": mcpStringEnumSchema(
		string(TextDiffReady),
		string(TextDiffNoTextualChanges),
		string(TextDiffUnavailable),
	),
	"path":              {Type: "string"},
	"comparison_status": mcpChangedStatusSchema(),
	"reason": mcpStringEnumSchema(
		string(TextDiffEncodingOrBOMOnly),
		string(TextDiffNonText),
		string(TextDiffMixedEntryKinds),
		string(TextDiffSourceTooLarge),
		string(TextDiffStale),
		string(TextDiffComplexityLimit),
		string(TextDiffOutputTooLarge),
		string(TextDiffServerBusy),
	),
	"context_lines":       mcpIntegerSchema(0, 20),
	"baseline_encoding":   mcpTextEncodingSchema(),
	"target_encoding":     mcpTextEncodingSchema(),
	"baseline_size":       mcpNonnegativeIntegerSchema(),
	"baseline_line_count": mcpNonnegativeIntegerSchema(),
	"target_size":         mcpNonnegativeIntegerSchema(),
	"target_line_count":   mcpNonnegativeIntegerSchema(),
	"added_lines":         mcpNonnegativeIntegerSchema(),
	"deleted_lines":       mcpNonnegativeIntegerSchema(),
	"output_bytes":        mcpNonnegativeIntegerSchema(),
	"patch":               {Type: "string"},
}, "status", "path", "comparison_status")

func mcpStrictObjectSchema(properties map[string]*jsonschema.Schema, required ...string) *jsonschema.Schema {
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           properties,
		Required:             required,
		AdditionalProperties: &jsonschema.Schema{Not: &jsonschema.Schema{}},
	}
}

func mcpArraySchema(items *jsonschema.Schema) *jsonschema.Schema {
	return &jsonschema.Schema{Type: "array", Items: items}
}

func mcpChangedStatusSchema() *jsonschema.Schema {
	return mcpStringEnumSchema(string(StatusAdded), string(StatusDeleted), string(StatusModified))
}

func mcpTextEncodingSchema() *jsonschema.Schema {
	return mcpStringEnumSchema(string(EncodingUTF8), string(EncodingUTF16LE), string(EncodingUTF16BE))
}

func mcpStringEnumSchema(values ...string) *jsonschema.Schema {
	enum := make([]any, len(values))
	for index, value := range values {
		enum[index] = value
	}
	return &jsonschema.Schema{Type: "string", Enum: enum}
}

func mcpNonnegativeIntegerSchema() *jsonschema.Schema {
	return mcpIntegerSchema(0, 0)
}

func mcpPositiveSafeIntegerSchema() *jsonschema.Schema {
	return mcpIntegerSchema(1, 9007199254740991)
}

func mcpIntegerSchema(minimum, maximum float64) *jsonschema.Schema {
	schema := &jsonschema.Schema{Type: "integer", Minimum: jsonschema.Ptr(minimum)}
	if maximum != 0 {
		schema.Maximum = jsonschema.Ptr(maximum)
	}
	return schema
}

func makeMCPStatusCounts(counts StatusCounts) mcpStatusCounts {
	return mcpStatusCounts{
		Added:     counts.Added,
		Deleted:   counts.Deleted,
		Modified:  counts.Modified,
		Unchanged: counts.Unchanged,
	}
}

func makeMCPChange(entry Entry) mcpChange {
	return mcpChange{
		EntryID:  entry.ID,
		Path:     entry.Path,
		Status:   entry.Status,
		Baseline: makeMCPChangeState(entry.Baseline),
		Target:   makeMCPChangeState(entry.Target),
	}
}

func makeMCPChangeState(state *EntryState) *mcpChangeSnapshotState {
	if state == nil {
		return nil
	}
	result := &mcpChangeSnapshotState{Kind: state.Kind}
	if state.Kind == EntryKindFile {
		size := state.Size
		result.Size = &size
	} else {
		linkTarget := state.LinkTarget
		result.LinkTarget = &linkTarget
	}
	return result
}

func makeMCPTextDiff(result TextDiffResult) mcpTextDiffOutput {
	output := mcpTextDiffOutput{
		Status:            result.Status,
		Path:              result.Path,
		ComparisonStatus:  result.ComparisonStatus,
		Reason:            result.Reason,
		BaselineEncoding:  result.BaselineEncoding,
		TargetEncoding:    result.TargetEncoding,
		BaselineSize:      result.BaselineSize,
		BaselineLineCount: result.BaselineLineCount,
		TargetSize:        result.TargetSize,
		TargetLineCount:   result.TargetLineCount,
	}
	switch result.Status {
	case TextDiffReady:
		output.ContextLines = &result.ContextLines
		output.AddedLines = &result.AddedLines
		output.DeletedLines = &result.DeletedLines
		output.Patch = &result.Patch
	case TextDiffUnavailable:
		if result.Reason == TextDiffOutputTooLarge {
			output.AddedLines = &result.AddedLines
			output.DeletedLines = &result.DeletedLines
			output.OutputBytes = &result.OutputBytes
		}
	}
	return output
}

func mcpToolErrorResult(code, message string) *mcp.CallToolResult {
	structured := struct {
		Error mcpToolError `json:"error"`
	}{Error: mcpToolError{Code: code, Message: message}}
	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: code + ": " + message}},
		StructuredContent: structured,
		IsError:           true,
	}
}

func mcpCount(value int, noun string) string {
	if value == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(value) + " " + noun + "s"
}
