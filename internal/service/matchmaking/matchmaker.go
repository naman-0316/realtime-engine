// Package matchmaking implements a thread-safe join-queue matchmaker on top
// of the service/room package. It is intentionally thin: the queue only
// decides *when enough players are waiting to start a room*; everything
// about seating, starting, and running the game is delegated to
// room.Manager/room.Room.
package matchmaking

import (
	"context"
	"sync"

	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/service/room"
)

// Matchmaker holds one FIFO join-queue per game type and creates a room via
// the room.Manager as soon as a queue has enough players to satisfy that
// game's MaxPlayers.
type Matchmaker struct {
	registry *game.Registry
	rooms    *room.Manager

	mu     sync.Mutex
	queues map[string][]game.PlayerID
}

// New constructs a Matchmaker backed by registry (for reading each game's
// player-count bounds) and rooms (for actually creating matched rooms).
func New(registry *game.Registry, rooms *room.Manager) *Matchmaker {
	return &Matchmaker{
		registry: registry,
		rooms:    rooms,
		queues:   make(map[string][]game.PlayerID),
	}
}

// Enqueue adds player to the queue for gameType. Re-enqueueing an
// already-queued player is a safe no-op (idempotent). If this call fills
// the queue to the game's MaxPlayers, a room is created immediately with
// those players and returned with matched=true; otherwise matched=false and
// the caller should wait (e.g. for a future room-assignment notification
// once the transport layer is wired up in Phase 3).
func (m *Matchmaker) Enqueue(ctx context.Context, gameType string, player game.PlayerID) (r *room.Room, matched bool, err error) {
	g, err := m.registry.New(gameType)
	if err != nil {
		return nil, false, room.ErrUnknownGameType
	}
	capacity := g.MaxPlayers()

	m.mu.Lock()
	q := m.queues[gameType]
	for _, p := range q {
		if p == player {
			m.mu.Unlock()
			return nil, false, nil // already queued
		}
	}
	q = append(q, player)

	if len(q) < capacity {
		m.queues[gameType] = q
		m.mu.Unlock()
		return nil, false, nil
	}

	matchedPlayers := append([]game.PlayerID(nil), q[:capacity]...)
	m.queues[gameType] = append([]game.PlayerID(nil), q[capacity:]...)
	m.mu.Unlock()

	r, err = m.rooms.CreateRoom(ctx, gameType, matchedPlayers)
	if err != nil {
		return nil, false, err
	}
	return r, true, nil
}

// Dequeue idempotently removes player from gameType's queue (e.g. the
// client cancelled matchmaking or disconnected before a match was found).
// Returns true if the player had actually been queued.
func (m *Matchmaker) Dequeue(gameType string, player game.PlayerID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	q := m.queues[gameType]
	for i, p := range q {
		if p == player {
			m.queues[gameType] = append(q[:i], q[i+1:]...)
			return true
		}
	}
	return false
}

// QueueLength reports how many players are currently queued for gameType.
func (m *Matchmaker) QueueLength(gameType string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queues[gameType])
}
