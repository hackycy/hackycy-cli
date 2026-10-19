//go:build acceptance && !windows

package acceptance

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiffStandaloneLineEndingViewsAgreeAcrossHTTPAndMCP(t *testing.T) {
	binary := buildDiffStandaloneBinary(t)
	root := t.TempDir()
	baseline, target := filepath.Join(root, "baseline"), filepath.Join(root, "target")
	writeStandaloneDiffFile(t, baseline, "line-endings.txt", "same\n")
	writeStandaloneDiffFile(t, target, "line-endings.txt", "same\r\n")
	process := startDiffStandalone(t, binary, root, environmentWith(map[string]string{"HOME": t.TempDir(), "USERPROFILE": ""}), "diff", "--port", "0", baseline, target)
	_, localURL := waitForDiffStartup(t, process)
	waitForDiffReady(t, localURL)
	client := &http.Client{Timeout: 5 * time.Second}
	for _, mode := range []string{"true", "false", "true"} {
		response, err := client.Get(localURL + "/api/state?ignoreLineEndings=" + mode)
		if err != nil {
			t.Fatal(err)
		}
		var state struct {
			Snapshot struct {
				ID     string `json:"id"`
				Counts struct {
					Modified  int `json:"modified"`
					Unchanged int `json:"unchanged"`
				} `json:"counts"`
			} `json:"snapshot"`
		}
		err = json.NewDecoder(response.Body).Decode(&state)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		wantModified := 1
		wantUnchanged := 0
		if mode == "true" {
			wantModified = 0
			wantUnchanged = 1
		}
		if state.Snapshot.Counts.Modified != wantModified || state.Snapshot.Counts.Unchanged != wantUnchanged {
			t.Fatalf("HTTP counts %s = %#v", mode, state)
		}
		payload := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"get_text_diff","arguments":{"snapshot_id":"` + state.Snapshot.ID + `","entry_id":1,"ignore_line_endings":` + mode + `}}}`
		request, err := http.NewRequest(http.MethodPost, localURL+"/mcp", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response, err = client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		var rpc struct {
			Error  json.RawMessage `json:"error"`
			Result struct {
				IsError    bool `json:"isError"`
				Structured struct {
					Status           string  `json:"status"`
					ComparisonStatus string  `json:"comparison_status"`
					Reason           string  `json:"reason"`
					Patch            *string `json:"patch"`
				} `json:"structuredContent"`
			} `json:"result"`
		}
		err = json.NewDecoder(response.Body).Decode(&rpc)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if len(rpc.Error) > 0 || rpc.Result.IsError {
			t.Fatalf("MCP response = %#v", rpc)
		}
		output := rpc.Result.Structured
		if mode == "true" {
			if output.Status != "no_textual_changes" || output.ComparisonStatus != "unchanged" || output.Reason != "line_endings_only" || output.Patch != nil {
				t.Fatalf("MCP ignored = %#v", output)
			}
		} else if output.Status != "ready" || output.ComparisonStatus != "modified" || output.Patch == nil {
			t.Fatalf("MCP exact = %#v", output)
		}
	}
	if err := process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	if err := waitForDiffProcess(t, process); err != nil {
		t.Fatal(err)
	}
}
