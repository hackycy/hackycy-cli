package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/node"
	"github.com/hackycy/hackycy-cli/ent/server/nodemanagementcandidate"
	"github.com/hackycy/hackycy-cli/ent/server/serverclient"
	sqlite3 "github.com/ncruces/go-sqlite3"
)

type serverNodeEndpoint struct {
	Host string `json:"host"`
	Port int64  `json:"port"`
}

type serverNodeMetadataPatch struct {
	Name                 *string             `json:"name,omitempty"`
	ManagementAddress    *string             `json:"managementAddress,omitempty"`
	AdvertisedFRPAddress *serverNodeEndpoint `json:"advertisedFrpAddress,omitempty"`
	HTTPIngressAddress   *serverNodeEndpoint `json:"httpIngressAddress,omitempty"`
}

func (registry *serverNodeRegistry) patchMetadata(ctx context.Context, nodeID string, patch serverNodeMetadataPatch) error {
	_, err := registry.patchMetadataWithEvents(ctx, nodeID, patch)
	return err
}

func (registry *serverNodeRegistry) patchMetadataWithEvents(ctx context.Context, nodeID string, patch serverNodeMetadataPatch) ([]ServerControlPlaneEvent, error) {
	var address string
	if patch.ManagementAddress != nil {
		var err error
		address, err = normalizeNodeManagementAddress(*patch.ManagementAddress)
		if err != nil {
			return nil, err
		}
	}
	if patch.Name != nil && (strings.TrimSpace(*patch.Name) == "" || utf16CodeUnitCount(strings.TrimSpace(*patch.Name)) > 100) {
		return nil, serverDomainError("INVALID_NODE", "Node display name is invalid")
	}
	for _, endpoint := range []*serverNodeEndpoint{patch.AdvertisedFRPAddress, patch.HTTPIngressAddress} {
		if endpoint != nil && (!localEndpointHostPattern.MatchString(endpoint.Host) || strings.TrimSpace(endpoint.Host) == "" || !validServerNodePort(endpoint.Port)) {
			return nil, serverDomainError("INVALID_NODE_ENDPOINT", "Node public endpoint is invalid")
		}
	}
	var events []ServerControlPlaneEvent
	_, err := withImmediateTransaction(ctx, registry.database, func(connection *sql.Conn) (struct{}, error) {
		client := serverEntOnConnection(connection)
		current, err := client.Node.Query().Where(node.IDEQ(nodeID), node.KindEQ(node.KindRemote)).WithRemoteNode().Only(ctx)
		if serverent.IsNotFound(err) {
			return struct{}{}, serverDomainError("NOT_FOUND", "Remote Node not found")
		}
		if err != nil {
			return struct{}{}, err
		}
		if current.Edges.RemoteNode == nil {
			return struct{}{}, errors.New("Remote Node record is missing")
		}
		currentAddress := current.Edges.RemoteNode.ManagementAddress
		update := client.Node.UpdateOne(current).SetUpdatedAt(formatServerTimestamp(time.Now()))
		if patch.Name != nil {
			update.SetName(strings.TrimSpace(*patch.Name))
		}
		if patch.AdvertisedFRPAddress != nil {
			host := strings.ToLower(strings.TrimSpace(patch.AdvertisedFRPAddress.Host))
			port := patch.AdvertisedFRPAddress.Port
			update.SetAdvertisedFrpHost(host).SetAdvertisedFrpPort(int(port))
			if current.AdvertisedFrpHost == nil || *current.AdvertisedFrpHost != host || current.AdvertisedFrpPort == nil || int64(*current.AdvertisedFrpPort) != port {
				if _, err := client.ServerClient.Update().Where(serverclient.NodeIDEQ(nodeID)).AddDesiredRevision(1).Save(ctx); err != nil {
					return struct{}{}, err
				}
				assigned, err := client.ServerClient.Query().Where(serverclient.NodeIDEQ(nodeID)).All(ctx)
				if err != nil {
					return struct{}{}, err
				}
				for _, item := range assigned {
					events = append(events, ServerControlPlaneEvent{Type: serverDesiredState, ClientID: item.ID, OwnerAccountID: item.OwnerAccountID})
				}
			}
		}
		if patch.HTTPIngressAddress != nil {
			update.SetHTTPIngressHost(strings.ToLower(strings.TrimSpace(patch.HTTPIngressAddress.Host))).SetHTTPIngressPort(int(patch.HTTPIngressAddress.Port))
		}
		if patch.ManagementAddress != nil {
			candidate, lookupErr := client.NodeManagementCandidate.Query().Where(nodemanagementcandidate.NodeIDEQ(nodeID)).Only(ctx)
			if lookupErr != nil && !serverent.IsNotFound(lookupErr) {
				return struct{}{}, lookupErr
			}
			if address == currentAddress {
				if candidate != nil {
					if err := client.NodeManagementCandidate.DeleteOne(candidate).Exec(ctx); err != nil {
						return struct{}{}, err
					}
				}
			} else if candidate == nil {
				if _, err := client.NodeManagementCandidate.Create().SetNodeID(nodeID).SetCandidateAddress(address).SetFailureCode("").Save(ctx); err != nil {
					return struct{}{}, err
				}
			} else {
				if _, err := client.NodeManagementCandidate.UpdateOne(candidate).SetCandidateAddress(address).SetFailureCode("").Save(ctx); err != nil {
					return struct{}{}, err
				}
			}
		}
		if _, err := update.Save(ctx); err != nil {
			return struct{}{}, mapNodeEndpointConstraintError(ctx, client, nodeID, patch.AdvertisedFRPAddress, err)
		}
		return struct{}{}, nil
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

func mapNodeEndpointConstraintError(ctx context.Context, client *serverent.Client, nodeID string, endpoint *serverNodeEndpoint, err error) error {
	if errors.Is(err, sqlite3.CONSTRAINT_UNIQUE) && endpoint != nil {
		host := strings.ToLower(strings.TrimSpace(endpoint.Host))
		reserved, checkErr := client.Node.Query().Where(
			node.IDNEQ(nodeID), node.LifecycleEQ(node.LifecycleActive),
			node.AdvertisedFrpHostEQ(host), node.AdvertisedFrpPortEQ(int(endpoint.Port)),
		).Exist(ctx)
		if checkErr != nil {
			return fmt.Errorf("check Node FRP endpoint conflict: %w", checkErr)
		}
		if reserved {
			return serverDomainError("NODE_RESOURCE_CONFLICT", "Node public FRP endpoint is already in use")
		}
	}
	return fmt.Errorf("update Remote Node metadata: %w", err)
}
