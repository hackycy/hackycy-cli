package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	serverent "github.com/hackycy/hackycy-cli/ent/server"
	"github.com/hackycy/hackycy-cli/ent/server/nodeobservation"
)

const nodeObservationFreshness = 30 * time.Second

type serverNodeObservation struct {
	ManagementState string
	FRPSState       string
	ObservedAt      string
	Stale           bool
	LastKnown       *nodeStatus
	FailureCode     string
	Status          *nodeStatus
}

type serverNodeObservations struct {
	database  *sql.DB
	mu        sync.Mutex
	fresh     map[string]time.Time
	listeners map[uint64]func()
	nextID    uint64
	now       func() time.Time
}

func newServerNodeObservations(database *sql.DB) (*serverNodeObservations, error) {
	if database == nil {
		return nil, errors.New("Node observation database is required")
	}
	return &serverNodeObservations{database: database, fresh: make(map[string]time.Time), listeners: make(map[uint64]func()), now: time.Now}, nil
}

func (observations *serverNodeObservations) recordStatus(ctx context.Context, nodeID string, status nodeStatus) error {
	if !status.Claimed || status.ObservedAt == "" {
		return serverDomainError("NODE_PROTOCOL_INCOMPATIBLE", "Node status is invalid")
	}
	contents, err := json.Marshal(status)
	if err != nil {
		return err
	}
	now := observations.now()
	tx, err := serverEntForQueryer(observations.database).Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := tx.NodeObservation.Query().Where(nodeobservation.NodeIDEQ(nodeID)).Only(ctx)
	if serverent.IsNotFound(err) {
		_, err = tx.NodeObservation.Create().SetNodeID(nodeID).SetStatusJSON(string(contents)).
			SetStatusObservedAt(status.ObservedAt).SetFailureCode("").SetAttemptedAt(formatServerTimestamp(now)).Save(ctx)
	} else if err == nil {
		_, err = tx.NodeObservation.UpdateOne(current).SetStatusJSON(string(contents)).
			SetStatusObservedAt(status.ObservedAt).SetFailureCode("").SetAttemptedAt(formatServerTimestamp(now)).Save(ctx)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	observations.mu.Lock()
	observations.fresh[nodeID] = now
	observations.mu.Unlock()
	observations.notify()
	return nil
}

func (observations *serverNodeObservations) recordFailure(ctx context.Context, nodeID, code string) error {
	switch code {
	case "NODE_UNREACHABLE", "NODE_PROTOCOL_INCOMPATIBLE", "NODE_IDENTITY_MISMATCH":
	default:
		code = "NODE_UNREACHABLE"
	}
	now := observations.now()
	tx, err := serverEntForQueryer(observations.database).Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, err := tx.NodeObservation.Query().Where(nodeobservation.NodeIDEQ(nodeID)).Only(ctx)
	if serverent.IsNotFound(err) {
		_, err = tx.NodeObservation.Create().SetNodeID(nodeID).SetFailureCode(code).SetAttemptedAt(formatServerTimestamp(now)).Save(ctx)
	} else if err == nil {
		_, err = tx.NodeObservation.UpdateOne(current).SetFailureCode(code).SetAttemptedAt(formatServerTimestamp(now)).Save(ctx)
	}
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	observations.mu.Lock()
	observations.fresh[nodeID] = now
	observations.mu.Unlock()
	observations.notify()
	return nil
}

func (observations *serverNodeObservations) read(ctx context.Context, nodeID string) (serverNodeObservation, error) {
	item, err := serverEntForQueryer(observations.database).NodeObservation.Query().Where(nodeobservation.NodeIDEQ(nodeID)).Only(ctx)
	if serverent.IsNotFound(err) {
		return serverNodeObservation{ManagementState: "unknown", FRPSState: "unknown", Stale: true}, nil
	}
	if err != nil {
		return serverNodeObservation{}, err
	}
	view := serverNodeObservation{ManagementState: "unknown", FRPSState: "unknown", ObservedAt: item.AttemptedAt, FailureCode: item.FailureCode, Stale: true}
	if item.StatusJSON != nil {
		var status nodeStatus
		if err := json.Unmarshal([]byte(*item.StatusJSON), &status); err != nil {
			return serverNodeObservation{}, fmt.Errorf("read saved Node observation: %w", err)
		}
		view.LastKnown = &status
	}
	observations.mu.Lock()
	lastFresh, fresh := observations.fresh[nodeID]
	observations.mu.Unlock()
	if !fresh || observations.now().Sub(lastFresh) >= nodeObservationFreshness {
		return view, nil
	}
	if item.FailureCode != "" {
		switch item.FailureCode {
		case "NODE_PROTOCOL_INCOMPATIBLE":
			view.ManagementState = "incompatible"
		case "NODE_IDENTITY_MISMATCH":
			view.ManagementState = "identity_mismatch"
		default:
			view.ManagementState = "unreachable"
		}
		return view, nil
	}
	if view.LastKnown == nil {
		return view, nil
	}
	view.ManagementState = "reachable"
	view.Stale = false
	view.Status = view.LastKnown
	if item.StatusObservedAt != nil {
		view.ObservedAt = *item.StatusObservedAt
	}
	switch view.Status.FRPSProcess {
	case "running":
		view.FRPSState = "running"
	case "stopped":
		view.FRPSState = "stopped"
	case "failed":
		view.FRPSState = "failed"
	}
	return view, nil
}

func (observations *serverNodeObservations) subscribe(listener func()) func() {
	observations.mu.Lock()
	observations.nextID++
	id := observations.nextID
	observations.listeners[id] = listener
	observations.mu.Unlock()
	return func() {
		observations.mu.Lock()
		delete(observations.listeners, id)
		observations.mu.Unlock()
	}
}

func (observations *serverNodeObservations) notify() {
	observations.mu.Lock()
	listeners := make([]func(), 0, len(observations.listeners))
	for _, listener := range observations.listeners {
		listeners = append(listeners, listener)
	}
	observations.mu.Unlock()
	for _, listener := range listeners {
		listener()
	}
}
