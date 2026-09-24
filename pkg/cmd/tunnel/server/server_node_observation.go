package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
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
	_, err := database.Exec(`CREATE TABLE IF NOT EXISTS node_observations (
		node_id TEXT PRIMARY KEY REFERENCES nodes(node_id) ON DELETE CASCADE,
		status_json TEXT,
		status_observed_at TEXT,
		failure_code TEXT NOT NULL DEFAULT '',
		attempted_at TEXT NOT NULL
	)`)
	if err != nil {
		return nil, fmt.Errorf("initialize Node observations: %w", err)
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
	_, err = observations.database.ExecContext(ctx, `INSERT INTO node_observations(node_id,status_json,status_observed_at,failure_code,attempted_at) VALUES(?,?,?,'',?) ON CONFLICT(node_id) DO UPDATE SET status_json=excluded.status_json,status_observed_at=excluded.status_observed_at,failure_code='',attempted_at=excluded.attempted_at`, nodeID, string(contents), status.ObservedAt, formatServerTimestamp(now))
	if err != nil {
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
	_, err := observations.database.ExecContext(ctx, `INSERT INTO node_observations(node_id,failure_code,attempted_at) VALUES(?,?,?) ON CONFLICT(node_id) DO UPDATE SET failure_code=excluded.failure_code,attempted_at=excluded.attempted_at`, nodeID, code, formatServerTimestamp(now))
	if err != nil {
		return err
	}
	observations.mu.Lock()
	observations.fresh[nodeID] = now
	observations.mu.Unlock()
	observations.notify()
	return nil
}

func (observations *serverNodeObservations) read(ctx context.Context, nodeID string) (serverNodeObservation, error) {
	var statusJSON, observedAt sql.NullString
	var failureCode, attemptedAt string
	err := observations.database.QueryRowContext(ctx, `SELECT status_json,status_observed_at,failure_code,attempted_at FROM node_observations WHERE node_id=?`, nodeID).Scan(&statusJSON, &observedAt, &failureCode, &attemptedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return serverNodeObservation{ManagementState: "unknown", FRPSState: "unknown", Stale: true}, nil
	}
	if err != nil {
		return serverNodeObservation{}, err
	}
	view := serverNodeObservation{ManagementState: "unknown", FRPSState: "unknown", ObservedAt: attemptedAt, FailureCode: failureCode, Stale: true}
	if statusJSON.Valid {
		var status nodeStatus
		if err := json.Unmarshal([]byte(statusJSON.String), &status); err != nil {
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
	if failureCode != "" {
		switch failureCode {
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
	view.ObservedAt = observedAt.String
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
