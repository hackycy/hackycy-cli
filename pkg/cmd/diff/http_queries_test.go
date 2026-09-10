package diff

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestHTTPHandlerServesSnapshotBoundListTreeAndSearchQueries(t *testing.T) {
	workspace, snapshot := httpQueryFixture(t)
	handler := NewHTTPHandler(workspace)
	snapshotID := snapshot.Summary().ID

	list := httpResponse(handler, http.MethodGet, "/api/entries?snapshot="+snapshotID)
	assertHTTPAPIHeaders(t, list)
	listBody := decodeHTTPList(t, list)
	if list.Code != http.StatusOK || !equalStrings(httpEntryPaths(listBody.Entries), []string{"src/added.txt", "src/changed.txt", "src/deleted.txt"}) {
		t.Fatalf("list body = %#v", listBody)
	}
	for _, entry := range listBody.Entries {
		if entry.Kind != "file" || entry.Path == "" || entry.ID < 1 {
			t.Fatalf("list entry = %#v", entry)
		}
	}

	filtered := httpResponse(handler, http.MethodGet, "/api/entries?snapshot="+snapshotID+"&status=added,modified&path=%20SRC%20")
	filteredBody := decodeHTTPList(t, filtered)
	if filtered.Code != http.StatusOK || !equalStrings(httpEntryPaths(filteredBody.Entries), []string{"src/added.txt", "src/changed.txt"}) {
		t.Fatalf("filtered list = %#v", filteredBody)
	}
	unchanged := httpResponse(handler, http.MethodGet, "/api/entries?snapshot="+snapshotID+"&includeUnchanged=true")
	if body := decodeHTTPList(t, unchanged); !equalStrings(httpEntryPaths(body.Entries), []string{"same.txt", "src/added.txt", "src/changed.txt", "src/deleted.txt"}) {
		t.Fatalf("unchanged list = %#v", body)
	}

	changedID := snapshotEntryID(t, snapshot, "src/changed.txt")
	anchored := httpResponse(handler, http.MethodGet, "/api/entries?snapshot="+snapshotID+"&anchor="+strconv.Itoa(changedID)+"&status=modified")
	if body := decodeHTTPList(t, anchored); !equalStrings(httpEntryPaths(body.Entries), []string{"src/changed.txt"}) {
		t.Fatalf("anchored list = %#v", body)
	}
	firstPage := httpResponse(handler, http.MethodGet, "/api/entries?snapshot="+snapshotID+"&limit=1")
	firstPageBody := decodeHTTPList(t, firstPage)
	if firstPageBody.NextCursor == "" || !equalStrings(httpEntryPaths(firstPageBody.Entries), []string{"src/added.txt"}) {
		t.Fatalf("first page = %#v", firstPageBody)
	}
	secondPage := httpResponse(handler, http.MethodGet, "/api/entries?snapshot="+snapshotID+"&limit=1&cursor="+firstPageBody.NextCursor)
	if body := decodeHTTPList(t, secondPage); !equalStrings(httpEntryPaths(body.Entries), []string{"src/changed.txt"}) {
		t.Fatalf("second page = %#v", body)
	}

	tree := httpResponse(handler, http.MethodGet, "/api/tree?snapshot="+snapshotID+"&path=")
	assertHTTPAPIHeaders(t, tree)
	treeBody := decodeHTTPTree(t, tree)
	if tree.Code != http.StatusOK || len(treeBody.Children) != 2 || treeBody.Children[0].Kind != "directory" || treeBody.Children[0].Path != "src" || treeBody.Children[0].Counts == nil || *treeBody.Children[0].Counts != (httpStatusCounts{Added: 1, Deleted: 1, Modified: 1}) || treeBody.Children[0].Issues == nil || *treeBody.Children[0].Issues != 0 || treeBody.Children[1].Path != "same.txt" {
		t.Fatalf("tree body = %#v", treeBody)
	}

	emptyDefaultSearch := httpResponse(handler, http.MethodGet, "/api/search?snapshot="+snapshotID+"&q=src")
	if body := decodeHTTPSearch(t, emptyDefaultSearch); len(body.Results) != 0 || body.Truncated {
		t.Fatalf("default search = %#v", body)
	}
	modifiedSearch := httpResponse(handler, http.MethodGet, "/api/search?snapshot="+snapshotID+"&q=src&status=modified")
	if body := decodeHTTPSearch(t, modifiedSearch); !equalStrings(httpTreePaths(body.Results), []string{"src", "src/changed.txt"}) || body.Truncated {
		t.Fatalf("modified search = %#v", body)
	}
	blankSearch := httpResponse(handler, http.MethodGet, "/api/search?snapshot="+snapshotID+"&q=%20%20&status=not-real&limit=0")
	if body := decodeHTTPSearch(t, blankSearch); blankSearch.Code != http.StatusOK || len(body.Results) != 0 || body.Truncated {
		t.Fatalf("blank search = %#v", body)
	}
}

func TestHTTPHandlerRejectsInvalidSnapshotAndQueryInputs(t *testing.T) {
	workspace, snapshot := httpQueryFixture(t)
	handler := NewHTTPHandler(workspace)
	snapshotID := snapshot.Summary().ID

	for _, path := range []string{
		"/api/entries",
		"/api/tree?snapshot=old",
		"/api/search?snapshot=old&q=value",
	} {
		response := httpResponse(handler, http.MethodGet, path)
		assertHTTPAPIHeaders(t, response)
		assertHTTPAPIError(t, response, http.StatusConflict, "SNAPSHOT_CHANGED", "The requested Comparison Snapshot is no longer available")
	}
	for _, path := range []string{
		"/api/entries?snapshot=" + snapshotID + "&status=not-real",
		"/api/entries?snapshot=" + snapshotID + "&kind=directory",
		"/api/entries?snapshot=" + snapshotID + "&limit=0",
		"/api/entries?snapshot=" + snapshotID + "&anchor=none",
		"/api/entries?snapshot=" + snapshotID + "&cursor=not-a-cursor",
		"/api/search?snapshot=" + snapshotID + "&q=value&status=not-real",
		"/api/search?snapshot=" + snapshotID + "&q=value&limit=201",
	} {
		response := httpResponse(handler, http.MethodGet, path)
		assertHTTPAPIHeaders(t, response)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid request %q status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
	for _, path := range []string{
		"/api/entries?snapshot=" + snapshotID,
		"/api/tree?snapshot=" + snapshotID,
		"/api/search?snapshot=" + snapshotID + "&q=value",
	} {
		response := httpResponse(handler, http.MethodPost, path)
		assertHTTPAPIHeaders(t, response)
		assertHTTPAPIError(t, response, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Use GET")
	}
}

type httpListResponse struct {
	Entries    []httpListEntry `json:"entries"`
	NextCursor string          `json:"nextCursor"`
}

type httpListEntry struct {
	ID           int    `json:"id"`
	Path         string `json:"path"`
	Status       string `json:"status"`
	Kind         string `json:"kind"`
	BaselineSize *int64 `json:"baselineSize"`
	TargetSize   *int64 `json:"targetSize"`
	Message      string `json:"message"`
}

type httpTreeResponse struct {
	Children []httpTreeEntry `json:"children"`
}

type httpSearchResponse struct {
	Results   []httpTreeEntry `json:"results"`
	Truncated bool            `json:"truncated"`
}

type httpTreeEntry struct {
	Kind    string            `json:"kind"`
	Path    string            `json:"path"`
	Counts  *httpStatusCounts `json:"counts"`
	Issues  *int              `json:"issues"`
	ID      int               `json:"id"`
	Status  string            `json:"status"`
	Message string            `json:"message"`
}

func httpQueryFixture(t *testing.T) (*Workspace, *Snapshot) {
	t.Helper()
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "same.txt", "same")
	writeComparisonFile(t, target, "same.txt", "same")
	writeComparisonFile(t, baseline, "src/deleted.txt", "old")
	writeComparisonFile(t, baseline, "src/changed.txt", "before")
	writeComparisonFile(t, target, "src/changed.txt", "after!")
	writeComparisonFile(t, target, "src/added.txt", "new")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	return workspace, refreshWorkspace(t, workspace)
}

func decodeHTTPList(t *testing.T, response *httptest.ResponseRecorder) httpListResponse {
	t.Helper()
	var body httpListResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	return body
}

func decodeHTTPTree(t *testing.T, response *httptest.ResponseRecorder) httpTreeResponse {
	t.Helper()
	var body httpTreeResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode tree response: %v", err)
	}
	return body
}

func decodeHTTPSearch(t *testing.T, response *httptest.ResponseRecorder) httpSearchResponse {
	t.Helper()
	var body httpSearchResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	return body
}

func httpEntryPaths(entries []httpListEntry) []string {
	paths := make([]string, len(entries))
	for index, entry := range entries {
		paths[index] = entry.Path
	}
	return paths
}

func httpTreePaths(entries []httpTreeEntry) []string {
	paths := make([]string, len(entries))
	for index, entry := range entries {
		paths[index] = entry.Path
	}
	return paths
}
