package server

import (
	"context"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"strings"
	"testing"
)

func TestServerControlPlaneReservesHTTPRoutesTransactionally(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	ctx := context.Background()
	first, err := plane.CreateClient(ctx, "environment-admin", "First client")
	if err != nil {
		t.Fatalf("CreateClient(first) error = %v", err)
	}
	second, err := plane.CreateClient(ctx, "environment-admin", "Second client")
	if err != nil {
		t.Fatalf("CreateClient(second) error = %v", err)
	}
	location := "/service-a"
	tunnel, err := plane.CreateTunnel(ctx, first.ID, TunnelMutationInput{
		Protocol:      tunnelruntime.TunnelProtocolHTTP,
		CustomDomains: []string{"APP.example.com", "alias.example.com"},
		Location:      &location,
		LocalPort:     3000,
		Enabled:       boolPointer(false),
	})
	if err != nil {
		t.Fatalf("CreateTunnel(HTTP) error = %v", err)
	}
	if tunnel.Protocol != tunnelruntime.TunnelProtocolHTTP || strings.Join(tunnel.CustomDomains, ",") != "app.example.com,alias.example.com" || tunnel.Location == nil || *tunnel.Location != location || tunnel.Enabled {
		t.Fatalf("HTTP Tunnel = %#v", tunnel)
	}
	_, err = plane.CreateTunnel(ctx, second.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"alias.example.com"}, Location: &location, LocalPort: 3001})
	assertServerDomainCode(t, err, "RESOURCE_RESERVED")
	if tunnels, listErr := plane.ListTunnels(ctx, second.ID); listErr != nil || len(tunnels) != 0 {
		t.Fatalf("second ListTunnels() = (%#v, %v), want none after rollback", tunnels, listErr)
	}
	if client, getErr := plane.GetClient(ctx, second.ID); getErr != nil || client.DesiredRevision != 0 {
		t.Fatalf("second client after rollback = (%#v, %v)", client, getErr)
	}
	siblingLocation := "/service-b"
	if _, err := plane.CreateTunnel(ctx, second.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"app.example.com"}, Location: &siblingLocation, LocalPort: 3002}); err != nil {
		t.Fatalf("CreateTunnel(sibling) error = %v", err)
	}
	catchAll, err := plane.CreateTunnel(ctx, second.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"app.example.com"}, LocalPort: 3003})
	if err != nil || catchAll.Location != nil {
		t.Fatalf("CreateTunnel(catch-all) = (%#v, %v)", catchAll, err)
	}
	if err := plane.DeleteTunnel(ctx, tunnel.ID); err != nil {
		t.Fatalf("DeleteTunnel() error = %v", err)
	}
	if _, err := plane.CreateTunnel(ctx, second.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"app.example.com"}, Location: &location, LocalPort: 3004}); err != nil {
		t.Fatalf("CreateTunnel(released reservation) error = %v", err)
	}
}

func TestServerControlPlaneAllocatesPortReservationsAndRecordsRevisions(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	ctx := context.Background()
	first, err := plane.CreateClient(ctx, "environment-admin", "First client")
	if err != nil {
		t.Fatalf("CreateClient(first) error = %v", err)
	}
	second, err := plane.CreateClient(ctx, "environment-admin", "Second client")
	if err != nil {
		t.Fatalf("CreateClient(second) error = %v", err)
	}
	tcp, err := plane.CreateTunnel(ctx, first.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, LocalPort: 5432})
	if err != nil {
		t.Fatalf("CreateTunnel(tcp) error = %v", err)
	}
	udp, err := plane.CreateTunnel(ctx, second.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolUDP, LocalPort: 53})
	if err != nil {
		t.Fatalf("CreateTunnel(udp) error = %v", err)
	}
	nextTCP, err := plane.CreateTunnel(ctx, second.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, LocalPort: 5433})
	if err != nil || tcp.ServerPort == nil || udp.ServerPort == nil || nextTCP.ServerPort == nil || *tcp.ServerPort != 20000 || *udp.ServerPort != 20000 || *nextTCP.ServerPort != 20001 {
		t.Fatalf("allocated ports = (%#v, %#v, %#v, %v)", tcp, udp, nextTCP, err)
	}
	port := int64(20001)
	_, err = plane.CreateTunnel(ctx, first.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolTCP, ServerPort: &port, LocalPort: 1})
	assertServerDomainCode(t, err, "RESOURCE_RESERVED")
	if err := plane.RecordAppliedRevision(ctx, first.ID, 1); err != nil {
		t.Fatalf("RecordAppliedRevision() error = %v", err)
	}
	if err := plane.RecordAppliedRevision(ctx, first.ID, 2); err == nil {
		t.Fatal("RecordAppliedRevision() error = nil, want desired-revision rejection")
	} else {
		assertServerDomainCode(t, err, "INVALID_REVISION")
	}
	snapshot, err := plane.Snapshot(ctx, first.ID)
	if err != nil || snapshot.Revision != 1 || len(snapshot.Tunnels) != 1 || snapshot.Tunnels[0].ID != tcp.ID {
		t.Fatalf("Snapshot() = (%#v, %v)", snapshot, err)
	}
}

func TestServerControlPlaneStoresTypedTunnelOptions(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	client, err := plane.CreateClient(context.Background(), "environment-admin", "Advanced HTTP")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	label := " Ticket H5 "
	location := "/service-a"
	password := "secret-value"
	hostRewrite := "internal.example.com"
	proxyProtocol := "v2"
	tunnel, err := plane.CreateTunnel(context.Background(), client.ID, TunnelMutationInput{
		Protocol:      tunnelruntime.TunnelProtocolHTTP,
		CustomDomains: []string{"routes.example.com"},
		Location:      &location,
		LocalPort:     9001,
		Label:         &label,
		Options: &TunnelOptionsInput{
			Transport: &TunnelTransportOptionsInput{
				UseEncryption:        boolPointer(true),
				UseCompression:       boolPointer(true),
				BandwidthLimit:       &tunnelruntime.TunnelBandwidthLimit{Value: 2, Unit: "MB", Mode: "server"},
				ProxyProtocolVersion: &proxyProtocol,
			},
			HealthCheck: &TunnelHealthCheckInput{Type: "http", Path: stringPointer("/health"), IntervalSeconds: 10, TimeoutSeconds: 3, MaxFailed: 2, Headers: []tunnelruntime.TunnelHeader{{Name: "X-Probe", Value: "ycy"}}},
			HTTP: &TunnelHTTPOptionsInput{
				BasicAuth:         &tunnelruntime.TunnelBasicAuth{Username: "operator", Password: password},
				HostHeaderRewrite: &hostRewrite,
				RequestHeaders:    []tunnelruntime.TunnelHeader{{Name: "X-Forwarded-By", Value: "ycy"}},
				ResponseHeaders:   []tunnelruntime.TunnelHeader{{Name: "X-Tunnel", Value: "ticket"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	if tunnel.Label != "Ticket H5" || tunnel.Options.HTTP == nil || tunnel.Options.HTTP.BasicAuth == nil || tunnel.Options.HTTP.BasicAuth.Password != password || tunnel.Options.Transport.BandwidthLimit == nil || tunnel.Options.Transport.BandwidthLimit.Value != 2 || tunnel.Options.HealthCheck == nil || tunnel.Options.HealthCheck.Type != "http" {
		t.Fatalf("stored typed tunnel = %#v", tunnel)
	}
}

func TestServerControlPlanePatchesTunnelReservationsAndRevisionsAtomically(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	ctx := context.Background()
	first, err := plane.CreateClient(ctx, "environment-admin", "First client")
	if err != nil {
		t.Fatalf("CreateClient(first) error = %v", err)
	}
	second, err := plane.CreateClient(ctx, "environment-admin", "Second client")
	if err != nil {
		t.Fatalf("CreateClient(second) error = %v", err)
	}
	firstLocation := "/first"
	tunnel, err := plane.CreateTunnel(ctx, first.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"app.example.com"}, Location: &firstLocation, LocalPort: 3000})
	if err != nil {
		t.Fatalf("CreateTunnel(first) error = %v", err)
	}
	reservedLocation := "/reserved"
	if _, err := plane.CreateTunnel(ctx, second.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"app.example.com"}, Location: &reservedLocation, LocalPort: 3001}); err != nil {
		t.Fatalf("CreateTunnel(second) error = %v", err)
	}
	falseValue := false
	patched, err := plane.UpdateTunnel(ctx, tunnel.ID, TunnelPatchInput{Enabled: &falseValue})
	if err != nil {
		t.Fatalf("UpdateTunnel(enabled) error = %v", err)
	}
	if patched.Enabled || patched.Location == nil || *patched.Location != firstLocation || strings.Join(patched.CustomDomains, ",") != "app.example.com" {
		t.Fatalf("UpdateTunnel(enabled) = %#v", patched)
	}
	if client, err := plane.GetClient(ctx, first.ID); err != nil || client.DesiredRevision != 2 {
		t.Fatalf("first client after enabled patch = (%#v, %v)", client, err)
	}
	assertServerDomainCode(t, func() error {
		_, err := plane.UpdateTunnel(ctx, tunnel.ID, TunnelPatchInput{Location: &TunnelPatchValue[*string]{Value: &reservedLocation}})
		return err
	}(), "RESOURCE_RESERVED")
	unchanged, err := plane.GetTunnel(ctx, tunnel.ID)
	if err != nil || unchanged.Location == nil || *unchanged.Location != firstLocation {
		t.Fatalf("GetTunnel() after rejected patch = (%#v, %v)", unchanged, err)
	}
	if client, err := plane.GetClient(ctx, first.ID); err != nil || client.DesiredRevision != 2 {
		t.Fatalf("first client after rejected patch = (%#v, %v)", client, err)
	}
	movedLocation := "/moved"
	if _, err := plane.UpdateTunnel(ctx, tunnel.ID, TunnelPatchInput{Location: &TunnelPatchValue[*string]{Value: &movedLocation}}); err != nil {
		t.Fatalf("UpdateTunnel(moved location) error = %v", err)
	}
	if _, err := plane.UpdateTunnel(ctx, tunnel.ID, TunnelPatchInput{Location: &TunnelPatchValue[*string]{}}); err != nil {
		t.Fatalf("UpdateTunnel(clear location) error = %v", err)
	}
	cleared, err := plane.GetTunnel(ctx, tunnel.ID)
	if err != nil || cleared.Location != nil {
		t.Fatalf("GetTunnel() after clear location = (%#v, %v)", cleared, err)
	}
	if client, err := plane.GetClient(ctx, first.ID); err != nil || client.DesiredRevision != 4 {
		t.Fatalf("first client after successful patches = (%#v, %v)", client, err)
	}
}

func TestServerControlPlanePatchesTypedOptionsWithoutLeakingOrDroppingOmittedFields(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	ctx := context.Background()
	client, err := plane.CreateClient(ctx, "environment-admin", "Advanced HTTP")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	password := "secret-value"
	hostRewrite := "internal.example.com"
	proxyProtocol := "v2"
	tunnel, err := plane.CreateTunnel(ctx, client.ID, TunnelMutationInput{
		Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"options.example.com"}, LocalPort: 9001,
		Options: &TunnelOptionsInput{
			Transport:   &TunnelTransportOptionsInput{UseEncryption: boolPointer(true), BandwidthLimit: &tunnelruntime.TunnelBandwidthLimit{Value: 2, Unit: "MB", Mode: "server"}, ProxyProtocolVersion: &proxyProtocol},
			HealthCheck: &TunnelHealthCheckInput{Type: "http", Path: stringPointer("/health"), IntervalSeconds: 10, TimeoutSeconds: 3, MaxFailed: 2, Headers: []tunnelruntime.TunnelHeader{{Name: "X-Probe", Value: "ycy"}}},
			HTTP:        &TunnelHTTPOptionsInput{BasicAuth: &tunnelruntime.TunnelBasicAuth{Username: "operator", Password: password}, HostHeaderRewrite: &hostRewrite, RequestHeaders: []tunnelruntime.TunnelHeader{{Name: "X-Request", Value: "first"}}, ResponseHeaders: []tunnelruntime.TunnelHeader{{Name: "X-Response", Value: "first"}}},
		},
	})
	if err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	renamed := "renamed"
	patched, err := plane.UpdateTunnel(ctx, tunnel.ID, TunnelPatchInput{Options: &TunnelOptionsPatchInput{HTTP: &TunnelPatchValue[*TunnelHTTPOptionsPatchInput]{Value: &TunnelHTTPOptionsPatchInput{BasicAuth: &TunnelPatchValue[*TunnelBasicAuthPatchInput]{Value: &TunnelBasicAuthPatchInput{Username: renamed}}}}}})
	if err != nil {
		t.Fatalf("UpdateTunnel(rename Basic Auth) error = %v", err)
	}
	if patched.Options.HTTP == nil || patched.Options.HTTP.BasicAuth == nil || patched.Options.HTTP.BasicAuth.Username != renamed || patched.Options.HTTP.BasicAuth.Password != password || patched.Options.HTTP.HostHeaderRewrite == nil || *patched.Options.HTTP.HostHeaderRewrite != hostRewrite || len(patched.Options.HTTP.RequestHeaders) != 1 || patched.Options.HealthCheck == nil || patched.Options.Transport.BandwidthLimit == nil {
		t.Fatalf("UpdateTunnel(rename Basic Auth) = %#v", patched)
	}
	patched, err = plane.UpdateTunnel(ctx, tunnel.ID, TunnelPatchInput{Options: &TunnelOptionsPatchInput{
		Transport:   &TunnelTransportOptionsPatchInput{BandwidthLimit: &TunnelPatchValue[*tunnelruntime.TunnelBandwidthLimit]{}, ProxyProtocolVersion: &TunnelPatchValue[*string]{}},
		HealthCheck: &TunnelPatchValue[*TunnelHealthCheckInput]{},
		HTTP: &TunnelPatchValue[*TunnelHTTPOptionsPatchInput]{Value: &TunnelHTTPOptionsPatchInput{
			BasicAuth:         &TunnelPatchValue[*TunnelBasicAuthPatchInput]{},
			HostHeaderRewrite: &TunnelPatchValue[*string]{},
			RequestHeaders:    &[]tunnelruntime.TunnelHeader{},
		}},
	}})
	if err != nil {
		t.Fatalf("UpdateTunnel(clear options) error = %v", err)
	}
	if patched.Options.Transport.BandwidthLimit != nil || patched.Options.Transport.ProxyProtocolVersion != nil || patched.Options.HealthCheck != nil || patched.Options.HTTP == nil || patched.Options.HTTP.BasicAuth != nil || patched.Options.HTTP.HostHeaderRewrite != nil || len(patched.Options.HTTP.RequestHeaders) != 0 || len(patched.Options.HTTP.ResponseHeaders) != 1 {
		t.Fatalf("UpdateTunnel(clear options) = %#v", patched.Options)
	}
}

func TestServerControlPlanePatchSwitchesProtocolAndReleasesHTTPReservation(t *testing.T) {
	state := openServerDomainState(t)
	plane := openServerControlPlane(t, state)
	ctx := context.Background()
	first, err := plane.CreateClient(ctx, "environment-admin", "First client")
	if err != nil {
		t.Fatalf("CreateClient(first) error = %v", err)
	}
	second, err := plane.CreateClient(ctx, "environment-admin", "Second client")
	if err != nil {
		t.Fatalf("CreateClient(second) error = %v", err)
	}
	tunnel, err := plane.CreateTunnel(ctx, first.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"released.example.com"}, LocalPort: 3000})
	if err != nil {
		t.Fatalf("CreateTunnel() error = %v", err)
	}
	protocol := tunnelruntime.TunnelProtocolTCP
	patched, err := plane.UpdateTunnel(ctx, tunnel.ID, TunnelPatchInput{Protocol: &protocol})
	if err != nil {
		t.Fatalf("UpdateTunnel(protocol) error = %v", err)
	}
	if patched.Protocol != tunnelruntime.TunnelProtocolTCP || patched.ServerPort == nil || patched.Options.HTTP != nil || len(patched.CustomDomains) != 0 || patched.Location != nil {
		t.Fatalf("UpdateTunnel(protocol) = %#v", patched)
	}
	if _, err := plane.CreateTunnel(ctx, second.ID, TunnelMutationInput{Protocol: tunnelruntime.TunnelProtocolHTTP, CustomDomains: []string{"released.example.com"}, LocalPort: 3001}); err != nil {
		t.Fatalf("CreateTunnel(released reservation) error = %v", err)
	}
}

func boolPointer(value bool) *bool { return &value }

func stringPointer(value string) *string { return &value }
