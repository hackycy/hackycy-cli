package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	tunnelruntime "github.com/hackycy/hackycy-cli/internal/tunnelruntime"
	"math"
	"regexp"
	"strings"

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/nodeportpool"
	"github.com/hackycy/hackycy-cli/ent/server/serverclient"
	"github.com/hackycy/hackycy-cli/ent/server/tunnel"
	"github.com/hackycy/hackycy-cli/ent/server/tunnelhttproute"
	sqlite3 "github.com/ncruces/go-sqlite3"
)

type ServerPortRange struct {
	Start int64
	End   int64
}

type TunnelMutationInput struct {
	Protocol      tunnelruntime.TunnelProtocol
	CustomDomains []string
	Location      *string
	ServerPort    *int64
	LocalHost     *string
	LocalPort     int64
	Enabled       *bool
	Label         *string
	Options       *TunnelOptionsInput
}

// TunnelPatchValue distinguishes an omitted field from an explicitly supplied
// null value for the later JSON adapter.
type TunnelPatchValue[T any] struct {
	Value T
}

type TunnelPatchInput struct {
	Protocol      *tunnelruntime.TunnelProtocol
	CustomDomains *[]string
	Location      *TunnelPatchValue[*string]
	ServerPort    *TunnelPatchValue[*int64]
	LocalHost     *string
	LocalPort     *int64
	Enabled       *bool
	Label         *string
	Options       *TunnelOptionsPatchInput
}

type TunnelOptionsPatchInput struct {
	Transport   *TunnelTransportOptionsPatchInput
	HealthCheck *TunnelPatchValue[*TunnelHealthCheckInput]
	HTTP        *TunnelPatchValue[*TunnelHTTPOptionsPatchInput]
}

type TunnelTransportOptionsPatchInput struct {
	UseEncryption        *bool
	UseCompression       *bool
	BandwidthLimit       *TunnelPatchValue[*tunnelruntime.TunnelBandwidthLimit]
	ProxyProtocolVersion *TunnelPatchValue[*string]
}

type TunnelHTTPOptionsPatchInput struct {
	BasicAuth         *TunnelPatchValue[*TunnelBasicAuthPatchInput]
	HostHeaderRewrite *TunnelPatchValue[*string]
	RequestHeaders    *[]tunnelruntime.TunnelHeader
	ResponseHeaders   *[]tunnelruntime.TunnelHeader
}

type TunnelBasicAuthPatchInput struct {
	Username string
	Password *string
}

type TunnelOptionsInput struct {
	Transport   *TunnelTransportOptionsInput
	HealthCheck *TunnelHealthCheckInput
	HTTP        *TunnelHTTPOptionsInput
}

type TunnelTransportOptionsInput struct {
	UseEncryption        *bool
	UseCompression       *bool
	BandwidthLimit       *tunnelruntime.TunnelBandwidthLimit
	ProxyProtocolVersion *string
}

type TunnelHealthCheckInput struct {
	Type            string
	Path            *string
	IntervalSeconds int64
	TimeoutSeconds  int64
	MaxFailed       int64
	Headers         []tunnelruntime.TunnelHeader
}

type TunnelHTTPOptionsInput struct {
	BasicAuth         *tunnelruntime.TunnelBasicAuth
	HostHeaderRewrite *string
	RequestHeaders    []tunnelruntime.TunnelHeader
	ResponseHeaders   []tunnelruntime.TunnelHeader
}

type ServerTunnel struct {
	tunnelruntime.TunnelDefinition
	ClientID string
	NodeID   string
}

const serverDesiredState = "desired_state"

var tunnelHeaderNamePattern = regexp.MustCompile("^[!#$%&'*+.^`|~A-Za-z0-9_-]+$")

func normalizeServerPortRange(input ServerPortRange) (ServerPortRange, error) {
	if input.Start == 0 && input.End == 0 {
		return ServerPortRange{Start: 20000, End: 20100}, nil
	}
	if input.Start < 1 || input.Start > 65535 || input.End < 1 || input.End > 65535 || input.Start > input.End {
		return ServerPortRange{}, serverDomainError("INVALID_CONFIG", "Tunnel server port range must contain ordered ports from 1 through 65535")
	}
	return input, nil
}

func (plane *ServerControlPlane) CreateTunnel(ctx context.Context, clientID string, input TunnelMutationInput) (tunnelruntime.TunnelDefinition, error) {
	tunnelID, err := randomUUID(plane.random)
	if err != nil {
		return tunnelruntime.TunnelDefinition{}, fmt.Errorf("generate Tunnel Definition ID: %w", err)
	}
	timestamp := formatServerTimestamp(plane.now())
	result, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (struct {
		tunnel ServerTunnel
		owner  string
	}, error) {
		client, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		value, err := plane.tunnelValues(ctx, connection, input, client.NodeID)
		if err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		if err := insertTunnel(ctx, connection, tunnelID, clientID, client.NodeID, value, timestamp); err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		if err := incrementDesiredRevision(ctx, connection, clientID); err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		tunnel, err := selectTunnel(ctx, connection, tunnelID)
		if err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		return struct {
			tunnel ServerTunnel
			owner  string
		}{tunnel: tunnel, owner: client.OwnerAccountID}, nil
	})
	if err != nil {
		return tunnelruntime.TunnelDefinition{}, err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverDesiredState, ClientID: clientID, OwnerAccountID: result.owner})
	return result.tunnel.TunnelDefinition, nil
}

func (plane *ServerControlPlane) UpdateTunnel(ctx context.Context, tunnelID string, patch TunnelPatchInput) (tunnelruntime.TunnelDefinition, error) {
	timestamp := formatServerTimestamp(plane.now())
	result, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (struct {
		tunnel ServerTunnel
		owner  string
	}, error) {
		current, err := selectTunnel(ctx, connection, tunnelID)
		if err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		client, err := selectClient(ctx, connection, current.ClientID)
		if err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		values, err := plane.patchTunnelValues(ctx, connection, current, patch)
		if err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		if err := updateTunnel(ctx, connection, tunnelID, current.NodeID, values, timestamp); err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		if err := incrementDesiredRevision(ctx, connection, current.ClientID); err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		updated, err := selectTunnel(ctx, connection, tunnelID)
		if err != nil {
			return struct {
				tunnel ServerTunnel
				owner  string
			}{}, err
		}
		return struct {
			tunnel ServerTunnel
			owner  string
		}{tunnel: updated, owner: client.OwnerAccountID}, nil
	})
	if err != nil {
		return tunnelruntime.TunnelDefinition{}, err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverDesiredState, ClientID: result.tunnel.ClientID, OwnerAccountID: result.owner})
	return result.tunnel.TunnelDefinition, nil
}

func (plane *ServerControlPlane) ListTunnels(ctx context.Context, clientID string) ([]tunnelruntime.TunnelDefinition, error) {
	if _, err := plane.GetClient(ctx, clientID); err != nil {
		return nil, err
	}
	items, err := serverEntForQueryer(plane.database).Tunnel.Query().Where(tunnel.ClientInternalIDEQ(clientID)).Order(tunnel.ByCreatedAt(), tunnel.ByID()).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Tunnel Definitions: %w", err)
	}
	tunnels := make([]tunnelruntime.TunnelDefinition, 0, len(items))
	for _, item := range items {
		mapped, err := serverTunnelFromEnt(item)
		if err != nil {
			return nil, err
		}
		tunnels = append(tunnels, mapped.TunnelDefinition)
	}
	return tunnels, nil
}

func (plane *ServerControlPlane) GetTunnel(ctx context.Context, tunnelID string) (ServerTunnel, error) {
	return selectTunnel(ctx, plane.database, tunnelID)
}

func (plane *ServerControlPlane) GetTunnelForOwner(ctx context.Context, tunnelID, ownerAccountID string) (ServerTunnel, error) {
	tunnel, err := selectTunnelForOwner(ctx, plane.database, tunnelID, ownerAccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return ServerTunnel{}, serverDomainError("NOT_FOUND", "Tunnel Definition was not found")
	}
	return tunnel, err
}

func (plane *ServerControlPlane) Snapshot(ctx context.Context, clientID string) (tunnelruntime.TunnelSnapshot, error) {
	client, err := plane.GetClient(ctx, clientID)
	if err != nil {
		return tunnelruntime.TunnelSnapshot{}, err
	}
	tunnels, err := plane.ListTunnels(ctx, clientID)
	if err != nil {
		return tunnelruntime.TunnelSnapshot{}, err
	}
	return tunnelruntime.TunnelSnapshot{ClientKey: client.ID, Revision: client.DesiredRevision, Tunnels: tunnels}, nil
}

func (plane *ServerControlPlane) DeleteTunnel(ctx context.Context, tunnelID string) error {
	type deletedTunnel struct {
		tunnel ServerTunnel
		owner  string
	}
	deleted, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (deletedTunnel, error) {
		tunnel, err := selectTunnel(ctx, connection, tunnelID)
		if err != nil {
			return deletedTunnel{}, err
		}
		client, err := selectClient(ctx, connection, tunnel.ClientID)
		if err != nil {
			return deletedTunnel{}, err
		}
		if err := serverEntOnConnection(connection).Tunnel.DeleteOneID(tunnelID).Exec(ctx); err != nil {
			return deletedTunnel{}, fmt.Errorf("delete Tunnel Definition: %w", err)
		}
		if err := incrementDesiredRevision(ctx, connection, tunnel.ClientID); err != nil {
			return deletedTunnel{}, err
		}
		return deletedTunnel{tunnel: tunnel, owner: client.OwnerAccountID}, nil
	})
	if err != nil {
		return err
	}
	plane.emit(ServerControlPlaneEvent{Type: serverDesiredState, ClientID: deleted.tunnel.ClientID, OwnerAccountID: deleted.owner})
	return nil
}

func (plane *ServerControlPlane) RecordAppliedRevision(ctx context.Context, clientID string, revision int64, reportedNodeID ...string) error {
	if revision < 0 {
		return serverDomainError("INVALID_REVISION", "Applied Revision must be a non-negative integer")
	}
	type appliedResult struct {
		client  TrustedTunnelClient
		changed bool
	}
	result, err := withImmediateTransaction(ctx, plane.database, func(connection *sql.Conn) (appliedResult, error) {
		client, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return appliedResult{}, err
		}
		if revision > client.DesiredRevision {
			return appliedResult{}, serverDomainError("INVALID_REVISION", "Applied Revision cannot exceed Desired Revision")
		}
		nodeID := client.NodeID
		if len(reportedNodeID) != 0 {
			nodeID = reportedNodeID[0]
		}
		if nodeID != client.NodeID {
			return appliedResult{}, serverDomainError("INVALID_REVISION", "Applied Node does not match current Client assignment")
		}
		if revision < client.LastAppliedRevision || revision == client.LastAppliedRevision && client.LastAppliedNodeID != nil && *client.LastAppliedNodeID == nodeID {
			return appliedResult{client: client}, nil
		}
		if _, err := serverEntOnConnection(connection).ServerClient.UpdateOneID(clientID).SetLastAppliedRevision(revision).SetLastAppliedNodeID(nodeID).Save(ctx); err != nil {
			return appliedResult{}, fmt.Errorf("record Applied Revision: %w", err)
		}
		updated, err := selectClient(ctx, connection, clientID)
		if err != nil {
			return appliedResult{}, err
		}
		return appliedResult{client: updated, changed: true}, nil
	})
	if err != nil {
		return err
	}
	if result.changed {
		plane.emit(ServerControlPlaneEvent{Type: serverClientUpdated, ClientID: result.client.ID, OwnerAccountID: result.client.OwnerAccountID})
	}
	return nil
}

func (plane *ServerControlPlane) tunnelValues(ctx context.Context, connection *sql.Conn, input TunnelMutationInput, nodeID string) (normalizedTunnelValues, error) {
	protocol, err := normalizeTunnelProtocol(input.Protocol)
	if err != nil {
		return normalizedTunnelValues{}, err
	}
	options, err := normalizeTunnelOptions(protocol, input.Options)
	if err != nil {
		return normalizedTunnelValues{}, err
	}
	return plane.tunnelValuesWithOptions(ctx, connection, input, protocol, options, nodeID)
}

func (plane *ServerControlPlane) patchTunnelValues(ctx context.Context, connection *sql.Conn, current ServerTunnel, patch TunnelPatchInput) (normalizedTunnelValues, error) {
	input := tunnelMutationForPatch(current, patch)
	protocol, err := normalizeTunnelProtocol(input.Protocol)
	if err != nil {
		return normalizedTunnelValues{}, err
	}
	options, err := normalizeTunnelPatchOptions(protocol, patch.Options, current.Options)
	if err != nil {
		return normalizedTunnelValues{}, err
	}
	return plane.tunnelValuesWithOptions(ctx, connection, input, protocol, options, current.NodeID)
}

func (plane *ServerControlPlane) tunnelValuesWithOptions(ctx context.Context, connection *sql.Conn, input TunnelMutationInput, protocol tunnelruntime.TunnelProtocol, options tunnelruntime.TunnelOptions, nodeID string) (normalizedTunnelValues, error) {
	label, err := normalizeTunnelLabel(input.Label)
	if err != nil {
		return normalizedTunnelValues{}, err
	}
	localHost, localPort, err := normalizeLocalEndpoint(input.LocalHost, input.LocalPort)
	if err != nil {
		return normalizedTunnelValues{}, err
	}
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	values := normalizedTunnelValues{protocol: protocol, label: label, localHost: localHost, localPort: localPort, enabled: enabled, options: options}
	if protocol == tunnelruntime.TunnelProtocolHTTP {
		domains, err := normalizeCustomDomains(input.CustomDomains)
		if err != nil {
			return normalizedTunnelValues{}, err
		}
		location, err := normalizeHTTPLocation(input.Location)
		if err != nil {
			return normalizedTunnelValues{}, err
		}
		values.customDomains, values.location = domains, location
		return values, nil
	}
	serverPort := input.ServerPort
	pool, err := nodePortPool(ctx, connection, nodeID)
	if err != nil {
		return normalizedTunnelValues{}, err
	}
	if serverPort == nil {
		allocated, err := plane.availablePort(ctx, connection, nodeID, protocol, pool)
		if err != nil {
			return normalizedTunnelValues{}, err
		}
		serverPort = &allocated
	}
	if *serverPort < pool.Start || *serverPort > pool.End {
		return normalizedTunnelValues{}, serverDomainError("PORT_OUTSIDE_POOL", fmt.Sprintf("Server port must be inside %d-%d", pool.Start, pool.End))
	}
	values.serverPort = serverPort
	return values, nil
}

func tunnelMutationForPatch(current ServerTunnel, patch TunnelPatchInput) TunnelMutationInput {
	protocol := current.Protocol
	if patch.Protocol != nil {
		protocol = *patch.Protocol
	}
	localHost := current.LocalHost
	localPort := current.LocalPort
	enabled := current.Enabled
	label := current.Label
	input := TunnelMutationInput{
		Protocol:  protocol,
		LocalHost: &localHost,
		LocalPort: localPort,
		Enabled:   &enabled,
		Label:     &label,
	}
	if patch.LocalHost != nil {
		input.LocalHost = patch.LocalHost
	}
	if patch.LocalPort != nil {
		input.LocalPort = *patch.LocalPort
	}
	if patch.Enabled != nil {
		input.Enabled = patch.Enabled
	}
	if patch.Label != nil {
		input.Label = patch.Label
	}
	if patch.CustomDomains != nil {
		input.CustomDomains = append([]string(nil), (*patch.CustomDomains)...)
	} else if current.Protocol == tunnelruntime.TunnelProtocolHTTP {
		input.CustomDomains = append([]string(nil), current.CustomDomains...)
	}
	if patch.Location != nil {
		input.Location = patch.Location.Value
	} else if current.Protocol == tunnelruntime.TunnelProtocolHTTP && current.Location != nil {
		location := *current.Location
		input.Location = &location
	}
	if patch.ServerPort != nil {
		input.ServerPort = patch.ServerPort.Value
	} else if current.ServerPort != nil {
		serverPort := *current.ServerPort
		input.ServerPort = &serverPort
	}
	return input
}

func nodePortPool(ctx context.Context, connection *sql.Conn, nodeID string) (ServerPortRange, error) {
	item, err := serverEntOnConnection(connection).NodePortPool.Query().Where(nodeportpool.NodeIDEQ(nodeID)).Only(ctx)
	if err != nil {
		return ServerPortRange{}, fmt.Errorf("read Node port pool: %w", err)
	}
	return ServerPortRange{Start: int64(item.PortStart), End: int64(item.PortEnd)}, nil
}

func (plane *ServerControlPlane) availablePort(ctx context.Context, connection *sql.Conn, nodeID string, protocol tunnelruntime.TunnelProtocol, pool ServerPortRange) (int64, error) {
	items, err := serverEntOnConnection(connection).Tunnel.Query().Where(
		tunnel.NodeIDEQ(nodeID), tunnel.ProtocolEQ(tunnel.Protocol(protocol)),
		tunnel.ServerPortGTE(int(pool.Start)), tunnel.ServerPortLTE(int(pool.End)),
	).All(ctx)
	if err != nil {
		return 0, fmt.Errorf("find available Tunnel server port: %w", err)
	}
	reserved := make(map[int64]struct{})
	for _, item := range items {
		if item.ServerPort != nil {
			reserved[int64(*item.ServerPort)] = struct{}{}
		}
	}
	for candidate := pool.Start; candidate <= pool.End; candidate++ {
		if _, found := reserved[candidate]; !found {
			return candidate, nil
		}
	}
	return 0, serverDomainError("PORT_POOL_EXHAUSTED", fmt.Sprintf("No %s server port is available in %d-%d", strings.ToUpper(string(protocol)), pool.Start, pool.End))
}

type normalizedTunnelValues struct {
	protocol      tunnelruntime.TunnelProtocol
	label         string
	customDomains []string
	location      *string
	serverPort    *int64
	localHost     string
	localPort     int64
	enabled       bool
	options       tunnelruntime.TunnelOptions
}

func insertTunnel(ctx context.Context, connection *sql.Conn, tunnelID, clientID, nodeID string, values normalizedTunnelValues, timestamp string) error {
	optionsJSON, customDomains, location, err := encodedTunnelValues(values)
	if err != nil {
		return err
	}
	client := serverEntOnConnection(connection)
	create := client.Tunnel.Create().SetID(tunnelID).
		SetClientInternalID(clientID).SetNodeID(nodeID).SetLabel(values.label).
		SetProtocol(tunnel.Protocol(values.protocol)).SetLocalHost(values.localHost).
		SetLocalPort(int(values.localPort)).SetEnabled(values.enabled).
		SetOptionsJSON(optionsJSON).SetCreatedAt(timestamp).SetUpdatedAt(timestamp)
	if customDomains != nil {
		create.SetCustomDomains(customDomains.(string))
	}
	if location != nil {
		create.SetLocation(location.(string))
	}
	if values.serverPort != nil {
		create.SetServerPort(int(*values.serverPort))
	}
	if _, err := create.Save(ctx); err != nil {
		return mapTunnelPortConstraintError(ctx, client, nodeID, tunnelID, values, err)
	}
	return reserveTunnelHTTPRoutes(ctx, connection, tunnelID, values)
}

func updateTunnel(ctx context.Context, connection *sql.Conn, tunnelID, nodeID string, values normalizedTunnelValues, timestamp string) error {
	optionsJSON, customDomains, location, err := encodedTunnelValues(values)
	if err != nil {
		return err
	}
	client := serverEntOnConnection(connection)
	update := client.Tunnel.UpdateOneID(tunnelID).SetLabel(values.label).
		SetProtocol(tunnel.Protocol(values.protocol)).SetLocalHost(values.localHost).
		SetLocalPort(int(values.localPort)).SetEnabled(values.enabled).
		SetOptionsJSON(optionsJSON).SetUpdatedAt(timestamp)
	if customDomains == nil {
		update.ClearCustomDomains()
	} else {
		update.SetCustomDomains(customDomains.(string))
	}
	if location == nil {
		update.ClearLocation()
	} else {
		update.SetLocation(location.(string))
	}
	if values.serverPort == nil {
		update.ClearServerPort()
	} else {
		update.SetServerPort(int(*values.serverPort))
	}
	if _, err := update.Save(ctx); err != nil {
		return mapTunnelPortConstraintError(ctx, client, nodeID, tunnelID, values, err)
	}
	if _, err := client.TunnelHTTPRoute.Delete().Where(tunnelhttproute.TunnelIDEQ(tunnelID)).Exec(ctx); err != nil {
		return err
	}
	return reserveTunnelHTTPRoutes(ctx, connection, tunnelID, values)
}

func encodedTunnelValues(values normalizedTunnelValues) (string, any, any, error) {
	optionsJSON, err := json.Marshal(values.options)
	if err != nil {
		return "", nil, nil, fmt.Errorf("encode Tunnel Definition options: %w", err)
	}
	if values.protocol != tunnelruntime.TunnelProtocolHTTP {
		return string(optionsJSON), nil, nil, nil
	}
	domainsJSON, err := json.Marshal(values.customDomains)
	if err != nil {
		return "", nil, nil, fmt.Errorf("encode Tunnel Definition domains: %w", err)
	}
	var location any
	if values.location != nil {
		location = *values.location
	}
	return string(optionsJSON), string(domainsJSON), location, nil
}

func reserveTunnelHTTPRoutes(ctx context.Context, connection *sql.Conn, tunnelID string, values normalizedTunnelValues) error {
	if values.protocol != tunnelruntime.TunnelProtocolHTTP {
		return nil
	}
	client := serverEntOnConnection(connection)
	tunnel, err := client.Tunnel.Get(ctx, tunnelID)
	if err != nil {
		return err
	}
	for _, hostname := range values.customDomains {
		location := ""
		if values.location != nil {
			location = *values.location
		}
		routes, err := client.TunnelHTTPRoute.Query().Where(tunnelhttproute.HostnameEQ(hostname)).WithTunnel().All(ctx)
		if err != nil {
			return err
		}
		for _, route := range routes {
			if route.Edges.Tunnel == nil || route.Edges.Tunnel.NodeID != tunnel.NodeID {
				return serverDomainError("RESOURCE_RESERVED", "HTTP hostname belongs to another Node")
			}
		}
		id, err := randomUUID(rand.Reader)
		if err != nil {
			return err
		}
		if _, err := client.TunnelHTTPRoute.Create().SetID(id).SetTunnelID(tunnelID).SetHostname(hostname).SetLocation(location).Save(ctx); err != nil {
			return mapTunnelRouteConstraintError(ctx, client, hostname, location, tunnelID, err)
		}
	}
	return nil
}

func incrementDesiredRevision(ctx context.Context, connection *sql.Conn, clientID string) error {
	if _, err := serverEntOnConnection(connection).ServerClient.UpdateOneID(clientID).AddDesiredRevision(1).Save(ctx); err != nil {
		return fmt.Errorf("increment Desired Revision: %w", err)
	}
	return nil
}

type tunnelQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func selectTunnel(ctx context.Context, queryer tunnelQueryer, tunnelID string) (ServerTunnel, error) {
	item, err := serverEntForQueryer(queryer).Tunnel.Get(ctx, tunnelID)
	if serverent.IsNotFound(err) {
		return ServerTunnel{}, serverDomainError("NOT_FOUND", "Tunnel Definition was not found")
	}
	if err != nil {
		return ServerTunnel{}, fmt.Errorf("read Tunnel Definition: %w", err)
	}
	return serverTunnelFromEnt(item)
}

func selectTunnelForOwner(ctx context.Context, queryer tunnelQueryer, tunnelID, ownerAccountID string) (ServerTunnel, error) {
	item, err := serverEntForQueryer(queryer).Tunnel.Query().Where(tunnel.IDEQ(tunnelID), tunnel.HasClientWith(serverclient.OwnerAccountIDEQ(ownerAccountID))).Only(ctx)
	if serverent.IsNotFound(err) {
		return ServerTunnel{}, sql.ErrNoRows
	}
	if err != nil {
		return ServerTunnel{}, err
	}
	return serverTunnelFromEnt(item)
}

func serverTunnelFromEnt(item *serverent.Tunnel) (ServerTunnel, error) {
	result := ServerTunnel{ClientID: item.ClientInternalID, NodeID: item.NodeID}
	result.ID, result.Label = item.ID, item.Label
	result.Protocol = tunnelruntime.TunnelProtocol(item.Protocol)
	result.LocalHost, result.LocalPort = item.LocalHost, int64(item.LocalPort)
	result.Enabled, result.CreatedAt, result.UpdatedAt = item.Enabled, item.CreatedAt, item.UpdatedAt
	result.Location = item.Location
	if item.ServerPort != nil {
		port := int64(*item.ServerPort)
		result.ServerPort = &port
	}
	if err := json.Unmarshal([]byte(item.OptionsJSON), &result.Options); err != nil {
		return ServerTunnel{}, fmt.Errorf("decode Tunnel Definition options: %w", err)
	}
	if result.Protocol == tunnelruntime.TunnelProtocolHTTP {
		if item.CustomDomains == nil {
			return ServerTunnel{}, errors.New("HTTP Tunnel Definition has no custom domains")
		}
		if err := json.Unmarshal([]byte(*item.CustomDomains), &result.CustomDomains); err != nil {
			return ServerTunnel{}, fmt.Errorf("decode HTTP Tunnel Definition domains: %w", err)
		}
	}
	return result, nil
}

type tunnelScanner interface {
	Scan(...any) error
}

func scanTunnel(scanner tunnelScanner) (ServerTunnel, error) {
	var tunnel ServerTunnel
	var protocol string
	var customDomains sql.NullString
	var location sql.NullString
	var serverPort sql.NullInt64
	var enabled int
	var optionsJSON string
	if err := scanner.Scan(&tunnel.ID, &tunnel.ClientID, &tunnel.NodeID, &tunnel.Label, &protocol, &customDomains, &location, &serverPort, &tunnel.LocalHost, &tunnel.LocalPort, &enabled, &optionsJSON, &tunnel.CreatedAt, &tunnel.UpdatedAt); err != nil {
		return ServerTunnel{}, err
	}
	tunnel.Protocol = tunnelruntime.TunnelProtocol(protocol)
	tunnel.Enabled = enabled == 1
	if err := json.Unmarshal([]byte(optionsJSON), &tunnel.Options); err != nil {
		return ServerTunnel{}, fmt.Errorf("decode Tunnel Definition options: %w", err)
	}
	if tunnel.Protocol == tunnelruntime.TunnelProtocolHTTP {
		if !customDomains.Valid {
			return ServerTunnel{}, errors.New("HTTP Tunnel Definition has no custom domains")
		}
		if err := json.Unmarshal([]byte(customDomains.String), &tunnel.CustomDomains); err != nil {
			return ServerTunnel{}, fmt.Errorf("decode HTTP Tunnel Definition domains: %w", err)
		}
		if location.Valid {
			tunnel.Location = &location.String
		}
		return tunnel, nil
	}
	if !serverPort.Valid {
		return ServerTunnel{}, errors.New("Port Tunnel Definition has no server port")
	}
	tunnel.ServerPort = &serverPort.Int64
	return tunnel, nil
}

func normalizeTunnelProtocol(protocol tunnelruntime.TunnelProtocol) (tunnelruntime.TunnelProtocol, error) {
	if protocol != tunnelruntime.TunnelProtocolHTTP && protocol != tunnelruntime.TunnelProtocolTCP && protocol != tunnelruntime.TunnelProtocolUDP {
		return "", serverDomainError("INVALID_TUNNEL", "Tunnel protocol must be http, tcp, or udp")
	}
	return protocol, nil
}

func normalizeTunnelOptions(protocol tunnelruntime.TunnelProtocol, input *TunnelOptionsInput) (tunnelruntime.TunnelOptions, error) {
	transportInput := (*TunnelTransportOptionsInput)(nil)
	if input != nil {
		transportInput = input.Transport
	}
	transport, err := normalizeTunnelTransportOptions(transportInput)
	if err != nil {
		return tunnelruntime.TunnelOptions{}, err
	}
	var healthInput *TunnelHealthCheckInput
	if input != nil {
		healthInput = input.HealthCheck
	}
	health, err := normalizeTunnelHealthCheck(healthInput)
	if err != nil {
		return tunnelruntime.TunnelOptions{}, err
	}
	if protocol != tunnelruntime.TunnelProtocolHTTP {
		return tunnelruntime.TunnelOptions{Transport: transport, HealthCheck: health}, nil
	}
	var httpInput *TunnelHTTPOptionsInput
	if input != nil {
		httpInput = input.HTTP
	}
	httpOptions, err := normalizeTunnelHTTPOptions(httpInput)
	if err != nil {
		return tunnelruntime.TunnelOptions{}, err
	}
	return tunnelruntime.TunnelOptions{Transport: transport, HealthCheck: health, HTTP: httpOptions}, nil
}

func normalizeTunnelPatchOptions(protocol tunnelruntime.TunnelProtocol, input *TunnelOptionsPatchInput, current tunnelruntime.TunnelOptions) (tunnelruntime.TunnelOptions, error) {
	transport := cloneTunnelTransportOptions(current.Transport)
	if input != nil && input.Transport != nil {
		patch := input.Transport
		if patch.UseEncryption != nil {
			transport.UseEncryption = *patch.UseEncryption
		}
		if patch.UseCompression != nil {
			transport.UseCompression = *patch.UseCompression
		}
		if patch.BandwidthLimit != nil {
			limit, err := normalizeTunnelBandwidthLimit(patch.BandwidthLimit.Value)
			if err != nil {
				return tunnelruntime.TunnelOptions{}, err
			}
			transport.BandwidthLimit = limit
		}
		if patch.ProxyProtocolVersion != nil {
			version, err := normalizeTunnelProxyProtocolVersion(patch.ProxyProtocolVersion.Value)
			if err != nil {
				return tunnelruntime.TunnelOptions{}, err
			}
			transport.ProxyProtocolVersion = version
		}
	}
	healthCheck := cloneTunnelHealthCheck(current.HealthCheck)
	if input != nil && input.HealthCheck != nil {
		if input.HealthCheck.Value == nil {
			healthCheck = nil
		} else {
			value, err := normalizeTunnelHealthCheck(input.HealthCheck.Value)
			if err != nil {
				return tunnelruntime.TunnelOptions{}, err
			}
			healthCheck = value
		}
	}
	if protocol != tunnelruntime.TunnelProtocolHTTP {
		return tunnelruntime.TunnelOptions{Transport: transport, HealthCheck: healthCheck}, nil
	}
	httpOptions := cloneTunnelHTTPOptions(current.HTTP)
	if input != nil && input.HTTP != nil {
		if input.HTTP.Value == nil {
			httpOptions = defaultTunnelHTTPOptions()
		} else {
			var err error
			httpOptions, err = applyTunnelHTTPOptionsPatch(httpOptions, input.HTTP.Value)
			if err != nil {
				return tunnelruntime.TunnelOptions{}, err
			}
		}
	}
	return tunnelruntime.TunnelOptions{Transport: transport, HealthCheck: healthCheck, HTTP: httpOptions}, nil
}

func cloneTunnelTransportOptions(source tunnelruntime.TunnelTransportOptions) tunnelruntime.TunnelTransportOptions {
	copy := source
	if source.BandwidthLimit != nil {
		limit := *source.BandwidthLimit
		copy.BandwidthLimit = &limit
	}
	if source.ProxyProtocolVersion != nil {
		version := *source.ProxyProtocolVersion
		copy.ProxyProtocolVersion = &version
	}
	return copy
}

func cloneTunnelHealthCheck(source *tunnelruntime.TunnelHealthCheck) *tunnelruntime.TunnelHealthCheck {
	if source == nil {
		return nil
	}
	copy := *source
	if source.Path != nil {
		path := *source.Path
		copy.Path = &path
	}
	copy.Headers = cloneTunnelHeaders(source.Headers)
	return &copy
}

func defaultTunnelHTTPOptions() *tunnelruntime.TunnelHTTPOptions {
	return &tunnelruntime.TunnelHTTPOptions{RequestHeaders: []tunnelruntime.TunnelHeader{}, ResponseHeaders: []tunnelruntime.TunnelHeader{}}
}

func cloneTunnelHTTPOptions(source *tunnelruntime.TunnelHTTPOptions) *tunnelruntime.TunnelHTTPOptions {
	if source == nil {
		return defaultTunnelHTTPOptions()
	}
	copy := *source
	if source.BasicAuth != nil {
		basicAuth := *source.BasicAuth
		copy.BasicAuth = &basicAuth
	}
	if source.HostHeaderRewrite != nil {
		hostHeaderRewrite := *source.HostHeaderRewrite
		copy.HostHeaderRewrite = &hostHeaderRewrite
	}
	copy.RequestHeaders = cloneTunnelHeaders(source.RequestHeaders)
	copy.ResponseHeaders = cloneTunnelHeaders(source.ResponseHeaders)
	return &copy
}

func cloneTunnelHeaders(source []tunnelruntime.TunnelHeader) []tunnelruntime.TunnelHeader {
	if len(source) == 0 {
		return []tunnelruntime.TunnelHeader{}
	}
	return append([]tunnelruntime.TunnelHeader(nil), source...)
}

func normalizeTunnelBandwidthLimit(input *tunnelruntime.TunnelBandwidthLimit) (*tunnelruntime.TunnelBandwidthLimit, error) {
	if input == nil {
		return nil, nil
	}
	if math.IsNaN(input.Value) || math.IsInf(input.Value, 0) || input.Value <= 0 || (input.Unit != "KB" && input.Unit != "MB") || (input.Mode != "client" && input.Mode != "server") {
		return nil, serverDomainError("INVALID_TUNNEL", "Bandwidth limit is invalid")
	}
	limit := *input
	return &limit, nil
}

func normalizeTunnelProxyProtocolVersion(input *string) (*string, error) {
	if input == nil {
		return nil, nil
	}
	if *input != "v1" && *input != "v2" {
		return nil, serverDomainError("INVALID_TUNNEL", "Proxy Protocol version is invalid")
	}
	version := *input
	return &version, nil
}

func applyTunnelHTTPOptionsPatch(current *tunnelruntime.TunnelHTTPOptions, patch *TunnelHTTPOptionsPatchInput) (*tunnelruntime.TunnelHTTPOptions, error) {
	options := cloneTunnelHTTPOptions(current)
	if patch.BasicAuth != nil {
		basicAuth, err := normalizeTunnelBasicAuthPatch(patch.BasicAuth.Value, options.BasicAuth)
		if err != nil {
			return nil, err
		}
		options.BasicAuth = basicAuth
	}
	if patch.HostHeaderRewrite != nil {
		hostHeaderRewrite, err := normalizeTunnelHostHeaderRewrite(patch.HostHeaderRewrite.Value)
		if err != nil {
			return nil, err
		}
		options.HostHeaderRewrite = hostHeaderRewrite
	}
	if patch.RequestHeaders != nil {
		requestHeaders, err := normalizeTunnelHeaders(*patch.RequestHeaders, "Request headers")
		if err != nil {
			return nil, err
		}
		options.RequestHeaders = requestHeaders
	}
	if patch.ResponseHeaders != nil {
		responseHeaders, err := normalizeTunnelHeaders(*patch.ResponseHeaders, "Response headers")
		if err != nil {
			return nil, err
		}
		options.ResponseHeaders = responseHeaders
	}
	return options, nil
}

func normalizeTunnelBasicAuthPatch(input *TunnelBasicAuthPatchInput, current *tunnelruntime.TunnelBasicAuth) (*tunnelruntime.TunnelBasicAuth, error) {
	if input == nil {
		return nil, nil
	}
	username := strings.TrimSpace(input.Username)
	password := ""
	if input.Password != nil {
		password = *input.Password
	} else if current != nil {
		password = current.Password
	}
	if username == "" || utf16CodeUnitCount(username) > 256 || utf16CodeUnitCount(password) < 1 || utf16CodeUnitCount(password) > 256 {
		return nil, serverDomainError("INVALID_TUNNEL", "HTTP Basic Auth requires a username and password of at most 256 characters")
	}
	return &tunnelruntime.TunnelBasicAuth{Username: username, Password: password}, nil
}

func normalizeTunnelHostHeaderRewrite(input *string) (*string, error) {
	if input == nil {
		return nil, nil
	}
	value := strings.TrimSpace(*input)
	if value == "" || utf16CodeUnitCount(value) > 1024 || strings.ContainsAny(value, "\r\n") {
		return nil, serverDomainError("INVALID_TUNNEL", "Host Header Rewrite is invalid")
	}
	return &value, nil
}

func normalizeTunnelTransportOptions(input *TunnelTransportOptionsInput) (tunnelruntime.TunnelTransportOptions, error) {
	transport := tunnelruntime.TunnelTransportOptions{}
	if input == nil {
		return transport, nil
	}
	if input.UseEncryption != nil {
		transport.UseEncryption = *input.UseEncryption
	}
	if input.UseCompression != nil {
		transport.UseCompression = *input.UseCompression
	}
	if input.BandwidthLimit != nil {
		if math.IsNaN(input.BandwidthLimit.Value) || math.IsInf(input.BandwidthLimit.Value, 0) || input.BandwidthLimit.Value <= 0 || (input.BandwidthLimit.Unit != "KB" && input.BandwidthLimit.Unit != "MB") || (input.BandwidthLimit.Mode != "client" && input.BandwidthLimit.Mode != "server") {
			return tunnelruntime.TunnelTransportOptions{}, serverDomainError("INVALID_TUNNEL", "Bandwidth limit is invalid")
		}
		limit := *input.BandwidthLimit
		transport.BandwidthLimit = &limit
	}
	if input.ProxyProtocolVersion != nil {
		if *input.ProxyProtocolVersion != "v1" && *input.ProxyProtocolVersion != "v2" {
			return tunnelruntime.TunnelTransportOptions{}, serverDomainError("INVALID_TUNNEL", "Proxy Protocol version is invalid")
		}
		version := *input.ProxyProtocolVersion
		transport.ProxyProtocolVersion = &version
	}
	return transport, nil
}

func normalizeTunnelHealthCheck(input *TunnelHealthCheckInput) (*tunnelruntime.TunnelHealthCheck, error) {
	if input == nil {
		return nil, nil
	}
	if input.IntervalSeconds < 1 || input.TimeoutSeconds < 1 || input.MaxFailed < 1 {
		return nil, serverDomainError("INVALID_TUNNEL", "Tunnel health check values must be positive integers")
	}
	if input.Type == "tcp" {
		return &tunnelruntime.TunnelHealthCheck{Type: "tcp", IntervalSeconds: input.IntervalSeconds, TimeoutSeconds: input.TimeoutSeconds, MaxFailed: input.MaxFailed}, nil
	}
	if input.Type != "http" || input.Path == nil {
		return nil, serverDomainError("INVALID_TUNNEL", "HTTP health check path must begin with / and contain no spaces")
	}
	path := strings.TrimSpace(*input.Path)
	if !strings.HasPrefix(path, "/") || containsWhitespace(path) {
		return nil, serverDomainError("INVALID_TUNNEL", "HTTP health check path must begin with / and contain no spaces")
	}
	headers, err := normalizeTunnelHeaders(input.Headers, "Health check headers")
	if err != nil {
		return nil, err
	}
	return &tunnelruntime.TunnelHealthCheck{Type: "http", Path: &path, IntervalSeconds: input.IntervalSeconds, TimeoutSeconds: input.TimeoutSeconds, MaxFailed: input.MaxFailed, Headers: headers}, nil
}

func normalizeTunnelHTTPOptions(input *TunnelHTTPOptionsInput) (*tunnelruntime.TunnelHTTPOptions, error) {
	options := &tunnelruntime.TunnelHTTPOptions{RequestHeaders: []tunnelruntime.TunnelHeader{}, ResponseHeaders: []tunnelruntime.TunnelHeader{}}
	if input == nil {
		return options, nil
	}
	if input.BasicAuth != nil {
		username := strings.TrimSpace(input.BasicAuth.Username)
		if username == "" || utf16CodeUnitCount(username) > 256 || utf16CodeUnitCount(input.BasicAuth.Password) < 1 || utf16CodeUnitCount(input.BasicAuth.Password) > 256 {
			return nil, serverDomainError("INVALID_TUNNEL", "HTTP Basic Auth requires a username and password of at most 256 characters")
		}
		options.BasicAuth = &tunnelruntime.TunnelBasicAuth{Username: username, Password: input.BasicAuth.Password}
	}
	if input.HostHeaderRewrite != nil {
		value := strings.TrimSpace(*input.HostHeaderRewrite)
		if value == "" || utf16CodeUnitCount(value) > 1024 || strings.ContainsAny(value, "\r\n") {
			return nil, serverDomainError("INVALID_TUNNEL", "Host Header Rewrite is invalid")
		}
		options.HostHeaderRewrite = &value
	}
	requestHeaders, err := normalizeTunnelHeaders(input.RequestHeaders, "Request headers")
	if err != nil {
		return nil, err
	}
	responseHeaders, err := normalizeTunnelHeaders(input.ResponseHeaders, "Response headers")
	if err != nil {
		return nil, err
	}
	options.RequestHeaders = requestHeaders
	options.ResponseHeaders = responseHeaders
	return options, nil
}

func normalizeTunnelHeaders(headers []tunnelruntime.TunnelHeader, label string) ([]tunnelruntime.TunnelHeader, error) {
	if len(headers) > 32 {
		return nil, serverDomainError("INVALID_TUNNEL", label+" accepts at most 32 headers")
	}
	normalized := make([]tunnelruntime.TunnelHeader, 0, len(headers))
	seen := make(map[string]struct{}, len(headers))
	for _, header := range headers {
		name := strings.TrimSpace(header.Name)
		value := strings.TrimSpace(header.Value)
		if name == "" || utf16CodeUnitCount(name) > 128 || !tunnelHeaderNamePattern.MatchString(name) {
			return nil, serverDomainError("INVALID_TUNNEL", label+" contains an invalid header name")
		}
		if utf16CodeUnitCount(value) > 4096 || strings.ContainsAny(value, "\r\n") {
			return nil, serverDomainError("INVALID_TUNNEL", label+" contains an invalid header value")
		}
		key := strings.ToLower(name)
		if _, found := seen[key]; found {
			return nil, serverDomainError("INVALID_TUNNEL", label+" contains a duplicate header name")
		}
		seen[key] = struct{}{}
		normalized = append(normalized, tunnelruntime.TunnelHeader{Name: name, Value: value})
	}
	return normalized, nil
}

func boolToSQLite(value bool) int {
	if value {
		return 1
	}
	return 0
}

func mapTunnelPortConstraintError(ctx context.Context, client *serverent.Client, nodeID, tunnelID string, values normalizedTunnelValues, err error) error {
	if errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) && values.serverPort != nil {
		reserved, checkErr := client.Tunnel.Query().Where(
			tunnel.NodeIDEQ(nodeID), tunnel.ProtocolEQ(tunnel.Protocol(values.protocol)),
			tunnel.ServerPortEQ(int(*values.serverPort)), tunnel.IDNEQ(tunnelID),
		).Exist(ctx)
		if checkErr != nil {
			return fmt.Errorf("check Tunnel port conflict: %w", checkErr)
		}
		if reserved {
			return serverDomainError("RESOURCE_RESERVED", "Port Tunnel protocol and server port are already reserved")
		}
	}
	return fmt.Errorf("write Tunnel Definition: %w", err)
}

func mapTunnelRouteConstraintError(ctx context.Context, client *serverent.Client, hostname, location, tunnelID string, err error) error {
	if errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) {
		reserved, checkErr := client.TunnelHTTPRoute.Query().Where(
			tunnelhttproute.HostnameEQ(hostname), tunnelhttproute.LocationEQ(location),
			tunnelhttproute.TunnelIDNEQ(tunnelID),
		).Exist(ctx)
		if checkErr != nil {
			return fmt.Errorf("check HTTP route conflict: %w", checkErr)
		}
		if reserved {
			return serverDomainError("RESOURCE_RESERVED", "HTTP Tunnel custom domain and location are already reserved")
		}
	}
	return fmt.Errorf("write HTTP Tunnel route: %w", err)
}
