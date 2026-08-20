package ws

import (
	"encoding/json"
	"sync"

	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/service/room"
)

// Hub implements room.Sink, fanning a Room actor's broadcast/unicast
// events out to whichever live WebSocket connections currently represent
// that room's seated players on this node. A Room actor calls Broadcast /
// Unicast synchronously and inline (see internal/service/room/room.go), so
// every method here must never block: it hands bytes to each connection's
// buffered send channel and moves on, letting Connection.enqueue drop and
// disconnect any client that can't keep up rather than stall the room.
type Hub struct {
	mu    sync.RWMutex
	rooms map[room.ID]map[game.PlayerID]*Connection
}

func NewHub() *Hub {
	return &Hub{rooms: make(map[room.ID]map[game.PlayerID]*Connection)}
}

// Register associates playerID's live connection with roomID so future
// broadcasts/unicasts for that room reach it.
func (h *Hub) Register(roomID room.ID, playerID game.PlayerID, c *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	players, ok := h.rooms[roomID]
	if !ok {
		players = make(map[game.PlayerID]*Connection)
		h.rooms[roomID] = players
	}
	players[playerID] = c
}

// Unregister removes playerID's association with roomID, but only if c is
// still the currently registered connection — this avoids a stale close
// from an old connection clobbering a fresher reconnect.
func (h *Hub) Unregister(roomID room.ID, playerID game.PlayerID, c *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	players, ok := h.rooms[roomID]
	if !ok {
		return
	}
	if players[playerID] == c {
		delete(players, playerID)
	}
	if len(players) == 0 {
		delete(h.rooms, roomID)
	}
}

func (h *Hub) connectionsFor(roomID room.ID) map[game.PlayerID]*Connection {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[game.PlayerID]*Connection, len(h.rooms[roomID]))
	for k, v := range h.rooms[roomID] {
		out[k] = v
	}
	return out
}

// Broadcast implements room.Sink.
func (h *Hub) Broadcast(event room.RoomEvent) {
	env := buildEnvelope(event)
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	for _, c := range h.connectionsFor(event.RoomID) {
		c.enqueue(data)
	}
}

// Unicast implements room.Sink.
func (h *Hub) Unicast(playerID game.PlayerID, event room.RoomEvent) {
	env := buildEnvelope(event)
	data, err := json.Marshal(env)
	if err != nil {
		return
	}
	h.mu.RLock()
	c, ok := h.rooms[event.RoomID][playerID]
	h.mu.RUnlock()
	if ok {
		c.enqueue(data)
	}
}
