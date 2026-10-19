package diff

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

func TestLineEndingComparisonPreservesOtherDifferences(t *testing.T) {
	utf16Text := func(text string, big bool) []byte {
		var order binary.ByteOrder = binary.LittleEndian
		result := []byte{0xff, 0xfe}
		if big {
			order = binary.BigEndian
			result = []byte{0xfe, 0xff}
		}
		for _, unit := range utf16.Encode([]rune(text)) {
			var data [2]byte
			order.PutUint16(data[:], unit)
			result = append(result, data[:]...)
		}
		return result
	}
	for _, test := range []struct {
		name            string
		left, right     []byte
		supported, only bool
	}{
		{"LF CRLF", []byte("a\n"), []byte("a\r\n"), true, true},
		{"mixed same size", []byte("a\nb\r\n"), []byte("a\r\nb\n"), true, true},
		{"content", []byte("a\n"), []byte("b\r\n"), true, false},
		{"space", []byte("a \n"), []byte("a\r\n"), true, false},
		{"tab", []byte("\ta\n"), []byte("a\r\n"), true, false},
		{"EOF", []byte("a"), []byte("a\r\n"), true, false},
		{"bare CR", []byte("a\r"), []byte("a\n"), true, false},
		{"blank line", []byte("a\n\n"), []byte("a\r\n"), true, false},
		{"UTF8 BOM", []byte("\xef\xbb\xbfa\n"), []byte("\xef\xbb\xbfa\r\n"), true, true},
		{"different BOM", []byte("a\n"), []byte("\xef\xbb\xbfa\r\n"), false, false},
		{"UTF16 LE", utf16Text("你好\n", false), utf16Text("你好\r\n", false), true, true},
		{"UTF16 BE", utf16Text("你好\n", true), utf16Text("你好\r\n", true), true, true},
		{"different encoding", []byte("a\n"), utf16Text("a\r\n", false), false, false},
		{"invalid UTF8", []byte{0xff, '\n'}, []byte{0xff, '\r', '\n'}, false, false},
		{"invalid UTF16", []byte{0xff, 0xfe, 0, 0xd8}, []byte{0xff, 0xfe, 0, 0xd8, 13, 0, 10, 0}, false, false},
		{"NUL", []byte("a\x00\n"), []byte("a\x00\r\n"), false, false},
		{"chunk boundary", []byte(strings.Repeat("x", comparisonChunkSize-1) + "\n"), []byte(strings.Repeat("x", comparisonChunkSize-1) + "\r\n"), true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			baseline, target := comparisonRoots(t)
			writeComparisonBytes(t, baseline, "file.txt", test.left)
			writeComparisonBytes(t, target, "file.txt", test.right)
			workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := refreshWorkspace(t, workspace)
			id := snapshotEntryID(t, snapshot, "file.txt")
			detail, err := snapshot.Detail(id, ComparisonOptions{IgnoreLineEndings: true})
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := StatusModified
			if test.only {
				wantStatus = StatusUnchanged
			}
			if detail.Status != wantStatus || detail.RawStatus != StatusModified || detail.LineEndings.Supported != test.supported || detail.LineEndings.Only != test.only {
				t.Fatalf("detail = %#v", detail)
			}
			if !test.supported && detail.LineEndings.Reason == "" {
				t.Fatal("missing unsupported reason")
			}
			if snapshot.Summary().Counts.Modified != 1 {
				t.Fatal("projection mutated raw snapshot")
			}
			data, err := os.ReadFile(filepath.Join(target, "file.txt"))
			if err != nil || string(data) != string(test.right) {
				t.Fatal("source modified", err)
			}
		})
	}
}

func lineEndingFixture(t *testing.T) (*Workspace, *Snapshot) {
	t.Helper()
	baseline, target := comparisonRoots(t)
	writeComparisonFile(t, baseline, "src/a-eol.txt", "same\n")
	writeComparisonFile(t, target, "src/a-eol.txt", "same\r\n")
	writeComparisonFile(t, baseline, "src/z-content.txt", "before\n")
	writeComparisonFile(t, target, "src/z-content.txt", "after\r\n")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatal(err)
	}
	return workspace, refreshWorkspace(t, workspace)
}

func TestLineEndingViewsApplyBeforeFiltersPaginationAndCounts(t *testing.T) {
	_, snapshot := lineEndingFixture(t)
	opts := ComparisonOptions{IgnoreLineEndings: true}
	summary := snapshot.Summary(opts)
	if summary.ID != snapshot.Summary().ID || summary.Counts != (StatusCounts{Modified: 1, Unchanged: 1}) {
		t.Fatalf("summary = %#v", summary)
	}
	page, err := snapshot.List(EntryQuery{IgnoreLineEndings: true, Statuses: []ComparisonStatus{StatusModified}, Limit: 1})
	if err != nil || len(page.Entries) != 1 || page.Entries[0].Path != "src/z-content.txt" || page.NextCursor != "" {
		t.Fatalf("page = %#v, %v", page, err)
	}
	id := snapshotEntryID(t, snapshot, "src/a-eol.txt")
	if _, err := snapshot.List(EntryQuery{IgnoreLineEndings: true, Anchor: id, Statuses: []ComparisonStatus{StatusModified}}); err == nil {
		t.Fatal("modified anchor accepted projected unchanged")
	}
	if page, err := snapshot.List(EntryQuery{IgnoreLineEndings: true, Anchor: id, Statuses: []ComparisonStatus{StatusUnchanged}}); err != nil || len(page.Entries) != 1 {
		t.Fatalf("unchanged anchor = %#v, %v", page, err)
	}
	tree := snapshot.Tree("", opts)
	if len(tree.Children) != 1 || tree.Children[0].Counts != summary.Counts {
		t.Fatalf("tree = %#v", tree)
	}
	search := snapshot.Search("eol", []ComparisonStatus{StatusModified}, 1, opts)
	if len(search.Results) != 0 || search.Truncated {
		t.Fatalf("search = %#v", search)
	}
	search = snapshot.Search("src", []ComparisonStatus{StatusUnchanged}, 10, opts)
	if len(search.Results) != 2 || search.Results[1].Status != StatusUnchanged {
		t.Fatalf("unchanged search = %#v", search)
	}
	contentDiff, err := snapshot.TextDiff(context.Background(), snapshotEntryID(t, snapshot, "src/z-content.txt"), &TextDiffOptions{IgnoreLineEndings: true})
	if err != nil || contentDiff.Status != TextDiffReady || contentDiff.AddedLines != 1 || contentDiff.DeletedLines != 1 || strings.Contains(contentDiff.Patch, "\r") {
		t.Fatalf("normalized content diff = %#v, %v", contentDiff, err)
	}
	if snapshot.Summary().Counts != (StatusCounts{Modified: 2}) {
		t.Fatal("raw snapshot changed")
	}
}

func TestLineEndingLimitAndGuardedTextDiff(t *testing.T) {
	baseline, target := comparisonRoots(t)
	// Both sides are within the limit; line length and rendering guards do not affect equality.
	body := strings.Repeat("x", int(maxConfirmedTextBytes)-2)
	writeComparisonFile(t, baseline, "limit.txt", body+"\n")
	writeComparisonFile(t, target, "limit.txt", body+"\r\n")
	writeComparisonFile(t, baseline, "over.txt", body+"x\n")
	writeComparisonFile(t, target, "over.txt", body+"x\r\n")
	workspace, err := NewWorkspace(WorkspaceOptions{BaselineDirectory: baseline, TargetDirectory: target})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := refreshWorkspace(t, workspace)
	limitID := snapshotEntryID(t, snapshot, "limit.txt")
	result, err := snapshot.TextDiff(context.Background(), limitID, &TextDiffOptions{IgnoreLineEndings: true})
	if err != nil || result.Status != TextDiffNoTextualChanges || result.ComparisonStatus != StatusUnchanged || result.Reason != TextDiffLineEndingsOnly {
		t.Fatalf("limit diff = %#v, %v", result, err)
	}
	over, err := snapshot.Detail(snapshotEntryID(t, snapshot, "over.txt"), ComparisonOptions{IgnoreLineEndings: true})
	if err != nil || over.Status != StatusModified || over.LineEndings.Supported || !strings.Contains(over.LineEndings.Reason, "20 MiB") {
		t.Fatalf("over detail = %#v, %v", over, err)
	}
	if err := os.WriteFile(filepath.Join(target, "limit.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = snapshot.TextDiff(context.Background(), limitID, &TextDiffOptions{IgnoreLineEndings: true})
	if err != nil || result.Reason != TextDiffStale {
		t.Fatalf("stale diff = %#v, %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = snapshot.TextDiff(ctx, limitID, &TextDiffOptions{IgnoreLineEndings: true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestHTTPLineEndingViewsAndIndependentEventStreams(t *testing.T) {
	workspace, snapshot := lineEndingFixture(t)
	handler := NewHTTPHandler(workspace)
	id := snapshotEntryID(t, snapshot, "src/a-eol.txt")
	for _, mode := range []bool{true, false, true, false} {
		suffix := "?ignoreLineEndings=" + strconv.FormatBool(mode)
		response := httpResponse(handler, http.MethodGet, "/api/state"+suffix)
		var state httpStatePayload
		if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		modified := 2
		if mode {
			modified = 1
		}
		if state.Snapshot.Counts.Modified != modified {
			t.Fatalf("state = %#v", state.Snapshot)
		}
		query := "?snapshot=" + snapshot.Summary().ID + "&ignoreLineEndings=" + strconv.FormatBool(mode)
		list := decodeHTTPList(t, httpResponse(handler, http.MethodGet, "/api/entries"+query+"&limit=1&status=modified"))
		want := "src/a-eol.txt"
		if mode {
			want = "src/z-content.txt"
		}
		if len(list.Entries) != 1 || list.Entries[0].Path != want {
			t.Fatalf("list = %#v", list)
		}
		response = httpResponse(handler, http.MethodGet, "/api/entries/"+strconv.Itoa(id)+query)
		var detail httpEntryDetail
		if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil {
			t.Fatal(err)
		}
		if detail.RawStatus != StatusModified || !detail.LineEndings.Only || (detail.Status == StatusUnchanged) != mode {
			t.Fatalf("detail = %#v", detail)
		}
	}
	for _, path := range []string{"/api/state?", "/api/events?", "/api/tree?snapshot=" + snapshot.Summary().ID + "&", "/api/search?snapshot=" + snapshot.Summary().ID + "&q=eol&", "/api/entries?snapshot=" + snapshot.Summary().ID + "&", "/api/entries/" + strconv.Itoa(id) + "?snapshot=" + snapshot.Summary().ID + "&"} {
		for _, value := range []string{"", "1", "TRUE", "true&ignoreLineEndings=false"} {
			response := httpResponse(handler, http.MethodGet, path+"ignoreLineEndings="+value)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("invalid %s%s = %d", path, value, response.Code)
			}
		}
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	for _, mode := range []bool{true, false} {
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get(server.URL + "/api/events?ignoreLineEndings=" + strconv.FormatBool(mode))
		if err != nil {
			t.Fatal(err)
		}
		_, state := readHTTPEventState(t, bufio.NewReader(response.Body))
		response.Body.Close()
		modified := 2
		if mode {
			modified = 1
		}
		if state.Snapshot.Counts.Modified != modified {
			t.Fatalf("event state = %#v", state)
		}
	}
}

func TestMCPLineEndingOptionIsRequestScoped(t *testing.T) {
	workspace, snapshot := lineEndingFixture(t)
	server := httptest.NewServer(NewMCPHandler(workspace, "127.0.0.1"))
	defer server.Close()
	id := snapshotEntryID(t, snapshot, "src/a-eol.txt")
	for _, mode := range []bool{true, false, true, false} {
		option := `,"ignore_line_endings":` + strconv.FormatBool(mode)
		call := func(tool, args string) mcpRPCResponse {
			t.Helper()
			response := mcpToolCall(t, server.URL, "1", tool, args)
			var rpc mcpRPCResponse
			decodeMCPResponse(t, response, &rpc)
			if rpc.Error != nil {
				t.Fatalf("%s: %#v", tool, rpc.Error)
			}
			return rpc
		}
		rpc := call("get_comparison", `{"ignore_line_endings":`+strconv.FormatBool(mode)+`}`)
		var comparison mcpComparisonCallResult
		if err := json.Unmarshal(rpc.Result, &comparison); err != nil {
			t.Fatal(err)
		}
		modified := 2
		if mode {
			modified = 1
		}
		if comparison.StructuredContent.Snapshot.Counts.Modified != modified {
			t.Fatalf("MCP counts = %#v", comparison)
		}
		args := `{"snapshot_id":` + jsonString(snapshot.Summary().ID) + option
		changes := decodeMCPChanges(t, call("list_changes", args+`,"limit":1}`))
		want := "src/a-eol.txt"
		if mode {
			want = "src/z-content.txt"
		}
		if len(changes.Changes) != 1 || changes.Changes[0].Path != want {
			t.Fatalf("MCP changes = %#v", changes)
		}
		search := decodeMCPSearchChanges(t, call("search_changes", args+`,"query":"eol","limit":1}`))
		count := 1
		if mode {
			count = 0
		}
		if len(search.Changes) != count || search.Truncated {
			t.Fatalf("MCP search = %#v", search)
		}
		diff := decodeMCPTextDiff(t, call("get_text_diff", args+`,"entry_id":`+strconv.Itoa(id)+`}`))
		if mode {
			if diff.Output.Status != TextDiffNoTextualChanges || diff.Output.ComparisonStatus != StatusUnchanged || diff.Output.Reason != TextDiffLineEndingsOnly || diff.Output.Patch != nil || diff.Output.ContextLines != nil || diff.Output.AddedLines != nil {
				t.Fatalf("MCP diff = %#v", diff)
			}
		} else if diff.Output.Status != TextDiffReady || diff.Output.ComparisonStatus != StatusModified {
			t.Fatalf("MCP exact = %#v", diff)
		}
	}
	for _, tool := range []string{"get_comparison", "list_changes", "search_changes", "get_text_diff"} {
		args := `{"snapshot_id":` + jsonString(snapshot.Summary().ID) + `,"query":"eol","entry_id":` + strconv.Itoa(id) + `,"ignore_line_endings":"true"}`
		response := mcpToolCall(t, server.URL, "2", tool, args)
		var rpc mcpRPCResponse
		decodeMCPResponse(t, response, &rpc)
		if rpc.Error == nil || rpc.Error.Code != -32602 {
			t.Fatalf("%s accepted string option: %#v", tool, rpc)
		}
	}
}
