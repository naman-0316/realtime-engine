package ws

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/ratelimit"
	"realtime-engine/internal/service/room"
)

const writeWait = 10 * time.Second

// Connection wraps one authenticated WebSocket connection. All writes to
// the underlying *websocket.Conn happen exclusively inside writePump's
// goroutine, fed by the buffered `send` channel — gorilla/websocket
// connections are not safe for concurrent writes, so this single-writer
// invariant is load-bearing, not a style choice: Hub.Broadcast (called from
// room actor goroutines), the read pump, and control-message handlers all
// funnel through enqueue() rather than ever calling ws.WriteMessage
// directly.
type Connection struct {
	srv *Server
	ws  *websocket.Conn

	send      chan []byte
	closed    chan struct{}
	closeOnce sync.Once

	playerID  game.PlayerID
	sessionID string

	limiter *ratelimit.TokenBucket

	mu          sync.Mutex
	currentRoom *room.Room
	roomID      room.ID
}

func newConnection(srv *Server, wsConn *websocket.Conn, playerID game.PlayerID, sessionID string) *Connection {
	return &Connection{
		srv:       srv,
		ws:        wsConn,
		send:      make(chan []byte, 32),
		closed:    make(chan struct{}),
		playerID:  playerID,
		sessionID: sessionID,
		limiter:   ratelimit.New(srv.cfg.RateLimitCapacity, srv.cfg.RateLimitRefillRate),
	}
}

// enqueue hands data to the write pump without blocking the caller, which
// may be a room actor goroutine (via Hub.Broadcast/Unicast) that must never
// stall on a slow or wedged client. A client that can't keep up with its
// send buffer gets disconnected rather than allowed to backpressure the
// room.
func (c *Connection) enqueue(data []byte) {
	select {
	case c.send <- data:
	default:
		c.Close()
	}
}

func (c *Connection) sendError(code, message string) {
	data, err := json.Marshal(errorEnvelope(code, message))
	if err != nil {
		return
	}
	c.enqueue(data)
}

// Close is idempotent and safe to call from any goroutine.
func (c *Connection) Close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.ws.Close()
		c.srv.onConnectionClosed(c)
	})
}

func (c *Connection) setRoom(r *room.Room, roomID room.ID) {
	c.mu.Lock()
	c.currentRoom = r
	c.roomID = roomID
	c.mu.Unlock()
}

func (c *Connection) getRoom() (*room.Room, room.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.currentRoom, c.roomID
}

func (c *Connection) readPump() {
	defer c.Close()
	c.ws.SetReadLimit(maxIncomingMessageBytes)
	_ = c.ws.SetReadDeadline(time.Now().Add(c.srv.cfg.PongTimeout))
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(time.Now().Add(c.srv.cfg.PongTimeout))
	})
	for {
		_, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		c.handleMessage(data)
	}
}

func (c *Connection) writePump() {
	ticker := time.NewTicker(c.srv.cfg.PingInterval)
	defer ticker.Stop()
	defer c.Close()
	for {
		select {
		case data, ok := <-c.send:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.ws.WriteMessage(websocket.TextMessage, data); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.closed:
			return
		}
	}
}

func (c *Connection) handleMessage(raw []byte) {
	if !c.limiter.Allow() {
		c.sendError("RATE_LIMITED", "too many messages; slow down")
		return
	}
	env, err := decodeEnvelope(raw)
	if err != nil {
		c.sendError("INVALID_ENVELOPE", err.Error())
		return
	}
	switch env.Type {
	case TypeFindMatch:
		c.handleFindMatch(env)
	case TypeResync:
		c.handleResync()
	default:
		c.handleGameAction(env)
	}
}

type findMatchPayload struct {
	GameType string `json:"gameType"`
}

func (c *Connection) handleFindMatch(env ClientEnvelope) {
	if r, _ := c.getRoom(); r != nil {
		c.sendError("ALREADY_IN_ROOM", "already seated in a room")
		return
	}
	var p findMatchPayload
	if err := json.Unmarshal(env.Payload, &p); err != nil || p.GameType == "" {
		c.sendError("INVALID_PAYLOAD", "FIND_MATCH requires a non-empty gameType")
		return
	}
	c.srv.findMatch(c, p.GameType)
}

func (c *Connection) handleResync() {
	r, _ := c.getRoom()
	if r == nil {
		c.sendError("NOT_IN_ROOM", "not currently seated in a room")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := r.Snapshot(ctx)
	if err != nil {
		c.sendError(errorCode(err), err.Error())
		return
	}
	env := ServerEnvelope{
		Type: string(room.EventStateSnapshot), RoomID: string(r.ID()), ServerSeq: res.ServerSeq,
		Ts: time.Now().UnixMilli(), Snapshot: json.RawMessage(res.Snapshot),
	}
	data, _ := json.Marshal(env)
	c.enqueue(data)
}

func (c *Connection) handleGameAction(env ClientEnvelope) {
	r, _ := c.getRoom()
	if r == nil {
		c.sendError("NOT_IN_ROOM", "not currently seated in a room; send FIND_MATCH first")
		return
	}
	action := game.Action{
		Type:      game.ActionType(env.Type),
		PlayerID:  c.playerID,
		Seq:       env.Seq,
		Timestamp: time.UnixMilli(env.Ts),
		Payload:   env.Payload,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := r.SubmitAction(ctx, action); err != nil {
		c.sendError(errorCode(err), err.Error())
		return
	}
	// On success the room actor already broadcast a STATE_DIFF via Hub;
	// nothing further to send here.
}
