// Package ws is the WebSocket transport layer: it upgrades authenticated
// HTTP connections, decodes/validates/rate-limits incoming messages, turns
// them into room.Room commands, and fans room broadcasts back out via Hub.
// It knows nothing about any specific game — only the generic
// domain/game.Game and service/room.Room contracts.
package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"realtime-engine/internal/config"
	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/service/matchmaking"
	"realtime-engine/internal/service/room"
	"realtime-engine/internal/service/session"
	"realtime-engine/internal/storage"
)

// Server wires the transport layer to the service layer (room.Manager,
// matchmaking.Matchmaker) and storage layer (session recovery).
type Server struct {
	cfg        config.Config
	registry   *game.Registry
	rooms      *room.Manager
	matchmaker *matchmaking.Matchmaker
	hub        *Hub
	issuer     *session.Issuer
	sessions   storage.SessionRecovery
	upgrader   websocket.Upgrader

	pendingMu sync.Mutex
	pending   map[game.PlayerID]*Connection
	pendingGT map[game.PlayerID]string
}

// NewServer wires the transport layer. hub must be the same *Hub instance
// passed as the room.Sink to room.NewManager when constructing rooms — the
// caller creates it first (see cmd/server/main.go) since it has no
// dependency on the room.Manager or Matchmaker, breaking what would
// otherwise be a construction-order cycle (Manager needs a Sink; Server
// needs a Manager).
func NewServer(cfg config.Config, registry *game.Registry, rooms *room.Manager, mm *matchmaking.Matchmaker, hub *Hub, issuer *session.Issuer, sessions storage.SessionRecovery) *Server {
	return &Server{
		cfg:        cfg,
		registry:   registry,
		rooms:      rooms,
		matchmaker: mm,
		hub:        hub,
		issuer:     issuer,
		sessions:   sessions,
		upgrader:   websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, CheckOrigin: func(*http.Request) bool { return true }},
		pending:    make(map[game.PlayerID]*Connection),
		pendingGT:  make(map[game.PlayerID]string),
	}
}

// HandleWS is the http.HandlerFunc for the WebSocket upgrade endpoint. The
// client must present a bearer JWT (see internal/transport/httpapi/auth.go
// for how one is minted) either via the Authorization header or a "token"
// query parameter.
func (s *Server) HandleWS(w http.ResponseWriter, r *http.Request) {
	claims, err := authenticate(s.issuer, r)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wsConn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	c := newConnection(s, wsConn, game.PlayerID(claims.PlayerID), claims.ID)
	s.tryResume(c)
	go c.writePump()
	go c.readPump()
}

// tryResume looks up a persisted session record for c.sessionID (present
// only if this player previously disconnected mid-game within the grace
// window) and, if the owning room is registered on this node, reconnects
// the player into it. See internal/storage for the multi-node story: a
// record whose OwnerNode differs from this node would instead trigger a
// REDIRECT once Phase 4's Redis-backed SessionRecovery is wired in; the
// in-memory default always owns everything it wrote, so that branch never
// triggers in a single-node deployment.
func (s *Server) tryResume(c *Connection) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	rec, err := s.sessions.LoadSession(ctx, c.sessionID)
	if err != nil {
		return // fresh connection, no prior session to resume
	}
	r, ok := s.rooms.Get(room.ID(rec.RoomID))
	if !ok {
		if rec.OwnerNode != "" && rec.OwnerNode != s.cfg.NodeID {
			// The room lives on a different node (see storage.RoomLocator /
			// ManagerConfig.Locator); tell the client to redial there
			// rather than tunneling the connection across nodes.
			env := ServerEnvelope{Type: TypeRedirect, Ts: time.Now().UnixMilli(), Redirect: rec.OwnerNode}
			data, _ := json.Marshal(env)
			c.enqueue(data)
			return
		}
		c.sendError("ROOM_NOT_FOUND", "session referenced a room that no longer exists")
		return
	}
	res, err := r.Reconnect(ctx, c.playerID)
	if err != nil {
		c.sendError(errorCode(err), err.Error())
		return
	}
	c.setRoom(r, r.ID())
	s.hub.Register(r.ID(), c.playerID, c)
	s.bindSession(ctx, c, r.ID(), s.cfg.SessionTTL)

	env := ServerEnvelope{
		Type: string(room.EventStateSnapshot), RoomID: string(r.ID()), ServerSeq: res.ServerSeq,
		Ts: time.Now().UnixMilli(), Snapshot: json.RawMessage(res.Snapshot),
	}
	data, _ := json.Marshal(env)
	c.enqueue(data)
}

func (s *Server) bindSession(ctx context.Context, c *Connection, roomID room.ID, ttl time.Duration) {
	_ = s.sessions.SaveSession(ctx, c.sessionID, storage.SessionRecord{
		RoomID: string(roomID), PlayerID: string(c.playerID), OwnerNode: s.cfg.NodeID,
	}, ttl)
}

// findMatch enqueues c's player into gameType's matchmaking queue. If this
// call completes a match, every seated player who is currently pending on
// this node (which, in the single-node case, is every seated player) gets
// seated into the new room and sent an initial snapshot.
func (s *Server) findMatch(c *Connection, gameType string) {
	s.pendingMu.Lock()
	s.pending[c.playerID] = c
	s.pendingGT[c.playerID] = gameType
	s.pendingMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r, matched, err := s.matchmaker.Enqueue(ctx, gameType, c.playerID)
	if err != nil {
		s.clearPending(c.playerID)
		c.sendError(errorCode(err), err.Error())
		return
	}
	if !matched {
		return // still queued; will be seated by whichever call completes the match
	}
	s.seatMatchedPlayers(ctx, r)
}

func (s *Server) clearPending(playerID game.PlayerID) {
	s.pendingMu.Lock()
	delete(s.pending, playerID)
	delete(s.pendingGT, playerID)
	s.pendingMu.Unlock()
}

func (s *Server) seatMatchedPlayers(ctx context.Context, r *room.Room) {
	status, err := r.GetStatus(ctx)
	if err != nil {
		return
	}
	for _, p := range status.Players {
		s.pendingMu.Lock()
		conn, ok := s.pending[p]
		if ok {
			delete(s.pending, p)
			delete(s.pendingGT, p)
		}
		s.pendingMu.Unlock()
		if !ok {
			continue // that player isn't pending on this node
		}

		res, err := r.Snapshot(ctx)
		conn.setRoom(r, r.ID())
		s.hub.Register(r.ID(), p, conn)
		s.bindSession(ctx, conn, r.ID(), s.cfg.SessionTTL)

		env := ServerEnvelope{Type: string(room.EventRoomStarted), RoomID: string(r.ID()), Ts: time.Now().UnixMilli()}
		if err == nil {
			env.ServerSeq = res.ServerSeq
			env.Snapshot = json.RawMessage(res.Snapshot)
		}
		data, _ := json.Marshal(env)
		conn.enqueue(data)
	}
}

// onConnectionClosed runs cleanup for a connection that has stopped
// reading/writing, whether the client closed it, the heartbeat timed out,
// or a slow-consumer disconnect fired from Hub.Broadcast.
func (s *Server) onConnectionClosed(c *Connection) {
	s.pendingMu.Lock()
	_, wasPending := s.pending[c.playerID]
	gameType := s.pendingGT[c.playerID]
	if wasPending {
		delete(s.pending, c.playerID)
		delete(s.pendingGT, c.playerID)
	}
	s.pendingMu.Unlock()
	if wasPending {
		s.matchmaker.Dequeue(gameType, c.playerID)
	}

	r, roomID := c.getRoom()
	if r == nil {
		return
	}
	s.hub.Unregister(roomID, c.playerID, c)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = r.Disconnect(ctx, c.playerID)
	s.bindSession(ctx, c, roomID, s.cfg.GraceDuration+5*time.Second)
}
