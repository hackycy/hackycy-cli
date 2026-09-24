package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

type serverNodePreview struct {
	ID          string `json:"previewId"`
	Address     string `json:"managementAddress"`
	Fingerprint string `json:"nodeFingerprint"`
	ExpiresAt   string `json:"expiresAt"`
	publicKey   []byte
	session     string
	expires     time.Time
}

type serverNodeService struct {
	registry     *serverNodeRegistry
	observations *serverNodeObservations
	coordinator  *serverNodeCoordinator
	mu           sync.Mutex
	previews     map[string]serverNodePreview
	localState   ServerHTTPStateProvider
}

func newServerNodeService(registry *serverNodeRegistry, observations *serverNodeObservations, coordinator *serverNodeCoordinator) *serverNodeService {
	return &serverNodeService{registry: registry, observations: observations, coordinator: coordinator, previews: make(map[string]serverNodePreview)}
}

func (service *serverNodeService) preview(ctx context.Context, sessionToken, address string) (serverNodePreview, error) {
	normalized, err := normalizeNodeManagementAddress(address)
	if err != nil {
		return serverNodePreview{}, err
	}
	peer, err := service.coordinator.wire.preview(ctx, normalized)
	if err != nil {
		return serverNodePreview{}, err
	}
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return serverNodePreview{}, err
	}
	now := time.Now()
	preview := serverNodePreview{ID: hex.EncodeToString(bytes), Address: normalized, Fingerprint: peer.Fingerprint, ExpiresAt: now.Add(60 * time.Second).UTC().Format(time.RFC3339), publicKey: peer.PublicKey, session: sessionToken, expires: now.Add(60 * time.Second)}
	service.mu.Lock()
	for id, existing := range service.previews {
		if !now.Before(existing.expires) {
			delete(service.previews, id)
		}
	}
	service.previews[preview.ID] = preview
	service.mu.Unlock()
	return preview, nil
}

func (service *serverNodeService) register(ctx context.Context, sessionToken, previewID, name, confirmedFingerprint, mode string) (serverNodeRecord, error) {
	service.mu.Lock()
	preview, found := service.previews[previewID]
	if found {
		delete(service.previews, previewID)
	}
	service.mu.Unlock()
	if !found || preview.session != sessionToken || !time.Now().Before(preview.expires) {
		return serverNodeRecord{}, serverDomainError("NODE_PREVIEW_EXPIRED", "Preview Node identity again before confirming")
	}
	if confirmedFingerprint != preview.Fingerprint {
		return serverNodeRecord{}, serverDomainError("NODE_IDENTITY_MISMATCH", "Confirmed fingerprint differs from preview")
	}
	var nodeID string
	var highestRevision int64
	switch mode {
	case "claim":
		var err error
		nodeID, err = service.coordinator.wire.claim(ctx, preview.Address, service.coordinator.privateKey, preview.publicKey)
		if err != nil {
			return serverNodeRecord{}, err
		}
	case "readd":
		var status nodeStatus
		var err error
		nodeID, status, err = service.coordinator.wire.status(ctx, preview.Address, service.coordinator.privateKey, preview.publicKey)
		if err != nil {
			return serverNodeRecord{}, err
		}
		highestRevision = status.HighestAcceptedRevision
	default:
		return serverNodeRecord{}, serverDomainError("INVALID_REQUEST", "Node registration mode is invalid")
	}
	if len(nodeID) != 32 {
		return serverNodeRecord{}, serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node identity is invalid")
	}
	record, err := service.registry.register(ctx, nodeID, name, preview.Address, preview.publicKey, highestRevision)
	if err != nil {
		if mode == "claim" {
			var domain *ServerDomainError
			if !errors.As(err, &domain) || domain.Code != "NODE_ALREADY_REGISTERED" {
				return serverNodeRecord{}, serverDomainError("NODE_CLAIM_OUTCOME_UNKNOWN", "Node was bound but Server record could not be saved; preview and explicitly re-add")
			}
		}
		return serverNodeRecord{}, err
	}
	service.coordinator.Wake()
	service.observations.notify()
	return record, nil
}

func (service *serverNodeService) patch(ctx context.Context, nodeID string, patch serverNodeMetadataPatch) (serverNodeRecord, error) {
	events, err := service.registry.patchMetadataWithEvents(ctx, nodeID, patch)
	if err != nil {
		return serverNodeRecord{}, err
	}
	if service.coordinator.controlPlane != nil {
		for _, event := range events {
			service.coordinator.controlPlane.emit(event)
		}
	}
	service.coordinator.Wake()
	service.observations.notify()
	return service.registry.get(ctx, nodeID)
}

func (service *serverNodeService) saveDesired(ctx context.Context, nodeID string, expectedRevision int64, settings serverNodeSettings) (int64, error) {
	revision, err := service.registry.saveDesired(ctx, nodeID, expectedRevision, settings)
	if err != nil {
		return 0, err
	}
	service.coordinator.Wake()
	service.observations.notify()
	return revision, nil
}

func (service *serverNodeService) reapply(ctx context.Context, nodeID string) (int64, error) {
	revision, err := service.registry.reapply(ctx, nodeID)
	if err != nil {
		return 0, err
	}
	service.coordinator.Wake()
	service.observations.notify()
	return revision, nil
}

func (service *serverNodeService) rotateToken(ctx context.Context, nodeID string, expectedRevision int64) (int64, error) {
	revision, err := service.registry.stageTokenRotation(ctx, nodeID, expectedRevision)
	if err != nil {
		return 0, err
	}
	service.coordinator.Wake()
	service.observations.notify()
	return revision, nil
}
