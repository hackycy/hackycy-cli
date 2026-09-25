package tunnelruntime

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRuntimeDigestIsStableAcrossTunnelOrdering(t *testing.T) {
	left := ClientRuntime{
		Revision: 4, NodeID: "local", AdvertisedFRPHost: "frp.example.test", AdvertisedFRPPort: 7000,
		FRPToken: "token", ClientKey: "client", Tunnels: []TunnelDefinition{{ID: "b", Protocol: TunnelProtocolTCP}, {ID: "a", Protocol: TunnelProtocolUDP}},
	}
	right := left
	right.Tunnels = []TunnelDefinition{left.Tunnels[1], left.Tunnels[0]}
	first, err := RuntimeDigest(left)
	if err != nil {
		t.Fatalf("RuntimeDigest(left) error = %v", err)
	}
	second, err := RuntimeDigest(right)
	if err != nil || first != second {
		t.Fatalf("RuntimeDigest ordering = (%q, %q, %v)", first, second, err)
	}
	if first == "" || len(first) != len("sha256:")+64 {
		t.Fatalf("RuntimeDigest = %q", first)
	}
}

func TestV5WelcomeAndDesiredStateCarryTheSameRuntimeObject(t *testing.T) {
	runtime := ClientRuntime{Revision: 2, NodeID: "local", AdvertisedFRPHost: "frp.example.test", AdvertisedFRPPort: 7000, FRPToken: "token", ClientKey: "client"}
	var err error
	runtime.Digest, err = RuntimeDigest(runtime)
	if err != nil {
		t.Fatal(err)
	}
	welcome := AgentWelcome{Type: "welcome", TunnelProtocolVersion: TunnelProtocolVersion, Runtime: runtime}
	desired := DesiredState{Type: "desired_state", TunnelProtocolVersion: TunnelProtocolVersion, Runtime: runtime}
	welcomeBytes, err := json.Marshal(welcome)
	if err != nil {
		t.Fatal(err)
	}
	desiredBytes, err := json.Marshal(desired)
	if err != nil {
		t.Fatal(err)
	}
	var welcomeEnvelope, desiredEnvelope struct {
		Runtime ClientRuntime `json:"runtime"`
	}
	if err := json.Unmarshal(welcomeBytes, &welcomeEnvelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(desiredBytes, &desiredEnvelope); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(welcomeEnvelope.Runtime, desiredEnvelope.Runtime) || welcomeEnvelope.Runtime.Digest != runtime.Digest {
		t.Fatalf("welcome/desired runtime mismatch: %#v / %#v", welcomeEnvelope.Runtime, desiredEnvelope.Runtime)
	}
}
