package room

import (
	"sync"

	"realtime-engine/internal/domain/game"
)

// EventKind names the wire-level shape of a RoomEvent, mirroring the
// transport envelope "type" field documented in the project README once the
// WebSocket layer (Phase 3) is built on top of this package.
type EventKind string

const (
	EventStateDiff          EventKind = "STATE_DIFF"
	EventStateSnapshot      EventKind = "STATE_SNAPSHOT"
	EventPlayerJoined       EventKind = "PLAYER_JOINED"
	EventPlayerDisconnected EventKind = "PLAYER_DISCONNECTED"
	EventPlayerReconnected  EventKind = "PLAYER_RECONNECTED"
	EventRoomStarted        EventKind = "ROOM_STARTED"
	EventRoomClosed         EventKind = "ROOM_CLOSED"
)

// RoomEvent is a single notification the Room actor hands to its Sink. It
// carries enough information for a transport layer to build the wire
// envelope described in the project plan without reaching back into room
// internals.
type RoomEvent struct {
	RoomID    ID
	Kind      EventKind
	ServerSeq uint64
	Diff      game.Diff
	Snapshot  []byte
	Events    []game.Event
	Terminal  bool
	Winner    *game.PlayerID
	Subject   game.PlayerID // the player an event is about (join/disconnect/reconnect)
}

// Sink receives events produced by a Room's actor goroutine. Broadcast
// targets every currently-connected player in the room; Unicast targets
// exactly one player (e.g. a STATE_SNAPSHOT delivered only to a resuming
// client). Implementations must be safe for concurrent use, since a
// RoomManager runs many rooms' actor goroutines concurrently, and must not
// block the calling actor goroutine for long (the WebSocket implementation
// funnels into per-connection buffered channels; see internal/transport/ws).
type Sink interface {
	Broadcast(event RoomEvent)
	Unicast(playerID game.PlayerID, event RoomEvent)
}

// RecordingSink is an in-memory Sink used by tests to assert on what a Room
// broadcast, without needing a real transport layer.
type RecordingSink struct {
	mu        sync.Mutex
	broadcast []RoomEvent
	unicast   []unicastRecord
}

type unicastRecord struct {
	Player game.PlayerID
	Event  RoomEvent
}

func NewRecordingSink() *RecordingSink { return &RecordingSink{} }

func (s *RecordingSink) Broadcast(event RoomEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcast = append(s.broadcast, event)
}

func (s *RecordingSink) Unicast(playerID game.PlayerID, event RoomEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unicast = append(s.unicast, unicastRecord{Player: playerID, Event: event})
}

// Broadcasts returns a snapshot copy of all broadcast events recorded so far.
func (s *RecordingSink) Broadcasts() []RoomEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RoomEvent, len(s.broadcast))
	copy(out, s.broadcast)
	return out
}

// UnicastsFor returns a snapshot copy of all events unicast to playerID.
func (s *RecordingSink) UnicastsFor(playerID game.PlayerID) []RoomEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []RoomEvent
	for _, r := range s.unicast {
		if r.Player == playerID {
			out = append(out, r.Event)
		}
	}
	return out
}
