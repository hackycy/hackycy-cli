package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type serverNodeCoordinator struct {
	registry     *serverNodeRegistry
	observations *serverNodeObservations
	controlPlane *ServerControlPlane
	wire         *nodeManagementWire
	privateKey   []byte
	wake         chan struct{}
	cancel       context.CancelFunc
	done         chan struct{}
	startOnce    sync.Once
}

func newServerNodeCoordinator(directory string, registry *serverNodeRegistry, observations *serverNodeObservations) (*serverNodeCoordinator, error) {
	privateKey, err := os.ReadFile(filepath.Join(directory, controllerKeyFileName))
	if err != nil || len(privateKey) != 32 {
		return nil, errors.New("Tunnel Controller identity is unavailable")
	}
	return &serverNodeCoordinator{registry: registry, observations: observations, wire: newNodeManagementWire(), privateKey: privateKey, wake: make(chan struct{}, 1)}, nil
}

func (coordinator *serverNodeCoordinator) Start() {
	coordinator.startOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		coordinator.cancel = cancel
		coordinator.done = make(chan struct{})
		go func() {
			defer close(coordinator.done)
			ticker := time.NewTicker(15 * time.Second)
			defer ticker.Stop()
			for {
				_ = coordinator.reconcile(ctx)
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				case <-coordinator.wake:
				}
			}
		}()
	})
}

func (coordinator *serverNodeCoordinator) Wake() {
	select {
	case coordinator.wake <- struct{}{}:
	default:
	}
}

func (coordinator *serverNodeCoordinator) Close() {
	if coordinator == nil || coordinator.cancel == nil {
		return
	}
	coordinator.cancel()
	<-coordinator.done
}

func (coordinator *serverNodeCoordinator) reconcile(ctx context.Context) error {
	nodes, err := coordinator.registry.listRemote(ctx)
	if err != nil {
		return err
	}
	for _, record := range nodes {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		coordinator.reconcileNode(ctx, record)
	}
	return nil
}

func (coordinator *serverNodeCoordinator) reconcileNode(ctx context.Context, record serverNodeRecord) {
	if record.PendingManagementAddress != "" {
		if coordinator.verifyCandidate(ctx, record) {
			record.ManagementAddress = record.PendingManagementAddress
		}
	}
	nodeID, status, err := coordinator.wire.status(ctx, record.ManagementAddress, coordinator.privateKey, record.PublicKey)
	if err == nil && nodeID != record.ID {
		err = serverDomainError("NODE_IDENTITY_MISMATCH", "Node ID differs from the registered identity")
	}
	if err != nil {
		var domain *ServerDomainError
		code := "NODE_UNREACHABLE"
		if errors.As(err, &domain) {
			code = domain.Code
		}
		_ = coordinator.observations.recordFailure(ctx, record.ID, code)
		return
	}
	coordinator.observeNodeStatus(ctx, record.ID, status)
	if !record.DesiredSnapshot.Valid || !record.DesiredHash.Valid || record.Lifecycle != "active" {
		return
	}
	digest := sha256.Sum256([]byte(record.DesiredSnapshot.String))
	if hex.EncodeToString(digest[:]) != record.DesiredHash.String {
		return
	}
	if status.HighestAcceptedRevision > record.DesiredRevision || status.HighestAcceptedRevision == record.DesiredRevision && status.SHA256 != record.DesiredHash.String {
		return
	}
	if status.HighestAcceptedRevision == record.DesiredRevision && status.Phase != "accepted" && status.Phase != "switching" {
		return
	}
	_, _ = coordinator.wire.applySnapshot(ctx, record.ManagementAddress, coordinator.privateKey, record.PublicKey, record.ID, record.DesiredRevision, []byte(record.DesiredSnapshot.String))
	nodeID, status, err = coordinator.wire.status(ctx, record.ManagementAddress, coordinator.privateKey, record.PublicKey)
	if err == nil && nodeID == record.ID {
		coordinator.observeNodeStatus(ctx, record.ID, status)
	}
}

func (coordinator *serverNodeCoordinator) observeNodeStatus(ctx context.Context, nodeID string, status nodeStatus) {
	_ = coordinator.observations.recordStatus(ctx, nodeID, status)
	promotion, err := coordinator.registry.promoteTokenRotation(ctx, nodeID, status)
	if err != nil || !promotion.Promoted {
		return
	}
	if coordinator.controlPlane != nil {
		for _, event := range promotion.Events {
			coordinator.controlPlane.emit(event)
		}
	}
	coordinator.observations.notify()
}

func (coordinator *serverNodeCoordinator) verifyCandidate(ctx context.Context, record serverNodeRecord) bool {
	nodeID, _, err := coordinator.wire.status(ctx, record.PendingManagementAddress, coordinator.privateKey, record.PublicKey)
	if err == nil && nodeID != record.ID {
		err = serverDomainError("NODE_IDENTITY_MISMATCH", "Node ID differs from the registered identity")
	}
	if err != nil {
		code := "NODE_UNREACHABLE"
		var domain *ServerDomainError
		if errors.As(err, &domain) {
			code = domain.Code
		}
		_, _ = coordinator.registry.database.ExecContext(ctx, `UPDATE node_management_candidates SET failure_code=? WHERE node_id=? AND candidate_address=?`, code, record.ID, record.PendingManagementAddress)
		coordinator.observations.notify()
		return false
	}
	_, err = withImmediateTransaction(ctx, coordinator.registry.database, func(connection *sql.Conn) (struct{}, error) {
		var candidate string
		if err := connection.QueryRowContext(ctx, `SELECT candidate_address FROM node_management_candidates WHERE node_id=?`, record.ID).Scan(&candidate); err != nil || candidate != record.PendingManagementAddress {
			return struct{}{}, errors.New("Node management candidate changed during verification")
		}
		if _, err := connection.ExecContext(ctx, `UPDATE remote_nodes SET management_address=? WHERE node_id=?`, candidate, record.ID); err != nil {
			return struct{}{}, err
		}
		_, err := connection.ExecContext(ctx, `DELETE FROM node_management_candidates WHERE node_id=?`, record.ID)
		return struct{}{}, err
	})
	if err != nil {
		return false
	}
	coordinator.observations.notify()
	return true
}
