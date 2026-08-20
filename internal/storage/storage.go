// Package storage defines the adapter interfaces the service/transport
// layers use for anything that must be shared across nodes: room-ownership
// leases (so exactly one node runs a given room's actor) and session
// recovery records (so a reconnecting client can be routed to the node that
// actually owns its room, even behind a non-sticky load balancer).
//
// internal/storage/memory implements these in-memory for single-node
// operation and tests; internal/storage/redis implements them for
// horizontally-scaled, multi-node deployments (see storage/redis/lock.go
// and session_store.go).
package storage

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned by lookups that find nothing (not a failure of
// the storage backend itself).
var ErrNotFound = errors.New("storage: not found")

// SessionRecord is what gets persisted about a player's session while their
// connection is down, so a reconnect (potentially to a different node) can
// find its way back to the right room.
type SessionRecord struct {
	RoomID    string
	PlayerID  string
	OwnerNode string
	LastSeq   uint64
}

// SessionRecovery persists SessionRecords with a TTL matching the
// disconnect grace window (plus a small buffer), so an expired record
// simply stops existing rather than needing explicit cleanup.
type SessionRecovery interface {
	SaveSession(ctx context.Context, sessionID string, rec SessionRecord, ttl time.Duration) error
	LoadSession(ctx context.Context, sessionID string) (SessionRecord, error) // ErrNotFound if absent/expired
	DeleteSession(ctx context.Context, sessionID string) error
}

// RoomLocator arbitrates which node owns (runs the actor for) a given room
// ID, via a leased key renewed periodically by the owning node. Behind an
// interface so a stronger algorithm (e.g. Redlock across a Redis cluster)
// could replace the single-instance SET NX PX implementation later without
// touching callers.
type RoomLocator interface {
	// AcquireRoomLease attempts to become the owner of roomID. ok is false
	// if another node already holds a live lease.
	AcquireRoomLease(ctx context.Context, roomID, nodeID string, ttl time.Duration) (ok bool, err error)
	// RenewRoomLease extends nodeID's existing lease on roomID. ok is false
	// if nodeID does not currently hold the lease (e.g. it expired).
	RenewRoomLease(ctx context.Context, roomID, nodeID string, ttl time.Duration) (ok bool, err error)
	ReleaseRoomLease(ctx context.Context, roomID, nodeID string) error
	// RoomOwner returns the current owning node, if any live lease exists.
	RoomOwner(ctx context.Context, roomID string) (nodeID string, err error) // ErrNotFound if unowned
}

// EventBus propagates room lifecycle events (e.g. "room closed") across
// nodes so no node's local view goes stale after another node's room
// closes.
type EventBus interface {
	PublishRoomEvent(ctx context.Context, roomID string, payload []byte) error
	// SubscribeRoomEvents returns a channel of raw payloads for roomID and
	// an unsubscribe function the caller must call when done.
	SubscribeRoomEvents(ctx context.Context, roomID string) (<-chan []byte, func(), error)
}
