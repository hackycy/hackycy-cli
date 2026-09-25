package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/hackycy/hackycy-cli/internal/tunnelruntime"
)

const nodeSnapshotFormatVersion = 1
const maximumSnapshotBytes = 2 << 20
const maximumSnapshotChunkBytes = 32 << 10

type desiredSnapshot struct {
	FormatVersion  int    `json:"formatVersion"`
	FRPVersion     string `json:"frpVersion"`
	NodeID         string `json:"nodeId"`
	Revision       int64  `json:"revision"`
	State          string `json:"state"`
	BindAddress    string `json:"bindAddress,omitempty"`
	BindPort       int64  `json:"bindPort,omitempty"`
	VhostHTTPPort  int64  `json:"vhostHTTPPort,omitempty"`
	PortRangeStart int64  `json:"portRangeStart,omitempty"`
	PortRangeEnd   int64  `json:"portRangeEnd,omitempty"`
	Token          string `json:"token,omitempty"`
	Custom404Page  string `json:"custom404Page,omitempty"`
}

func decodeDesiredSnapshot(contents []byte, nodeID string) (desiredSnapshot, error) {
	var snapshot desiredSnapshot
	if len(contents) == 0 || len(contents) > maximumSnapshotBytes {
		return snapshot, fmt.Errorf("snapshot size is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return snapshot, fmt.Errorf("decode snapshot: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return snapshot, fmt.Errorf("snapshot has trailing or invalid data")
	}
	if snapshot.FormatVersion != nodeSnapshotFormatVersion || snapshot.FRPVersion != tunnelruntime.FRPVersion || snapshot.NodeID != nodeID || snapshot.Revision < 1 {
		return snapshot, fmt.Errorf("snapshot identity, revision or version is invalid")
	}
	switch snapshot.State {
	case "disabled":
		if snapshot.BindAddress != "" || snapshot.BindPort != 0 || snapshot.VhostHTTPPort != 0 || snapshot.PortRangeStart != 0 || snapshot.PortRangeEnd != 0 || snapshot.Token != "" || snapshot.Custom404Page != "" {
			return snapshot, fmt.Errorf("disabled snapshot contains running configuration")
		}
	case "running":
		if net.ParseIP(snapshot.BindAddress) == nil || !validSnapshotPort(snapshot.BindPort) || !validSnapshotPort(snapshot.VhostHTTPPort) || !validSnapshotPort(snapshot.PortRangeStart) || !validSnapshotPort(snapshot.PortRangeEnd) || snapshot.PortRangeStart > snapshot.PortRangeEnd || strings.TrimSpace(snapshot.Token) == "" || len(snapshot.Custom404Page) > 512<<10 || snapshot.BindPort == snapshot.VhostHTTPPort || snapshot.BindPort >= snapshot.PortRangeStart && snapshot.BindPort <= snapshot.PortRangeEnd || snapshot.VhostHTTPPort >= snapshot.PortRangeStart && snapshot.VhostHTTPPort <= snapshot.PortRangeEnd {
			return snapshot, fmt.Errorf("running snapshot FRPS configuration is invalid")
		}
	default:
		return snapshot, fmt.Errorf("snapshot state is invalid")
	}
	return snapshot, nil
}

func validSnapshotPort(port int64) bool { return port >= 1 && port <= 65535 }
