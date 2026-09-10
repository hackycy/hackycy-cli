package diff

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestMCPHandlerServesStatelessComparisonTool(t *testing.T) {
	workspace, summary := mcpWorkspaceFixture(t)
	server := httptest.NewServer(NewMCPHandler(workspace, "127.0.0.1"))
	defer server.Close()

	initialize := postMCPRequest(t, server.URL, server.URL, `{
		"jsonrpc":"2.0",
		"id":1,
		"method":"initialize",
		"params":{
			"protocolVersion":"2025-06-18",
			"capabilities":{},
			"clientInfo":{"name":"diff-mcp-test","version":"1.0.0"}
		}
	}`)
	assertMCPResponseHeaders(t, initialize)
	assertNoMCPSession(t, initialize)
	var initialized mcpRPCResponse
	decodeMCPResponse(t, initialize, &initialized)
	if initialized.JSONRPC != "2.0" || string(initialized.ID) != "1" || initialized.Error != nil {
		t.Fatalf("initialize response = %#v", initialized)
	}
	var initializeResult struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"serverInfo"`
	}
	if err := json.Unmarshal(initialized.Result, &initializeResult); err != nil {
		t.Fatalf("decode initialize result: %v", err)
	}
	if initializeResult.ProtocolVersion != "2025-06-18" || initializeResult.ServerInfo.Name != "ycy-directory-diff" || initializeResult.ServerInfo.Version != "1.0.0" {
		t.Fatalf("initialize result = %#v", initializeResult)
	}

	tools := postMCPRequest(t, server.URL, server.URL, `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	assertMCPResponseHeaders(t, tools)
	assertNoMCPSession(t, tools)
	var listed mcpRPCResponse
	decodeMCPResponse(t, tools, &listed)
	var toolResult struct {
		Tools []struct {
			Name        string `json:"name"`
			Annotations struct {
				ReadOnlyHint  bool `json:"readOnlyHint"`
				OpenWorldHint bool `json:"openWorldHint"`
			} `json:"annotations"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(listed.Result, &toolResult); err != nil {
		t.Fatalf("decode tools result: %v", err)
	}
	if len(toolResult.Tools) != 6 || toolResult.Tools[0].Name != "get_comparison" || !toolResult.Tools[0].Annotations.ReadOnlyHint || toolResult.Tools[0].Annotations.OpenWorldHint || toolResult.Tools[1].Name != "refresh_comparison" || toolResult.Tools[1].Annotations.ReadOnlyHint || toolResult.Tools[1].Annotations.OpenWorldHint || toolResult.Tools[2].Name != "list_changes" || !toolResult.Tools[2].Annotations.ReadOnlyHint || toolResult.Tools[2].Annotations.OpenWorldHint || toolResult.Tools[3].Name != "list_issues" || !toolResult.Tools[3].Annotations.ReadOnlyHint || toolResult.Tools[3].Annotations.OpenWorldHint || toolResult.Tools[4].Name != "search_changes" || !toolResult.Tools[4].Annotations.ReadOnlyHint || toolResult.Tools[4].Annotations.OpenWorldHint || toolResult.Tools[5].Name != "get_text_diff" || !toolResult.Tools[5].Annotations.ReadOnlyHint || toolResult.Tools[5].Annotations.OpenWorldHint {
		t.Fatalf("tools result = %#v", toolResult)
	}

	call := postMCPRequest(t, server.URL, server.URL, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_comparison","arguments":{}}}`)
	assertMCPResponseHeaders(t, call)
	assertNoMCPSession(t, call)
	var called mcpRPCResponse
	decodeMCPResponse(t, call, &called)
	if called.Error != nil {
		t.Fatalf("tool error = %#v", called.Error)
	}
	var comparison mcpComparisonCallResult
	if err := json.Unmarshal(called.Result, &comparison); err != nil {
		t.Fatalf("decode comparison result: %v", err)
	}
	if len(comparison.Content) != 1 || comparison.Content[0].Type != "text" || comparison.Content[0].Text != "Comparison ready: 2 changes, 0 issues" {
		t.Fatalf("comparison text content = %#v", comparison.Content)
	}
	if comparison.StructuredContent.Phase != PhaseReady || comparison.StructuredContent.Error != "" || comparison.StructuredContent.Snapshot == nil {
		t.Fatalf("comparison structured content = %#v", comparison.StructuredContent)
	}
	snapshot := comparison.StructuredContent.Snapshot
	if snapshot.SnapshotID != summary.ID || snapshot.BaselineDirectory != summary.BaselineDirectory || snapshot.TargetDirectory != summary.TargetDirectory || snapshot.CreatedAt != summary.CreatedAt || snapshot.Counts != (StatusCounts{Added: 1, Modified: 1}) || snapshot.Issues != 0 {
		t.Fatalf("comparison snapshot = %#v, want %#v", snapshot, summary)
	}
}

func TestMCPHandlerPreservesStreamableHTTPTransport(t *testing.T) {
	workspace, _ := mcpWorkspaceFixture(t)
	server := httptest.NewServer(NewMCPHandler(workspace, "127.0.0.1"))
	defer server.Close()

	requestBody := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	validHeaders := map[string]string{
		"Accept":       "application/json, text/event-stream",
		"Content-Type": "application/json",
	}

	t.Run("POST requires both response types", func(t *testing.T) {
		response := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodPost, requestBody, map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
		})
		assertMCPTransportError(t, response, http.StatusNotAcceptable, -32000, mcpPostAcceptError)
	})

	t.Run("POST requires JSON content type", func(t *testing.T) {
		response := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodPost, requestBody, map[string]string{
			"Accept": "application/json, text/event-stream",
		})
		assertMCPTransportError(t, response, http.StatusUnsupportedMediaType, -32000, mcpContentTypeError)
	})

	for _, test := range []struct {
		name    string
		body    string
		message string
	}{
		{name: "invalid JSON", body: `{`, message: mcpInvalidJSONError},
		{name: "invalid JSON-RPC message", body: `{"jsonrpc":"2.0","id":1}`, message: mcpInvalidJSONRPCMessageError},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodPost, test.body, validHeaders)
			assertMCPTransportError(t, response, http.StatusBadRequest, -32700, test.message)
		})
	}

	t.Run("unsupported methods are JSON-RPC errors", func(t *testing.T) {
		response := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodPatch, "", nil)
		if response.Header.Get("Allow") != "GET, POST, DELETE" {
			response.Body.Close()
			t.Fatalf("Allow header = %q", response.Header.Get("Allow"))
		}
		assertMCPTransportError(t, response, http.StatusMethodNotAllowed, -32000, mcpUnsupportedHTTPMethodMessage)
	})

	t.Run("stateless DELETE has no session requirement", func(t *testing.T) {
		response := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodDelete, "", nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("DELETE status = %d", response.StatusCode)
		}
		assertMCPHeaders(t, response.Header)
		assertNoMCPSession(t, response)
		if response.Header.Get("Content-Type") != "" {
			t.Fatalf("DELETE Content-Type = %q", response.Header.Get("Content-Type"))
		}
		contents, err := io.ReadAll(response.Body)
		if err != nil || len(contents) != 0 {
			t.Fatalf("DELETE body = %q, read error = %v", contents, err)
		}

		unsupported := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodDelete, "", map[string]string{
			"Mcp-Protocol-Version": "2099-01-01",
		})
		assertMCPTransportError(t, unsupported, http.StatusBadRequest, -32000, "Bad Request: Unsupported protocol version: 2099-01-01 (supported versions: "+mcpLegacyProtocolVersions+")")
	})

	t.Run("protocol versions retain legacy rejection and historical support", func(t *testing.T) {
		unsupported := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodPost, requestBody, map[string]string{
			"Accept":               "application/json, text/event-stream",
			"Content-Type":         "application/json",
			"Mcp-Protocol-Version": "2099-01-01",
		})
		assertMCPTransportError(t, unsupported, http.StatusBadRequest, -32000, "Bad Request: Unsupported protocol version: 2099-01-01 (supported versions: "+mcpLegacyProtocolVersions+")")

		historical := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodPost, requestBody, map[string]string{
			"Accept":               "application/json, text/event-stream",
			"Content-Type":         "application/json",
			"Mcp-Protocol-Version": "2024-10-07",
		})
		if historical.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(historical.Body)
			historical.Body.Close()
			t.Fatalf("historical protocol status = %d, body = %s", historical.StatusCode, body)
		}
		assertMCPHeaders(t, historical.Header)
		assertNoMCPSession(t, historical)
		historical.Body.Close()

		initialize := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodPost, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"version-header-test","version":"1.0.0"}}}`, map[string]string{
			"Accept":               "application/json, text/event-stream",
			"Content-Type":         "application/json",
			"Mcp-Protocol-Version": "2099-01-01",
		})
		assertMCPResponseHeaders(t, initialize)
		assertNoMCPSession(t, initialize)
		initialize.Body.Close()
	})

	t.Run("GET requires event stream support", func(t *testing.T) {
		response := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodGet, "", map[string]string{"Accept": "application/json"})
		assertMCPTransportError(t, response, http.StatusNotAcceptable, -32000, mcpGetAcceptError)
	})

	t.Run("GET opens a stateless event stream", func(t *testing.T) {
		requestContext, cancel := context.WithCancel(context.Background())
		defer cancel()
		request, err := http.NewRequestWithContext(requestContext, http.MethodGet, server.URL, nil)
		if err != nil {
			t.Fatalf("new MCP GET request: %v", err)
		}
		request.Header.Set("Accept", "text/event-stream")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatalf("MCP GET request: %v", err)
		}
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			t.Fatalf("MCP GET status = %d, body = %s", response.StatusCode, body)
		}
		assertMCPHeaders(t, response.Header)
		assertNoMCPSession(t, response)
		if response.Header.Get("Content-Type") != "text/event-stream" {
			response.Body.Close()
			t.Fatalf("MCP GET Content-Type = %q", response.Header.Get("Content-Type"))
		}
		cancel()
		response.Body.Close()
	})
}

func TestMCPHandlerServesIndependentStatelessClients(t *testing.T) {
	workspace, _ := mcpWorkspaceFixture(t)
	server := httptest.NewServer(NewMCPHandler(workspace, "127.0.0.1"))
	defer server.Close()

	for _, id := range []string{"1", "2"} {
		response := rawMCPRequest(t, http.DefaultClient, server.URL, http.MethodPost, `{"jsonrpc":"2.0","id":`+id+`,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"stateless-client-`+id+`","version":"1.0.0"}}}`, map[string]string{
			"Accept":       "application/json, text/event-stream",
			"Content-Type": "application/json",
		})
		assertMCPResponseHeaders(t, response)
		assertNoMCPSession(t, response)
		var initialized mcpRPCResponse
		decodeMCPResponse(t, response, &initialized)
		if initialized.Error != nil || string(initialized.ID) != id {
			t.Fatalf("client %s initialize response = %#v", id, initialized)
		}
	}

	first := mcpToolCall(t, server.URL, "3", "get_comparison", `{}`)
	second := mcpToolCall(t, server.URL, "4", "get_comparison", `{}`)
	assertMCPResponseHeaders(t, first)
	assertNoMCPSession(t, first)
	assertMCPResponseHeaders(t, second)
	assertNoMCPSession(t, second)
	var firstRPC, secondRPC mcpRPCResponse
	decodeMCPResponse(t, first, &firstRPC)
	decodeMCPResponse(t, second, &secondRPC)
	if firstRPC.Error != nil || secondRPC.Error != nil || !bytes.Equal(firstRPC.Result, secondRPC.Result) {
		t.Fatalf("independent stateless results = %#v, %#v", firstRPC, secondRPC)
	}
}

func TestMCPHandlerPublishesDiffToolSchemas(t *testing.T) {
	workspace, _ := mcpWorkspaceFixture(t)
	server := httptest.NewServer(NewMCPHandler(workspace, "127.0.0.1"))
	defer server.Close()

	response := postMCPRequest(t, server.URL, server.URL, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	assertMCPResponseHeaders(t, response)
	assertNoMCPSession(t, response)
	var rpc mcpRPCResponse
	decodeMCPResponse(t, response, &rpc)
	if rpc.Error != nil {
		t.Fatalf("tools/list protocol error = %#v", rpc.Error)
	}

	var result struct {
		Tools []struct {
			Name         string          `json:"name"`
			InputSchema  json.RawMessage `json:"inputSchema"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(rpc.Result, &result); err != nil {
		t.Fatalf("decode tools/list result: %v", err)
	}
	if len(result.Tools) != 6 {
		t.Fatalf("tools/list count = %d, want 6", len(result.Tools))
	}
	for index, name := range []string{
		"get_comparison",
		"refresh_comparison",
		"list_changes",
		"list_issues",
		"search_changes",
		"get_text_diff",
	} {
		if result.Tools[index].Name != name {
			t.Fatalf("tool %d = %q, want %q", index, result.Tools[index].Name, name)
		}
	}

	listChangesInput := mcpSchemaObject(t, result.Tools[2].InputSchema)
	assertMCPSchemaNumber(t, listChangesInput, "limit", float64(1), float64(500), float64(100))
	assertMCPSchemaRequired(t, listChangesInput, "snapshot_id")
	assertMCPSchemaArrayEnum(t, listChangesInput, "statuses", []string{"added", "deleted", "modified"})

	listIssuesInput := mcpSchemaObject(t, result.Tools[3].InputSchema)
	assertMCPSchemaNumber(t, listIssuesInput, "limit", float64(1), float64(500), float64(100))
	assertMCPSchemaRequired(t, listIssuesInput, "snapshot_id")

	searchInput := mcpSchemaObject(t, result.Tools[4].InputSchema)
	assertMCPSchemaNumber(t, searchInput, "limit", float64(1), float64(100), float64(20))
	assertMCPSchemaRequired(t, searchInput, "snapshot_id", "query")
	assertMCPSchemaArrayEnum(t, searchInput, "kinds", []string{"file", "symlink"})

	textDiffInput := mcpSchemaObject(t, result.Tools[5].InputSchema)
	assertMCPSchemaNumber(t, textDiffInput, "entry_id", float64(1), float64(9007199254740991), nil)
	assertMCPSchemaNumber(t, textDiffInput, "context_lines", float64(0), float64(20), float64(3))
	assertMCPSchemaRequired(t, textDiffInput, "snapshot_id", "entry_id")

	comparisonOutput := mcpSchemaObject(t, result.Tools[0].OutputSchema)
	assertMCPSchemaRequired(t, comparisonOutput, "phase")
	comparisonProperties := mcpSchemaProperties(t, comparisonOutput)
	snapshot, ok := comparisonProperties["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("get_comparison snapshot schema = %#v", comparisonProperties["snapshot"])
	}
	counts, ok := mcpSchemaProperties(t, snapshot)["counts"].(map[string]any)
	if !ok {
		t.Fatalf("get_comparison counts schema = %#v", mcpSchemaProperties(t, snapshot)["counts"])
	}
	assertMCPSchemaRequired(t, counts, "added", "deleted", "modified", "unchanged")

	searchOutput := mcpSchemaObject(t, result.Tools[4].OutputSchema)
	assertMCPSchemaRequired(t, searchOutput, "changes", "truncated")

	textDiffOutput := mcpSchemaObject(t, result.Tools[5].OutputSchema)
	assertMCPSchemaRequired(t, textDiffOutput, "status", "path", "comparison_status")
	textDiffProperties := mcpSchemaProperties(t, textDiffOutput)
	assertMCPSchemaEnum(t, textDiffProperties["status"], []string{"ready", "no_textual_changes", "unavailable"})
	assertMCPSchemaEnum(t, textDiffProperties["comparison_status"], []string{"added", "deleted", "modified"})

	comparison := mcpToolCall(t, server.URL, "2", "get_comparison", `{}`)
	assertMCPResponseHeaders(t, comparison)
	var comparisonRPC mcpRPCResponse
	decodeMCPResponse(t, comparison, &comparisonRPC)
	var comparisonResult struct {
		StructuredContent map[string]any `json:"structuredContent"`
	}
	if err := json.Unmarshal(comparisonRPC.Result, &comparisonResult); err != nil {
		t.Fatalf("decode get_comparison result: %v", err)
	}
	snapshotResult, ok := comparisonResult.StructuredContent["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("get_comparison structured snapshot = %#v", comparisonResult.StructuredContent)
	}
	countResult, ok := snapshotResult["counts"].(map[string]any)
	if !ok || len(countResult) != 4 {
		t.Fatalf("get_comparison structured counts = %#v", snapshotResult["counts"])
	}
	for _, name := range []string{"added", "deleted", "modified", "unchanged"} {
		if _, ok := countResult[name]; !ok {
			t.Fatalf("get_comparison counts = %#v, missing %q", countResult, name)
		}
	}
}

func TestMCPHandlerChecksOriginWithoutCORS(t *testing.T) {
	workspace, _ := mcpWorkspaceFixture(t)
	requestBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"diff-mcp-test","version":"1.0.0"}}}`

	tests := []struct {
		name           string
		bindingAddress string
		host           string
		origin         string
		wantStatus     int
	}{
		{name: "originless", bindingAddress: "127.0.0.1", host: "127.0.0.1:3311", wantStatus: http.StatusOK},
		{name: "exact loopback binding", bindingAddress: "127.0.0.1", host: "127.0.0.1:3311", origin: "http://127.0.0.1:3311", wantStatus: http.StatusOK},
		{name: "localhost", bindingAddress: "127.0.0.1", host: "localhost:3311", origin: "http://localhost:3311", wantStatus: http.StatusOK},
		{name: "public literal IPv6", bindingAddress: "0.0.0.0", host: "[::1]:3311", origin: "http://[::1]:3311", wantStatus: http.StatusOK},
		{name: "cross origin", bindingAddress: "127.0.0.1", host: "127.0.0.1:3311", origin: "https://attacker.example", wantStatus: http.StatusForbidden},
		{name: "disallowed same origin host", bindingAddress: "127.0.0.1", host: "attacker.example:3311", origin: "http://attacker.example:3311", wantStatus: http.StatusForbidden},
		{name: "noncanonical default port", bindingAddress: "127.0.0.1", host: "127.0.0.1:80", origin: "http://127.0.0.1:80", wantStatus: http.StatusForbidden},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://"+test.host+"/mcp", bytes.NewBufferString(requestBody))
			request.Host = test.host
			request.Header.Set("Accept", "application/json, text/event-stream")
			request.Header.Set("Content-Type", "application/json")
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			response := httptest.NewRecorder()
			NewMCPHandler(workspace, test.bindingAddress).ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("response status = %d, want %d; body = %s", response.Code, test.wantStatus, response.Body.String())
			}
			assertMCPRecorderHeaders(t, response)
			if response.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatalf("response unexpectedly enables CORS: %v", response.Header())
			}
			if test.wantStatus == http.StatusForbidden {
				var denied mcpRPCResponse
				if err := json.Unmarshal(response.Body.Bytes(), &denied); err != nil {
					t.Fatalf("decode forbidden MCP response: %v", err)
				}
				if denied.JSONRPC != "2.0" || string(denied.ID) != "null" || denied.Error == nil || denied.Error.Code != -32000 || denied.Error.Message != "MCP requests must be same-origin" {
					t.Fatalf("forbidden response = %#v", denied)
				}
			}
		})
	}
}

func TestMCPHandlerStartsOneAsynchronousRefresh(t *testing.T) {
	workspace, previous := mcpWorkspaceFixture(t)
	server := httptest.NewServer(NewMCPHandler(workspace, "127.0.0.1"))
	defer server.Close()

	comparing := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unsubscribe := workspace.Subscribe(func(state WorkspaceState) {
		if state.Phase == PhaseComparing {
			select {
			case <-comparing:
			default:
				close(comparing)
				<-release
			}
		}
	})
	t.Cleanup(unsubscribe)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })

	first := postMCPRequest(t, server.URL, server.URL, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"refresh_comparison","arguments":{}}}`)
	assertMCPResponseHeaders(t, first)
	assertNoMCPSession(t, first)
	var firstResponse mcpRPCResponse
	decodeMCPResponse(t, first, &firstResponse)
	assertMCPRefreshResult(t, firstResponse, "Refresh accepted", true, false)

	select {
	case <-comparing:
	case <-time.After(time.Second):
		t.Fatal("MCP refresh did not reach comparing")
	}
	if snapshot := workspace.Snapshot(); snapshot == nil || snapshot.Summary().ID != previous.ID {
		t.Fatalf("refresh replaced the published snapshot before completion: %#v", snapshot)
	}

	second := postMCPRequest(t, server.URL, server.URL, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"refresh_comparison","arguments":{}}}`)
	assertMCPResponseHeaders(t, second)
	assertNoMCPSession(t, second)
	var secondResponse mcpRPCResponse
	decodeMCPResponse(t, second, &secondResponse)
	assertMCPRefreshResult(t, secondResponse, "Refresh already running", false, true)

	releaseOnce.Do(func() { close(release) })
	waitForWorkspacePhase(t, workspace, PhaseReady)
	if snapshot := workspace.Snapshot(); snapshot == nil || snapshot.Summary().ID == previous.ID {
		t.Fatalf("completed MCP refresh did not publish a replacement snapshot: %#v", snapshot)
	}
}

func TestMCPHandlerListsSnapshotBoundChanges(t *testing.T) {
	workspace, summary := mcpWorkspaceFixture(t)
	server := httptest.NewServer(NewMCPHandler(workspace, "127.0.0.1"))
	defer server.Close()

	first := mcpToolCall(t, server.URL, "1", "list_changes", `{"snapshot_id":`+jsonString(summary.ID)+`,"limit":1}`)
	assertMCPResponseHeaders(t, first)
	var firstResponse mcpRPCResponse
	decodeMCPResponse(t, first, &firstResponse)
	firstPage := decodeMCPChanges(t, firstResponse)
	if len(firstPage.Changes) != 1 || firstPage.Changes[0].Path != "added.txt" || firstPage.Changes[0].Status != StatusAdded || firstPage.Changes[0].Baseline != nil || firstPage.Changes[0].Target == nil || firstPage.Changes[0].Target.Kind != EntryKindFile || firstPage.Changes[0].Target.Size != 6 || firstPage.NextCursor == "" {
		t.Fatalf("first MCP change page = %#v", firstPage)
	}
	if firstPage.Content != "Listed 1 changed entry; more available" {
		t.Fatalf("first MCP change text = %q", firstPage.Content)
	}

	defaultPageResponse := mcpToolCall(t, server.URL, "6", "list_changes", `{"snapshot_id":`+jsonString(summary.ID)+`}`)
	assertMCPResponseHeaders(t, defaultPageResponse)
	var defaultPageRPC mcpRPCResponse
	decodeMCPResponse(t, defaultPageResponse, &defaultPageRPC)
	defaultPage := decodeMCPChanges(t, defaultPageRPC)
	if len(defaultPage.Changes) != 2 || defaultPage.NextCursor != "" || defaultPage.Content != "Listed 2 changed entries" {
		t.Fatalf("default MCP change page = %#v", defaultPage)
	}

	second := mcpToolCall(t, server.URL, "2", "list_changes", `{"snapshot_id":`+jsonString(summary.ID)+`,"limit":1,"cursor":`+jsonString(firstPage.NextCursor)+`}`)
	assertMCPResponseHeaders(t, second)
	var secondResponse mcpRPCResponse
	decodeMCPResponse(t, second, &secondResponse)
	secondPage := decodeMCPChanges(t, secondResponse)
	if len(secondPage.Changes) != 1 || secondPage.Changes[0].Path != "changed.txt" || secondPage.Changes[0].Status != StatusModified || secondPage.Changes[0].Baseline == nil || secondPage.Changes[0].Baseline.Size != 7 || secondPage.Changes[0].Target == nil || secondPage.Changes[0].Target.Size != 6 || secondPage.NextCursor != "" || secondPage.Content != "Listed 1 changed entry" {
		t.Fatalf("second MCP change page = %#v", secondPage)
	}

	filtered := mcpToolCall(t, server.URL, "3", "list_changes", `{"snapshot_id":`+jsonString(summary.ID)+`,"statuses":["modified"],"kinds":["file"],"path":"CHANGED"}`)
	assertMCPResponseHeaders(t, filtered)
	var filteredResponse mcpRPCResponse
	decodeMCPResponse(t, filtered, &filteredResponse)
	filteredPage := decodeMCPChanges(t, filteredResponse)
	if len(filteredPage.Changes) != 1 || filteredPage.Changes[0].Path != "changed.txt" || filteredPage.Content != "Listed 1 changed entry" {
		t.Fatalf("filtered MCP change page = %#v", filteredPage)
	}

	for _, test := range []struct {
		name      string
		arguments string
		code      string
		message   string
	}{
		{name: "stale snapshot", arguments: `{"snapshot_id":"replaced"}`, code: "snapshot_changed", message: "The requested Comparison Snapshot is no longer available"},
		{name: "invalid cursor", arguments: `{"snapshot_id":` + jsonString(summary.ID) + `,"cursor":"not-a-cursor"}`, code: "invalid_cursor", message: "The cursor is invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := mcpToolCall(t, server.URL, "4", "list_changes", test.arguments)
			assertMCPResponseHeaders(t, response)
			var rpc mcpRPCResponse
			decodeMCPResponse(t, response, &rpc)
			assertMCPToolError(t, rpc, test.code, test.message)
		})
	}

	for _, arguments := range []string{
		`{"snapshot_id":` + jsonString(summary.ID) + `,"limit":501}`,
		`{"snapshot_id":` + jsonString(summary.ID) + `,"statuses":[]}`,
		`{"snapshot_id":` + jsonString(summary.ID) + `,"kinds":["issue"]}`,
		`{"snapshot_id":` + jsonString(summary.ID) + `,"cursor":""}`,
	} {
		invalidInput := mcpToolCall(t, server.URL, "5", "list_changes", arguments)
		assertMCPResponseHeaders(t, invalidInput)
		var invalidInputResponse mcpRPCResponse
		decodeMCPResponse(t, invalidInput, &invalidInputResponse)
		if invalidInputResponse.Error == nil || invalidInputResponse.Error.Code != -32602 {
			t.Fatalf("invalid list_changes input response = %#v", invalidInputResponse)
		}
	}
}

func TestMCPHandlerSearchesSnapshotBoundChanges(t *testing.T) {
	workspace, summary := mcpWorkspaceFixture(t)
	server := httptest.NewServer(NewMCPHandler(workspace, "127.0.0.1"))
	defer server.Close()

	firstResponse := mcpToolCall(t, server.URL, "1", "search_changes", `{"snapshot_id":`+jsonString(summary.ID)+`,"query":" .TXT ","limit":1}`)
	assertMCPResponseHeaders(t, firstResponse)
	assertNoMCPSession(t, firstResponse)
	var firstRPC mcpRPCResponse
	decodeMCPResponse(t, firstResponse, &firstRPC)
	first := decodeMCPSearchChanges(t, firstRPC)
	if len(first.Changes) != 1 || first.Changes[0].Path != "added.txt" || !first.Truncated || first.Content != "Found 1 changed entry; result truncated" {
		t.Fatalf("first MCP search page = %#v", first)
	}

	defaultResponse := mcpToolCall(t, server.URL, "2", "search_changes", `{"snapshot_id":`+jsonString(summary.ID)+`,"query":"txt"}`)
	assertMCPResponseHeaders(t, defaultResponse)
	var defaultRPC mcpRPCResponse
	decodeMCPResponse(t, defaultResponse, &defaultRPC)
	all := decodeMCPSearchChanges(t, defaultRPC)
	if len(all.Changes) != 2 || all.Changes[0].Path != "added.txt" || all.Changes[1].Path != "changed.txt" || all.Truncated || all.Content != "Found 2 changed entries" {
		t.Fatalf("default MCP search page = %#v", all)
	}

	filteredResponse := mcpToolCall(t, server.URL, "3", "search_changes", `{"snapshot_id":`+jsonString(summary.ID)+`,"query":"CHANGED","statuses":["modified"],"kinds":["file"]}`)
	assertMCPResponseHeaders(t, filteredResponse)
	var filteredRPC mcpRPCResponse
	decodeMCPResponse(t, filteredResponse, &filteredRPC)
	filtered := decodeMCPSearchChanges(t, filteredRPC)
	if len(filtered.Changes) != 1 || filtered.Changes[0].Path != "changed.txt" || filtered.Truncated || filtered.Content != "Found 1 changed entry" {
		t.Fatalf("filtered MCP search page = %#v", filtered)
	}

	staleResponse := mcpToolCall(t, server.URL, "4", "search_changes", `{"snapshot_id":"replaced","query":"txt"}`)
	assertMCPResponseHeaders(t, staleResponse)
	var staleRPC mcpRPCResponse
	decodeMCPResponse(t, staleResponse, &staleRPC)
	assertMCPToolError(t, staleRPC, "snapshot_changed", "The requested Comparison Snapshot is no longer available")

	for _, arguments := range []string{
		`{"snapshot_id":` + jsonString(summary.ID) + `,"query":"txt","limit":101}`,
		`{"snapshot_id":` + jsonString(summary.ID) + `,"query":"txt","statuses":[]}`,
		`{"snapshot_id":` + jsonString(summary.ID) + `,"query":"txt","kinds":["issue"]}`,
		`{"snapshot_id":` + jsonString(summary.ID) + `,"query":" "}`,
	} {
		response := mcpToolCall(t, server.URL, "5", "search_changes", arguments)
		assertMCPResponseHeaders(t, response)
		var rpc mcpRPCResponse
		decodeMCPResponse(t, response, &rpc)
		if rpc.Error == nil || rpc.Error.Code != -32602 {
			t.Fatalf("invalid search_changes input response = %#v", rpc)
		}
	}
}

func TestMCPHandlerGetsSnapshotBoundTextDiff(t *testing.T) {
	workspace, summary, ids := mcpTextDiffWorkspaceFixture(t)
	server := httptest.NewServer(NewMCPHandler(workspace, "127.0.0.1"))
	defer server.Close()

	readyResponse := mcpToolCall(t, server.URL, "1", "get_text_diff", `{"snapshot_id":`+jsonString(summary.ID)+`,"entry_id":`+strconv.Itoa(ids["changed.txt"])+`,"context_lines":0}`)
	assertMCPResponseHeaders(t, readyResponse)
	assertNoMCPSession(t, readyResponse)
	var readyRPC mcpRPCResponse
	decodeMCPResponse(t, readyResponse, &readyRPC)
	ready := decodeMCPTextDiff(t, readyRPC)
	if ready.Content != "Text Difference ready for changed.txt: +1 -1" || ready.Output.Status != TextDiffReady || ready.Output.Path != "changed.txt" || ready.Output.ComparisonStatus != StatusModified || ready.Output.ContextLines == nil || *ready.Output.ContextLines != 0 || ready.Output.BaselineEncoding == nil || *ready.Output.BaselineEncoding != EncodingUTF8 || ready.Output.TargetEncoding == nil || *ready.Output.TargetEncoding != EncodingUTF8 || ready.Output.AddedLines == nil || *ready.Output.AddedLines != 1 || ready.Output.DeletedLines == nil || *ready.Output.DeletedLines != 1 || ready.Output.Patch == nil || *ready.Output.Patch != "--- baseline\n+++ target\n@@ -1 +1 @@\n-before\n+after\n" || ready.Output.Reason != "" {
		t.Fatalf("ready MCP text diff = %#v", ready)
	}

	defaultResponse := mcpToolCall(t, server.URL, "6", "get_text_diff", `{"snapshot_id":`+jsonString(summary.ID)+`,"entry_id":`+strconv.Itoa(ids["changed.txt"])+`}`)
	assertMCPResponseHeaders(t, defaultResponse)
	var defaultRPC mcpRPCResponse
	decodeMCPResponse(t, defaultResponse, &defaultRPC)
	defaultResult := decodeMCPTextDiff(t, defaultRPC)
	if defaultResult.Output.Status != TextDiffReady || defaultResult.Output.ContextLines == nil || *defaultResult.Output.ContextLines != 3 {
		t.Fatalf("default-context MCP text diff = %#v", defaultResult)
	}

	noChangesResponse := mcpToolCall(t, server.URL, "2", "get_text_diff", `{"snapshot_id":`+jsonString(summary.ID)+`,"entry_id":`+strconv.Itoa(ids["encoding.txt"])+`}`)
	assertMCPResponseHeaders(t, noChangesResponse)
	var noChangesRPC mcpRPCResponse
	decodeMCPResponse(t, noChangesResponse, &noChangesRPC)
	noChanges := decodeMCPTextDiff(t, noChangesRPC)
	if noChanges.Content != "No textual changes for encoding.txt" || noChanges.Output.Status != TextDiffNoTextualChanges || noChanges.Output.Reason != TextDiffEncodingOrBOMOnly || noChanges.Output.BaselineEncoding == nil || *noChanges.Output.BaselineEncoding != EncodingUTF8 || noChanges.Output.TargetEncoding == nil || *noChanges.Output.TargetEncoding != EncodingUTF16LE || noChanges.Output.ContextLines != nil || noChanges.Output.Patch != nil {
		t.Fatalf("no-change MCP text diff = %#v", noChanges)
	}

	unavailableResponse := mcpToolCall(t, server.URL, "3", "get_text_diff", `{"snapshot_id":`+jsonString(summary.ID)+`,"entry_id":`+strconv.Itoa(ids["binary.bin"])+`}`)
	assertMCPResponseHeaders(t, unavailableResponse)
	var unavailableRPC mcpRPCResponse
	decodeMCPResponse(t, unavailableResponse, &unavailableRPC)
	unavailable := decodeMCPTextDiff(t, unavailableRPC)
	if unavailable.Content != "Text Difference unavailable for binary.bin: non_text" || unavailable.Output.Status != TextDiffUnavailable || unavailable.Output.Reason != TextDiffNonText || unavailable.Output.ContextLines != nil || unavailable.Output.Patch != nil {
		t.Fatalf("unavailable MCP text diff = %#v", unavailable)
	}

	for _, test := range []struct {
		name      string
		arguments string
		code      string
		message   string
	}{
		{name: "stale snapshot", arguments: `{"snapshot_id":"replaced","entry_id":1}`, code: "snapshot_changed", message: "The requested Comparison Snapshot is no longer available"},
		{name: "unchanged entry", arguments: `{"snapshot_id":` + jsonString(summary.ID) + `,"entry_id":` + strconv.Itoa(ids["unchanged.txt"]) + `}`, code: "entry_not_found", message: "The Comparison Entry does not exist in this snapshot"},
		{name: "unknown entry", arguments: `{"snapshot_id":` + jsonString(summary.ID) + `,"entry_id":99999}`, code: "entry_not_found", message: "The Comparison Entry does not exist in this snapshot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := mcpToolCall(t, server.URL, "4", "get_text_diff", test.arguments)
			assertMCPResponseHeaders(t, response)
			var rpc mcpRPCResponse
			decodeMCPResponse(t, response, &rpc)
			assertMCPToolError(t, rpc, test.code, test.message)
		})
	}

	for _, arguments := range []string{
		`{"snapshot_id":` + jsonString(summary.ID) + `,"entry_id":0}`,
		`{"snapshot_id":` + jsonString(summary.ID) + `,"entry_id":9007199254740992}`,
		`{"snapshot_id":` + jsonString(summary.ID) + `,"entry_id":` + strconv.Itoa(ids["changed.txt"]) + `,"context_lines":21}`,
	} {
		response := mcpToolCall(t, server.URL, "5", "get_text_diff", arguments)
		assertMCPResponseHeaders(t, response)
		var rpc mcpRPCResponse
		decodeMCPResponse(t, response, &rpc)
		if rpc.Error == nil || rpc.Error.Code != -32602 {
			t.Fatalf("invalid get_text_diff input response = %#v", rpc)
		}
	}
}

type mcpRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *mcpRPCError    `json:"error"`
}

type mcpRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpComparisonCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent struct {
		Phase    WorkspacePhase `json:"phase"`
		Error    string         `json:"error"`
		Snapshot *struct {
			SnapshotID        string       `json:"snapshot_id"`
			BaselineDirectory string       `json:"baseline_directory"`
			TargetDirectory   string       `json:"target_directory"`
			CreatedAt         string       `json:"created_at"`
			Counts            StatusCounts `json:"counts"`
			Issues            int          `json:"issues"`
		} `json:"snapshot"`
	} `json:"structuredContent"`
}

func mcpWorkspaceFixture(t *testing.T) (*Workspace, SnapshotSummary) {
	t.Helper()
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "changed.txt", "before\n")
	writeComparisonFile(t, target, "changed.txt", "after\n")
	writeComparisonFile(t, target, "added.txt", "added\n")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	return workspace, snapshot.Summary()
}

func mcpTextDiffWorkspaceFixture(t *testing.T) (*Workspace, SnapshotSummary, map[string]int) {
	t.Helper()
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "changed.txt", "before\n")
	writeComparisonFile(t, target, "changed.txt", "after\n")
	writeComparisonFile(t, baseline, "encoding.txt", "hello\n")
	writeComparisonBytes(t, target, "encoding.txt", []byte{0xff, 0xfe, 'h', 0, 'e', 0, 'l', 0, 'l', 0, 'o', 0, '\n', 0})
	writeComparisonBytes(t, baseline, "binary.bin", []byte{0xc3, 0x28})
	writeComparisonBytes(t, target, "binary.bin", []byte{0xc3, 0x29})
	writeComparisonFile(t, baseline, "unchanged.txt", "same\n")
	writeComparisonFile(t, target, "unchanged.txt", "same\n")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	snapshot := refreshWorkspace(t, workspace)
	ids := make(map[string]int, len(snapshot.entries))
	for _, entry := range snapshot.entries {
		ids[entry.Path] = entry.ID
	}
	if ids["changed.txt"] == 0 || ids["encoding.txt"] == 0 || ids["binary.bin"] == 0 || ids["unchanged.txt"] == 0 {
		t.Fatalf("text-diff snapshot IDs = %#v", ids)
	}
	return workspace, snapshot.Summary(), ids
}

func postMCPRequest(t *testing.T, endpoint, origin, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new MCP request: %v", err)
	}
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", origin)
	if !bytes.Contains([]byte(body), []byte(`"method":"initialize"`)) {
		request.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("MCP request: %v", err)
	}
	return response
}

func rawMCPRequest(t *testing.T, client *http.Client, endpoint, method, body string, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(method, endpoint, bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("new raw MCP request: %v", err)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("raw MCP request: %v", err)
	}
	return response
}

func mcpToolCall(t *testing.T, endpoint, id, name, arguments string) *http.Response {
	t.Helper()
	return postMCPRequest(t, endpoint, endpoint, `{"jsonrpc":"2.0","id":`+id+`,"method":"tools/call","params":{"name":`+jsonString(name)+`,"arguments":`+arguments+`}}`)
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func decodeMCPResponse(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	contents, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read MCP response: %v", err)
	}
	if err := json.Unmarshal(contents, target); err != nil {
		t.Fatalf("decode MCP response %q: %v", contents, err)
	}
}

func assertMCPResponseHeaders(t *testing.T, response *http.Response) {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		t.Fatalf("MCP response status = %d, body = %s", response.StatusCode, body)
	}
	assertMCPHeaders(t, response.Header)
}

func assertMCPRecorderHeaders(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	assertMCPHeaders(t, response.Header())
}

func assertMCPHeaders(t *testing.T, headers http.Header) {
	t.Helper()
	if headers.Get("Cache-Control") != "no-store" || headers.Get("Content-Security-Policy") != diffAPICSP || headers.Get("Referrer-Policy") != "no-referrer" || headers.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("MCP security headers = %v", headers)
	}
}

func assertNoMCPSession(t *testing.T, response *http.Response) {
	t.Helper()
	if sessionID := response.Header.Get("Mcp-Session-Id"); sessionID != "" {
		_ = response.Body.Close()
		t.Fatalf("MCP response unexpectedly has session ID %q", sessionID)
	}
}

func assertMCPTransportError(t *testing.T, response *http.Response, status, code int, message string) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != status {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("MCP transport status = %d, want %d; body = %s", response.StatusCode, status, body)
	}
	assertMCPHeaders(t, response.Header)
	if response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("MCP transport Content-Type = %q", response.Header.Get("Content-Type"))
	}
	var payload mcpRPCResponse
	decodeMCPResponse(t, response, &payload)
	if payload.JSONRPC != "2.0" || string(payload.ID) != "null" || payload.Error == nil || payload.Error.Code != code || payload.Error.Message != message {
		t.Fatalf("MCP transport error = %#v", payload)
	}
}

func assertMCPRefreshResult(t *testing.T, response mcpRPCResponse, text string, accepted, alreadyRunning bool) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("refresh tool error = %#v", response.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			Accepted       bool `json:"accepted"`
			AlreadyRunning bool `json:"already_running"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode refresh result: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text != text || result.StructuredContent.Accepted != accepted || result.StructuredContent.AlreadyRunning != alreadyRunning {
		t.Fatalf("refresh result = %#v", result)
	}
}

type mcpChangePage struct {
	Content    string
	Changes    []mcpChangeResult
	NextCursor string
}

type mcpChangeResult struct {
	EntryID  int              `json:"entry_id"`
	Path     string           `json:"path"`
	Status   ComparisonStatus `json:"status"`
	Baseline *mcpChangeState  `json:"baseline"`
	Target   *mcpChangeState  `json:"target"`
}

type mcpChangeState struct {
	Kind       EntryKind `json:"kind"`
	Size       int64     `json:"size"`
	LinkTarget string    `json:"link_target"`
}

func decodeMCPChanges(t *testing.T, response mcpRPCResponse) mcpChangePage {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("list_changes protocol error = %#v", response.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			Changes    []mcpChangeResult `json:"changes"`
			NextCursor string            `json:"next_cursor"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode list_changes response: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("list_changes content = %#v", result.Content)
	}
	return mcpChangePage{
		Content:    result.Content[0].Text,
		Changes:    result.StructuredContent.Changes,
		NextCursor: result.StructuredContent.NextCursor,
	}
}

type mcpSearchChangesPage struct {
	Content   string
	Changes   []mcpChangeResult
	Truncated bool
}

type mcpTextDiffCall struct {
	Content string
	Output  mcpTextDiffOutput
}

func decodeMCPTextDiff(t *testing.T, response mcpRPCResponse) mcpTextDiffCall {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("get_text_diff protocol error = %#v", response.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent mcpTextDiffOutput `json:"structuredContent"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode get_text_diff response: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("get_text_diff content = %#v", result.Content)
	}
	return mcpTextDiffCall{Content: result.Content[0].Text, Output: result.StructuredContent}
}

func decodeMCPSearchChanges(t *testing.T, response mcpRPCResponse) mcpSearchChangesPage {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("search_changes protocol error = %#v", response.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			Changes   []mcpChangeResult `json:"changes"`
			Truncated bool              `json:"truncated"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode search_changes response: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("search_changes content = %#v", result.Content)
	}
	return mcpSearchChangesPage{
		Content:   result.Content[0].Text,
		Changes:   result.StructuredContent.Changes,
		Truncated: result.StructuredContent.Truncated,
	}
}

func assertMCPToolError(t *testing.T, response mcpRPCResponse, code, message string) {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("tool protocol error = %#v", response.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode MCP tool error: %v", err)
	}
	var structured map[string]json.RawMessage
	if err := json.Unmarshal(result.StructuredContent, &structured); err != nil {
		t.Fatalf("decode MCP tool error structured content: %v", err)
	}
	if len(structured) != 1 {
		t.Fatalf("MCP tool error includes unexpected structured fields: %s", result.StructuredContent)
	}
	var toolError mcpToolErrorResponse
	if err := json.Unmarshal(structured["error"], &toolError); err != nil {
		t.Fatalf("decode MCP tool error payload: %v", err)
	}
	if !result.IsError || len(result.Content) != 1 || result.Content[0].Type != "text" || result.Content[0].Text != code+": "+message || toolError.Code != code || toolError.Message != message {
		t.Fatalf("MCP tool error = %#v", result)
	}
}

func mcpSchemaObject(t *testing.T, contents json.RawMessage) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(contents, &schema); err != nil {
		t.Fatalf("decode MCP schema %q: %v", contents, err)
	}
	if schema["type"] != "object" {
		t.Fatalf("MCP schema type = %#v, want object", schema["type"])
	}
	return schema
}

func mcpSchemaProperties(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("MCP schema properties = %#v", schema["properties"])
	}
	return properties
}

func assertMCPSchemaRequired(t *testing.T, schema map[string]any, names ...string) {
	t.Helper()
	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("MCP schema required = %#v", schema["required"])
	}
	for _, name := range names {
		found := false
		for _, value := range required {
			if value == name {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("MCP schema required = %#v, missing %q", required, name)
		}
	}
}

func assertMCPSchemaNumber(t *testing.T, schema map[string]any, name string, minimum, maximum, defaultValue any) {
	t.Helper()
	property, ok := mcpSchemaProperties(t, schema)[name].(map[string]any)
	if !ok {
		t.Fatalf("MCP schema property %q = %#v", name, mcpSchemaProperties(t, schema)[name])
	}
	if property["type"] != "integer" || property["minimum"] != minimum || property["maximum"] != maximum {
		t.Fatalf("MCP schema number %q = %#v", name, property)
	}
	if defaultValue == nil {
		if _, ok := property["default"]; ok {
			t.Fatalf("MCP schema number %q unexpectedly has default: %#v", name, property)
		}
		return
	}
	if property["default"] != defaultValue {
		t.Fatalf("MCP schema number %q default = %#v, want %#v", name, property["default"], defaultValue)
	}
}

func assertMCPSchemaArrayEnum(t *testing.T, schema map[string]any, name string, want []string) {
	t.Helper()
	property, ok := mcpSchemaProperties(t, schema)[name].(map[string]any)
	if !ok {
		t.Fatalf("MCP schema property %q = %#v", name, mcpSchemaProperties(t, schema)[name])
	}
	items, ok := property["items"].(map[string]any)
	if !ok || property["type"] != "array" || property["minItems"] != float64(1) {
		t.Fatalf("MCP schema array %q = %#v", name, property)
	}
	assertMCPSchemaEnum(t, items, want)
}

func assertMCPSchemaEnum(t *testing.T, schema any, want []string) {
	t.Helper()
	object, ok := schema.(map[string]any)
	if !ok {
		t.Fatalf("MCP enum schema = %#v", schema)
	}
	values, ok := object["enum"].([]any)
	if !ok || len(values) != len(want) {
		t.Fatalf("MCP enum schema = %#v", object)
	}
	for index, value := range want {
		if values[index] != value {
			t.Fatalf("MCP enum schema = %#v, want %#v", values, want)
		}
	}
}

type mcpToolErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
