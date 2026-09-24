package server

import (
	"context"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
)

type serverNodeDesiredView struct {
	Revision int64               `json:"revision"`
	Digest   string              `json:"digest,omitempty"`
	Mode     string              `json:"mode"`
	Settings *serverNodeSettings `json:"settings,omitempty"`
}

type serverNodeObservedError struct {
	Code     string `json:"code"`
	Phase    string `json:"phase"`
	Revision int64  `json:"revision"`
}

type serverNodeObservedView struct {
	HighestAcceptedRevision int64                    `json:"highestAcceptedRevision"`
	AppliedRevision         int64                    `json:"appliedRevision"`
	FailedRevision          int64                    `json:"failedRevision"`
	Configuration           string                   `json:"configuration"`
	FRPS                    string                   `json:"frps"`
	ObservedAt              string                   `json:"observedAt,omitempty"`
	Stale                   bool                     `json:"stale"`
	Error                   *serverNodeObservedError `json:"error,omitempty"`
}

type serverNodeManagementView struct {
	serverNodeSummary
	ManagementAddress        string                 `json:"managementAddress,omitempty"`
	PendingManagementAddress string                 `json:"pendingManagementAddress,omitempty"`
	CandidateError           string                 `json:"candidateError,omitempty"`
	NodeFingerprint          string                 `json:"nodeFingerprint,omitempty"`
	ControllerFingerprint    string                 `json:"controllerFingerprint,omitempty"`
	Desired                  serverNodeDesiredView  `json:"desired"`
	Observed                 serverNodeObservedView `json:"observed"`
	TokenRotation            struct {
		State    string `json:"state"`
		Revision int64  `json:"revision,omitempty"`
	} `json:"tokenRotation"`
	Removal struct {
		State string `json:"state"`
	} `json:"removal"`
}

func (service *serverNodeService) managementView(ctx context.Context, nodeID string) (serverNodeManagementView, error) {
	summary, err := service.summary(ctx, nodeID)
	if err != nil {
		return serverNodeManagementView{}, err
	}
	view := serverNodeManagementView{serverNodeSummary: summary}
	view.TokenRotation.State = "idle"
	view.Removal.State = "none"
	view.Observed = serverNodeObservedView{Configuration: "unknown", FRPS: summary.FRPS.State, Stale: summary.FRPS.Stale}
	if summary.Kind == "local" {
		view.Desired.Mode = "local"
		view.Observed.Configuration = "local"
		return view, nil
	}
	record, err := service.registry.get(ctx, nodeID)
	if err != nil {
		return serverNodeManagementView{}, err
	}
	view.ManagementAddress = record.ManagementAddress
	view.PendingManagementAddress = record.PendingManagementAddress
	view.CandidateError = safeNodeManagementCode(record.CandidateError)
	view.NodeFingerprint = nodeKeyFingerprint(record.PublicKey)
	key, err := ecdh.X25519().NewPrivateKey(service.coordinator.privateKey)
	if err != nil {
		return serverNodeManagementView{}, err
	}
	view.ControllerFingerprint = nodeKeyFingerprint(key.PublicKey().Bytes())
	view.Desired = serverNodeDesiredView{Revision: record.DesiredRevision, Mode: "unconfigured"}
	if record.StagedTokenRevision.Valid {
		view.TokenRotation.State = "pending_node"
		view.TokenRotation.Revision = record.StagedTokenRevision.Int64
	}
	if record.DesiredSnapshot.Valid {
		var snapshot serverDesiredNodeSnapshot
		if err := json.Unmarshal([]byte(record.DesiredSnapshot.String), &snapshot); err != nil {
			return serverNodeManagementView{}, err
		}
		view.Desired.Mode = snapshot.State
		view.Desired.Digest = "sha256:" + record.DesiredHash.String
		view.Desired.Settings = &serverNodeSettings{BindAddress: snapshot.BindAddress, BindPort: snapshot.BindPort, VhostHTTPPort: snapshot.VhostHTTPPort, PortRangeStart: snapshot.PortRangeStart, PortRangeEnd: snapshot.PortRangeEnd, Custom404Page: snapshot.Custom404Page}
	}
	if record.Lifecycle == "removing" {
		view.Removal.State = "pending"
	}
	observation, err := service.observations.read(ctx, nodeID)
	if err != nil {
		return serverNodeManagementView{}, err
	}
	if observation.LastKnown != nil {
		status := observation.LastKnown
		view.Observed.HighestAcceptedRevision = status.HighestAcceptedRevision
		view.Observed.AppliedRevision = status.AppliedRevision
		view.Observed.FailedRevision = status.FailedRevision
		view.Observed.ObservedAt = status.ObservedAt
	}
	if observation.Status == nil {
		return view, nil
	}
	status := observation.Status
	if record.StagedTokenRevision.Valid {
		switch {
		case status.FailedRevision == record.StagedTokenRevision.Int64 && status.Phase == "failed":
			view.TokenRotation.State = "apply_failed"
		case status.AppliedRevision == record.StagedTokenRevision.Int64 && status.HighestAcceptedRevision == record.StagedTokenRevision.Int64 && status.SHA256 == record.DesiredHash.String && status.Phase == "applied":
			view.TokenRotation.State = "publishing_clients"
		}
	}
	view.Observed.Stale = false
	view.Observed.Configuration = nodeConfigurationState(record, *status, summary.FRPS.State)
	if code := safeNodeManagementCode(status.FailureCode); code != "" && status.FailedRevision > 0 {
		view.Observed.Error = &serverNodeObservedError{Code: code, Phase: safeNodePhase(status.Phase), Revision: status.FailedRevision}
	}
	return view, nil
}

func nodeConfigurationState(record serverNodeRecord, status nodeStatus, frps string) string {
	if !record.DesiredSnapshot.Valid {
		return "unconfigured"
	}
	if status.FailedRevision == record.DesiredRevision {
		if status.FailureCode == "NODE_ROLLBACK_FAILED" {
			return "rollback_failed"
		}
		if status.FailureCode == "NODE_CONFIG_REJECTED" {
			return "rejected"
		}
		if frps == "running" {
			return "apply_failed_old_running"
		}
		return "unknown"
	}
	if status.AppliedRevision == record.DesiredRevision && status.SHA256 == record.DesiredHash.String {
		return "converged"
	}
	if status.HighestAcceptedRevision < record.DesiredRevision {
		return "pending"
	}
	return "unknown"
}

func nodeKeyFingerprint(public []byte) string {
	digest := sha256.Sum256(public)
	return "SHA256:" + base64.RawURLEncoding.EncodeToString(digest[:])
}

func safeNodeManagementCode(code string) string {
	switch code {
	case "NODE_UNREACHABLE", "NODE_PROTOCOL_INCOMPATIBLE", "NODE_IDENTITY_MISMATCH", "NODE_CONFIG_REJECTED", "NODE_APPLY_FAILED", "NODE_ROLLBACK_FAILED", "NODE_SNAPSHOT_INVALID", "NODE_REVISION_STALE", "NODE_REVISION_CONFLICT":
		return code
	default:
		return ""
	}
}

func safeNodePhase(phase string) string {
	switch phase {
	case "accepted", "switching", "failed", "applied", "disabled", "disabling":
		return phase
	default:
		return "unknown"
	}
}
