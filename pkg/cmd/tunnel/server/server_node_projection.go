package server

import (
	"context"
	"database/sql"
)

type serverNodeStateView struct {
	State      string `json:"state"`
	ObservedAt string `json:"observedAt,omitempty"`
	Stale      bool   `json:"stale"`
}

type serverNodeSelectability struct {
	Selectable bool    `json:"selectable"`
	Reason     *string `json:"reason"`
}

type serverNodeSummary struct {
	ID                   string                  `json:"id"`
	Name                 string                  `json:"name"`
	Kind                 string                  `json:"kind"`
	Lifecycle            string                  `json:"lifecycle"`
	AdvertisedFRPAddress *serverNodeEndpoint     `json:"advertisedFrpAddress"`
	HTTPIngressAddress   *serverNodeEndpoint     `json:"httpIngressAddress"`
	Management           serverNodeStateView     `json:"management"`
	FRPS                 serverNodeStateView     `json:"frps"`
	LastKnownFRPS        *serverNodeStateView    `json:"lastKnownFrps,omitempty"`
	Selectability        serverNodeSelectability `json:"selectability"`
}

func (service *serverNodeService) listSummaries(ctx context.Context) ([]serverNodeSummary, error) {
	rows, err := service.registry.database.QueryContext(ctx, `SELECT node_id FROM nodes ORDER BY kind,name,node_id`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	summaries := make([]serverNodeSummary, 0, len(ids))
	for _, id := range ids {
		summary, err := service.summary(ctx, id)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func (service *serverNodeService) summary(ctx context.Context, nodeID string) (serverNodeSummary, error) {
	var summary serverNodeSummary
	var frpHost, ingressHost sql.NullString
	var frpPort, ingressPort sql.NullInt64
	err := service.registry.database.QueryRowContext(ctx, `SELECT node_id,name,kind,lifecycle,advertised_frp_host,advertised_frp_port,http_ingress_host,http_ingress_port FROM nodes WHERE node_id=?`, nodeID).Scan(&summary.ID, &summary.Name, &summary.Kind, &summary.Lifecycle, &frpHost, &frpPort, &ingressHost, &ingressPort)
	if err == sql.ErrNoRows {
		return serverNodeSummary{}, serverDomainError("NOT_FOUND", "Node not found")
	}
	if err != nil {
		return serverNodeSummary{}, err
	}
	if frpHost.Valid && frpPort.Valid {
		summary.AdvertisedFRPAddress = &serverNodeEndpoint{Host: frpHost.String, Port: frpPort.Int64}
	}
	if ingressHost.Valid && ingressPort.Valid {
		summary.HTTPIngressAddress = &serverNodeEndpoint{Host: ingressHost.String, Port: ingressPort.Int64}
	}
	if summary.Kind == "local" {
		summary.Management = serverNodeStateView{State: "local"}
		summary.FRPS = serverNodeStateView{State: "unknown", Stale: true}
		if service.localState != nil {
			summary.FRPS = serverNodeStateView{State: string(service.localState.State().FRPS.State)}
		}
	} else {
		observation, err := service.observations.read(ctx, nodeID)
		if err != nil {
			return serverNodeSummary{}, err
		}
		summary.Management = serverNodeStateView{State: observation.ManagementState, ObservedAt: observation.ObservedAt, Stale: observation.Stale}
		summary.FRPS = serverNodeStateView{State: observation.FRPSState, ObservedAt: observation.ObservedAt, Stale: observation.Stale}
		if observation.LastKnown != nil && observation.Stale {
			summary.LastKnownFRPS = &serverNodeStateView{State: observation.LastKnown.FRPSProcess, ObservedAt: observation.LastKnown.ObservedAt, Stale: true}
		}
		record, err := service.registry.get(ctx, nodeID)
		if err != nil {
			return serverNodeSummary{}, err
		}
		if !record.DesiredSnapshot.Valid {
			summary.Selectability.Reason = nodeReason("unconfigured")
		}
	}
	if summary.Lifecycle != "active" {
		summary.Selectability.Reason = nodeReason("removing")
	} else if summary.Selectability.Reason == nil && summary.AdvertisedFRPAddress == nil {
		summary.Selectability.Reason = nodeReason("unconfigured")
	} else if summary.Selectability.Reason == nil && summary.Management.State != "reachable" && summary.Management.State != "local" {
		switch summary.Management.State {
		case "incompatible":
			summary.Selectability.Reason = nodeReason("protocol_incompatible")
		case "identity_mismatch":
			summary.Selectability.Reason = nodeReason("identity_mismatch")
		default:
			summary.Selectability.Reason = nodeReason("node_offline")
		}
	} else if summary.Selectability.Reason == nil && summary.FRPS.State != "running" {
		summary.Selectability.Reason = nodeReason("frps_stopped")
	}
	summary.Selectability.Selectable = summary.Selectability.Reason == nil
	return summary, nil
}

func nodeReason(reason string) *string { return &reason }
