//go:build darwin || linux

package diff

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestMCPHandlerListsSnapshotBoundIssues(t *testing.T) {
	baseline, target := comparisonRoots(t)
	for _, name := range []string{"alpha.pipe", "bravo.pipe"} {
		if err := unix.Mkfifo(filepath.Join(target, name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatalf("NewWorkspace() error = %v", err)
	}
	summary := refreshWorkspace(t, workspace).Summary()
	server := httptest.NewServer(NewMCPHandler(workspace, "127.0.0.1"))
	defer server.Close()

	firstResponse := mcpToolCall(t, server.URL, "1", "list_issues", `{"snapshot_id":`+jsonString(summary.ID)+`,"limit":1}`)
	assertMCPResponseHeaders(t, firstResponse)
	assertNoMCPSession(t, firstResponse)
	var firstRPC mcpRPCResponse
	decodeMCPResponse(t, firstResponse, &firstRPC)
	first := decodeMCPIssues(t, firstRPC)
	if len(first.Issues) != 1 || first.Issues[0].Path != "alpha.pipe" || !strings.Contains(first.Issues[0].Message, "unsupported filesystem kind") || first.NextCursor == "" || first.Content != "Listed 1 Comparison Issue; more available" {
		t.Fatalf("first MCP issue page = %#v", first)
	}

	defaultResponse := mcpToolCall(t, server.URL, "2", "list_issues", `{"snapshot_id":`+jsonString(summary.ID)+`}`)
	assertMCPResponseHeaders(t, defaultResponse)
	var defaultRPC mcpRPCResponse
	decodeMCPResponse(t, defaultResponse, &defaultRPC)
	all := decodeMCPIssues(t, defaultRPC)
	if len(all.Issues) != 2 || all.Issues[0].Path != "alpha.pipe" || all.Issues[1].Path != "bravo.pipe" || all.NextCursor != "" || all.Content != "Listed 2 Comparison Issues" {
		t.Fatalf("default MCP issue page = %#v", all)
	}

	secondResponse := mcpToolCall(t, server.URL, "3", "list_issues", `{"snapshot_id":`+jsonString(summary.ID)+`,"limit":1,"cursor":`+jsonString(first.NextCursor)+`}`)
	assertMCPResponseHeaders(t, secondResponse)
	var secondRPC mcpRPCResponse
	decodeMCPResponse(t, secondResponse, &secondRPC)
	second := decodeMCPIssues(t, secondRPC)
	if len(second.Issues) != 1 || second.Issues[0].Path != "bravo.pipe" || second.NextCursor != "" || second.Content != "Listed 1 Comparison Issue" {
		t.Fatalf("second MCP issue page = %#v", second)
	}

	filteredResponse := mcpToolCall(t, server.URL, "4", "list_issues", `{"snapshot_id":`+jsonString(summary.ID)+`,"path":"BRAVO"}`)
	assertMCPResponseHeaders(t, filteredResponse)
	var filteredRPC mcpRPCResponse
	decodeMCPResponse(t, filteredResponse, &filteredRPC)
	filtered := decodeMCPIssues(t, filteredRPC)
	if len(filtered.Issues) != 1 || filtered.Issues[0].Path != "bravo.pipe" || filtered.Content != "Listed 1 Comparison Issue" {
		t.Fatalf("filtered MCP issue page = %#v", filtered)
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
			response := mcpToolCall(t, server.URL, "5", "list_issues", test.arguments)
			assertMCPResponseHeaders(t, response)
			var rpc mcpRPCResponse
			decodeMCPResponse(t, response, &rpc)
			assertMCPToolError(t, rpc, test.code, test.message)
		})
	}

	for _, arguments := range []string{
		`{"snapshot_id":` + jsonString(summary.ID) + `,"limit":501}`,
		`{"snapshot_id":` + jsonString(summary.ID) + `,"cursor":""}`,
	} {
		response := mcpToolCall(t, server.URL, "6", "list_issues", arguments)
		assertMCPResponseHeaders(t, response)
		var rpc mcpRPCResponse
		decodeMCPResponse(t, response, &rpc)
		if rpc.Error == nil || rpc.Error.Code != -32602 {
			t.Fatalf("invalid list_issues input response = %#v", rpc)
		}
	}
}

type mcpIssuePage struct {
	Content    string
	Issues     []mcpIssue
	NextCursor string
}

func decodeMCPIssues(t *testing.T, response mcpRPCResponse) mcpIssuePage {
	t.Helper()
	if response.Error != nil {
		t.Fatalf("list_issues protocol error = %#v", response.Error)
	}
	var result struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent struct {
			Issues     []mcpIssue `json:"issues"`
			NextCursor string     `json:"next_cursor"`
		} `json:"structuredContent"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode list_issues response: %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Type != "text" {
		t.Fatalf("list_issues content = %#v", result.Content)
	}
	return mcpIssuePage{
		Content:    result.Content[0].Text,
		Issues:     result.StructuredContent.Issues,
		NextCursor: result.StructuredContent.NextCursor,
	}
}
