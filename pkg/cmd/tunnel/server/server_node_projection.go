package server

import (
	"context"

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/node"
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
	items, err := serverEntForQueryer(service.registry.database).Node.Query().Order(node.ByKind(), node.ByName(), node.ByID()).All(ctx)
	if err != nil {
		return nil, err
	}
	summaries := make([]serverNodeSummary, 0, len(items))
	for _, item := range items {
		summary, err := service.summary(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func (service *serverNodeService) summary(ctx context.Context, nodeID string) (serverNodeSummary, error) {
	item, err := serverEntForQueryer(service.registry.database).Node.Get(ctx, nodeID)
	if serverent.IsNotFound(err) {
		return serverNodeSummary{}, serverDomainError("NOT_FOUND", "Node not found")
	}
	if err != nil {
		return serverNodeSummary{}, err
	}
	summary := serverNodeSummary{ID: item.ID, Name: item.Name, Kind: string(item.Kind), Lifecycle: string(item.Lifecycle)}
	if item.AdvertisedFrpHost != nil && item.AdvertisedFrpPort != nil {
		summary.AdvertisedFRPAddress = &serverNodeEndpoint{Host: *item.AdvertisedFrpHost, Port: int64(*item.AdvertisedFrpPort)}
	}
	if item.HTTPIngressHost != nil && item.HTTPIngressPort != nil {
		summary.HTTPIngressAddress = &serverNodeEndpoint{Host: *item.HTTPIngressHost, Port: int64(*item.HTTPIngressPort)}
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
