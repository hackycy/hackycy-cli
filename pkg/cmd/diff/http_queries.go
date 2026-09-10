package diff

import (
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const maxJavaScriptSafeInteger = 9_007_199_254_740_991

func (handler *diffHTTPHandler) serveEntries(writer http.ResponseWriter, request *http.Request) {
	if !requireHTTPMethod(writer, request, http.MethodGet) {
		return
	}
	snapshot, ok := handler.requestSnapshot(writer, request)
	if !ok {
		return
	}

	query := request.URL.Query()
	statuses, err := parseHTTPStatuses(query)
	if err != nil {
		writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid status filter")
		return
	}
	kinds, err := parseHTTPKinds(query)
	if err != nil {
		writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid kind filter")
		return
	}
	limit, hasLimit, err := parseHTTPPositiveInteger(query, "limit")
	if err != nil {
		writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be a positive integer")
		return
	}
	anchor, hasAnchor, err := parseHTTPPositiveInteger(query, "anchor")
	if err != nil {
		writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "anchor must be a positive integer")
		return
	}

	entryQuery := EntryQuery{
		Cursor:           query.Get("cursor"),
		IncludeUnchanged: query.Get("includeUnchanged") == "true",
		Statuses:         statuses,
		Kinds:            kinds,
		Path:             query.Get("path"),
	}
	if hasLimit {
		entryQuery.Limit = limit
	}
	if hasAnchor {
		entryQuery.Anchor = anchor
	}
	page, err := snapshot.List(entryQuery)
	if err != nil {
		writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	entries := make([]httpListItem, 0, len(page.Entries))
	for _, entry := range page.Entries {
		entries = append(entries, makeHTTPListItem(entry))
	}
	writeHTTPJSON(writer, http.StatusOK, httpEntryPage{Entries: entries, NextCursor: page.NextCursor})
}

func (handler *diffHTTPHandler) serveTree(writer http.ResponseWriter, request *http.Request) {
	if !requireHTTPMethod(writer, request, http.MethodGet) {
		return
	}
	snapshot, ok := handler.requestSnapshot(writer, request)
	if !ok {
		return
	}
	page := snapshot.Tree(request.URL.Query().Get("path"))
	children := make([]httpTreeItem, 0, len(page.Children))
	for _, child := range page.Children {
		children = append(children, makeHTTPTreeItem(child))
	}
	writeHTTPJSON(writer, http.StatusOK, httpTreePage{Children: children})
}

func (handler *diffHTTPHandler) serveSearch(writer http.ResponseWriter, request *http.Request) {
	if !requireHTTPMethod(writer, request, http.MethodGet) {
		return
	}
	snapshot, ok := handler.requestSnapshot(writer, request)
	if !ok {
		return
	}
	query := request.URL.Query()
	search := strings.TrimSpace(query.Get("q"))
	if search == "" {
		writeHTTPJSON(writer, http.StatusOK, httpSearchPage{Results: make([]httpTreeItem, 0), Truncated: false})
		return
	}
	statuses, err := parseHTTPStatuses(query)
	if err != nil {
		writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid status filter")
		return
	}
	limit := 200
	if value, present, err := parseHTTPPositiveInteger(query, "limit"); err != nil || (present && value > 200) {
		writeHTTPError(writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be an integer between 1 and 200")
		return
	} else if present {
		limit = value
	}
	page := snapshot.Search(search, statuses, limit)
	results := make([]httpTreeItem, 0, len(page.Results))
	for _, result := range page.Results {
		results = append(results, makeHTTPTreeItem(result))
	}
	writeHTTPJSON(writer, http.StatusOK, httpSearchPage{Results: results, Truncated: page.Truncated})
}

func requireHTTPMethod(writer http.ResponseWriter, request *http.Request, method string) bool {
	if request.Method == method {
		return true
	}
	writeHTTPError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use "+method)
	return false
}

func (handler *diffHTTPHandler) requestSnapshot(writer http.ResponseWriter, request *http.Request) (*Snapshot, bool) {
	id := request.URL.Query().Get("snapshot")
	if id != "" {
		if snapshot := handler.workspace.Snapshot(id); snapshot != nil {
			return snapshot, true
		}
	}
	writeHTTPError(writer, http.StatusConflict, "SNAPSHOT_CHANGED", "The requested Comparison Snapshot is no longer available")
	return nil, false
}

func parseHTTPStatuses(query url.Values) ([]ComparisonStatus, error) {
	values := make([]ComparisonStatus, 0)
	for _, raw := range query["status"] {
		for _, value := range strings.Split(raw, ",") {
			if value == "" {
				continue
			}
			status := ComparisonStatus(value)
			switch status {
			case StatusAdded, StatusDeleted, StatusModified, StatusUnchanged, StatusIssue:
				values = append(values, status)
			default:
				return nil, strconv.ErrSyntax
			}
		}
	}
	return values, nil
}

func parseHTTPKinds(query url.Values) ([]EntryItemKind, error) {
	values := make([]EntryItemKind, 0)
	for _, raw := range query["kind"] {
		for _, value := range strings.Split(raw, ",") {
			if value == "" {
				continue
			}
			kind := EntryItemKind(value)
			switch kind {
			case ItemKindFile, ItemKindSymlink, ItemKindIssue:
				values = append(values, kind)
			default:
				return nil, strconv.ErrSyntax
			}
		}
	}
	return values, nil
}

func parseHTTPPositiveInteger(query url.Values, name string) (int, bool, error) {
	values, present := query[name]
	if !present {
		return 0, false, nil
	}
	raw := ""
	if len(values) > 0 {
		raw = values[0]
	}
	value, ok := parseJavaScriptSafeInteger(raw)
	if !ok || value < 1 {
		return 0, true, strconv.ErrSyntax
	}
	return value, true, nil
}

func parseJavaScriptSafeInteger(raw string) (int, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, true
	}
	number, err := parseJavaScriptNumber(value)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number || number > maxJavaScriptSafeInteger || number < -maxJavaScriptSafeInteger {
		return 0, false
	}
	if strconv.IntSize == 32 && (number > math.MaxInt32 || number < math.MinInt32) {
		return 0, false
	}
	return int(number), true
}

func parseJavaScriptNumber(value string) (float64, error) {
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		parsed, err := strconv.ParseUint(value[2:], 16, 64)
		return float64(parsed), err
	}
	if strings.HasPrefix(value, "0b") || strings.HasPrefix(value, "0B") {
		parsed, err := strconv.ParseUint(value[2:], 2, 64)
		return float64(parsed), err
	}
	if strings.HasPrefix(value, "0o") || strings.HasPrefix(value, "0O") {
		parsed, err := strconv.ParseUint(value[2:], 8, 64)
		return float64(parsed), err
	}
	return strconv.ParseFloat(value, 64)
}

type httpEntryPage struct {
	Entries    []httpListItem `json:"entries"`
	NextCursor string         `json:"nextCursor,omitempty"`
}

type httpListItem struct {
	ID           int              `json:"id"`
	Path         string           `json:"path"`
	Status       ComparisonStatus `json:"status"`
	Kind         EntryItemKind    `json:"kind"`
	BaselineSize *int64           `json:"baselineSize,omitempty"`
	TargetSize   *int64           `json:"targetSize,omitempty"`
	Message      string           `json:"message,omitempty"`
}

func makeHTTPListItem(entry Entry) httpListItem {
	result := httpListItem{ID: entry.ID, Path: entry.Path, Status: entry.Status}
	if entry.Status == StatusIssue {
		result.Kind = ItemKindIssue
		result.Message = entry.Message
		return result
	}
	state := entry.Target
	if state == nil {
		state = entry.Baseline
	}
	if state != nil {
		result.Kind = EntryItemKind(state.Kind)
	}
	if entry.Baseline != nil && entry.Baseline.Kind == EntryKindFile {
		size := entry.Baseline.Size
		result.BaselineSize = &size
	}
	if entry.Target != nil && entry.Target.Kind == EntryKindFile {
		size := entry.Target.Size
		result.TargetSize = &size
	}
	return result
}

type httpTreePage struct {
	Children []httpTreeItem `json:"children"`
}

type httpSearchPage struct {
	Results   []httpTreeItem `json:"results"`
	Truncated bool           `json:"truncated"`
}

type httpTreeItem struct {
	Kind    TreeKind          `json:"kind"`
	Name    string            `json:"name"`
	Path    string            `json:"path"`
	Counts  *httpStatusCounts `json:"counts,omitempty"`
	Issues  *int              `json:"issues,omitempty"`
	ID      int               `json:"id,omitempty"`
	Status  ComparisonStatus  `json:"status,omitempty"`
	Message string            `json:"message,omitempty"`
}

func makeHTTPTreeItem(node TreeNode) httpTreeItem {
	result := httpTreeItem{Kind: node.Kind, Name: node.Name, Path: node.Path}
	if node.Kind == TreeKindDirectory {
		counts := httpStatusCounts{
			Added:     node.Counts.Added,
			Deleted:   node.Counts.Deleted,
			Modified:  node.Counts.Modified,
			Unchanged: node.Counts.Unchanged,
		}
		issues := node.Issues
		result.Counts = &counts
		result.Issues = &issues
		return result
	}
	result.ID = node.ID
	result.Status = node.Status
	result.Message = node.Message
	return result
}
