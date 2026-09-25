package server

import (
	"context"
	"database/sql"
	"strings"
	"time"
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
		var currentAddress, currentHost string
		var currentPort sql.NullInt64
		err := connection.QueryRowContext(ctx, `SELECT r.management_address,COALESCE(n.advertised_frp_host,''),n.advertised_frp_port FROM nodes n JOIN remote_nodes r ON r.node_id=n.node_id WHERE n.node_id=?`, nodeID).Scan(&currentAddress, &currentHost, &currentPort)
		if err == sql.ErrNoRows {
			return struct{}{}, serverDomainError("NOT_FOUND", "Remote Node not found")
		}
		if err != nil {
			return struct{}{}, err
		}
		if patch.Name != nil {
			if _, err := connection.ExecContext(ctx, `UPDATE nodes SET name=? WHERE node_id=?`, strings.TrimSpace(*patch.Name), nodeID); err != nil {
				return struct{}{}, err
			}
		}
		if patch.AdvertisedFRPAddress != nil {
			host := strings.ToLower(strings.TrimSpace(patch.AdvertisedFRPAddress.Host))
			port := patch.AdvertisedFRPAddress.Port
			if _, err := connection.ExecContext(ctx, `UPDATE nodes SET advertised_frp_host=?,advertised_frp_port=? WHERE node_id=?`, host, port, nodeID); err != nil {
				return struct{}{}, serverDomainError("NODE_RESOURCE_CONFLICT", "Node public FRP endpoint is already in use")
			}
			if currentHost != host || !currentPort.Valid || currentPort.Int64 != port {
				if _, err := connection.ExecContext(ctx, `UPDATE clients SET desired_revision=desired_revision+1 WHERE node_id=?`, nodeID); err != nil {
					return struct{}{}, err
				}
				rows, err := connection.QueryContext(ctx, `SELECT internal_id,owner_account_id FROM clients WHERE node_id=?`, nodeID)
				if err != nil {
					return struct{}{}, err
				}
				for rows.Next() {
					var event ServerControlPlaneEvent
					if err := rows.Scan(&event.ClientID, &event.OwnerAccountID); err != nil {
						_ = rows.Close()
						return struct{}{}, err
					}
					event.Type = serverDesiredState
					events = append(events, event)
				}
				if err := rows.Err(); err != nil {
					_ = rows.Close()
					return struct{}{}, err
				}
				_ = rows.Close()
			}
		}
		if patch.HTTPIngressAddress != nil {
			if _, err := connection.ExecContext(ctx, `UPDATE nodes SET http_ingress_host=?,http_ingress_port=? WHERE node_id=?`, strings.ToLower(strings.TrimSpace(patch.HTTPIngressAddress.Host)), patch.HTTPIngressAddress.Port, nodeID); err != nil {
				return struct{}{}, err
			}
		}
		if patch.ManagementAddress != nil {
			if address == currentAddress {
				if _, err := connection.ExecContext(ctx, `DELETE FROM node_management_candidates WHERE node_id=?`, nodeID); err != nil {
					return struct{}{}, err
				}
			} else {
				if _, err := connection.ExecContext(ctx, `INSERT INTO node_management_candidates(node_id,candidate_address,failure_code) VALUES(?,?,'') ON CONFLICT(node_id) DO UPDATE SET candidate_address=excluded.candidate_address,failure_code=''`, nodeID, address); err != nil {
					return struct{}{}, err
				}
			}
		}
		_, err = connection.ExecContext(ctx, `UPDATE nodes SET updated_at=? WHERE node_id=?`, formatServerTimestamp(time.Now()), nodeID)
		return struct{}{}, err
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}
