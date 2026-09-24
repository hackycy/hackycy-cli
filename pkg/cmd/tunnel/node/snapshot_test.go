package node

import (
	"encoding/json"
	"testing"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

func TestDecodeDesiredSnapshotValidatesCompleteRunningAndDisabledStates(t *testing.T) {
	running := desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: "node-1", Revision: 1, State: "running", BindAddress: "127.0.0.1", BindPort: 7000, VhostHTTPPort: 7001, PortRangeStart: 8000, PortRangeEnd: 8100, Token: "private-token"}
	encode := func(value desiredSnapshot) []byte {
		t.Helper()
		contents, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return contents
	}
	if _, err := decodeDesiredSnapshot(encode(running), "node-1"); err != nil {
		t.Fatal(err)
	}
	disabled := desiredSnapshot{FormatVersion: 1, FRPVersion: tunnelruntime.FRPVersion, NodeID: "node-1", Revision: 2, State: "disabled"}
	if _, err := decodeDesiredSnapshot(encode(disabled), "node-1"); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*desiredSnapshot){
		"wrong node":    func(s *desiredSnapshot) { s.NodeID = "other" },
		"wrong format":  func(s *desiredSnapshot) { s.FormatVersion++ },
		"wrong FRP":     func(s *desiredSnapshot) { s.FRPVersion = "0.0.0" },
		"no revision":   func(s *desiredSnapshot) { s.Revision = 0 },
		"bad bind":      func(s *desiredSnapshot) { s.BindAddress = "example.test" },
		"port overlap":  func(s *desiredSnapshot) { s.BindPort = 8000 },
		"missing token": func(s *desiredSnapshot) { s.Token = "" },
		"bad range":     func(s *desiredSnapshot) { s.PortRangeStart = 8101 },
	} {
		t.Run(name, func(t *testing.T) {
			value := running
			change(&value)
			if _, err := decodeDesiredSnapshot(encode(value), "node-1"); err == nil {
				t.Fatal("invalid snapshot accepted")
			}
		})
	}
	disabled.Token = "must-not-survive"
	if _, err := decodeDesiredSnapshot(encode(disabled), "node-1"); err == nil {
		t.Fatal("disabled snapshot retained token")
	}
	if _, err := decodeDesiredSnapshot(append(encode(running), []byte(` {}`)...), "node-1"); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := decodeDesiredSnapshot(append(encode(running)[:len(encode(running))-1], []byte(`,"unknown":1}`)...), "node-1"); err == nil {
		t.Fatal("unknown field accepted")
	}
}
