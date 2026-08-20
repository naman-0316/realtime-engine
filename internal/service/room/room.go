// Package room implements the authoritative per-room concurrency core: a
// single goroutine ("actor") per room processes a serialized command
// channel, owning a game.Game instance so game logic never needs to be
// thread-safe itself. See lifecycle.go for the room-level state machine and
// manager.go for the registry that creates/looks up/garbage-collects rooms.
package room

import (
	"context"
	"time"

	"github.com/oklog/ulid/v2"

	"realtime-engine/internal/domain/game"
)

// ID uniquely identifies a room. ULIDs are lexicographically sortable by
// creation time, which keeps logs and Redis keys easy to reason about.
type ID string

// NewID generates a fresh room ID.
func NewID() ID { return ID(ulid.Make().String()) }

// CmdKind enumerates the commands a Room actor accepts. Every state
// mutation in a room happens as the result of processing exactly one
// Command on the actor goroutine — this is the entire concurrency story.
type CmdKind int

const (
	CmdJoin CmdKind = iota
	CmdLeave
	CmdAction
	CmdDisconnect
	CmdReconnect
	CmdGraceExpired
	CmdForceClose
	CmdSnapshot
)

// Command is a single message sent into a Room's command channel. Reply, if
// non-nil, must be a buffered channel of capacity >= 1 so the actor's send
// never blocks regardless of whether the caller is still listening.
type Command struct {
	Kind       CmdKind
	Player     game.PlayerID
	Action     *game.Action
	Generation int // only meaningful for CmdGraceExpired staleness checks
	Reply      chan CmdResult
}

// CmdResult is the synchronous outcome of a Command that supplied a Reply
// channel.
type CmdResult struct {
	Err       error
	Seat      int
	Lifecycle Lifecycle
	Snapshot  []byte
	ServerSeq uint64
}

// Status is a point-in-time, concurrency-safe view of a Room's public state,
// cheap to read from any goroutine (e.g. a RoomManager's GC sweep) without
// going through the command channel.
type Status struct {
	ID             ID
	GameType       string
	Lifecycle      Lifecycle
	Players        []game.PlayerID
	PlayerCount    int
	ConnectedCount int
	CreatedAt      time.Time
	LastActivityAt time.Time
	FinishedAt     time.Time
}

type playerSlot struct {
	seat           int
	connected      bool
	generation     int
	lastAppliedSeq uint64
}

// Room is one authoritative game session. All exported methods are safe for
// concurrent use: they submit a Command and wait for the actor goroutine
// (started by newRoom) to process it.
type Room struct {
	id       ID
	gameType string
	g        game.Game

	graceDuration  time.Duration
	tickInterval   time.Duration
	finishedLinger time.Duration

	cmdCh   chan Command
	stopped chan struct{}
	onClose func(ID)

	// --- fields below are owned exclusively by the actor goroutine ---
	lifecycle    Lifecycle
	state        game.State
	seatOrder    []game.PlayerID
	players      map[game.PlayerID]*playerSlot
	serverSeq    uint64
	createdAt    time.Time
	lastActivity time.Time
	finishedAt   time.Time

	sink Sink

	statusCh chan chan Status // request/response for cheap concurrent status reads
}

type roomConfig struct {
	graceDuration  time.Duration
	tickInterval   time.Duration
	finishedLinger time.Duration
}

func defaultRoomConfig() roomConfig {
	return roomConfig{
		graceDuration:  30 * time.Second,
		tickInterval:   100 * time.Millisecond,
		finishedLinger: 10 * time.Second,
	}
}

func newRoom(id ID, gameType string, g game.Game, sink Sink, cfg roomConfig, onClose func(ID)) *Room {
	now := time.Now()
	r := &Room{
		id:             id,
		gameType:       gameType,
		g:              g,
		graceDuration:  cfg.graceDuration,
		tickInterval:   cfg.tickInterval,
		finishedLinger: cfg.finishedLinger,
		cmdCh:          make(chan Command, 64),
		stopped:        make(chan struct{}),
		onClose:        onClose,
		lifecycle:      Waiting,
		players:        make(map[game.PlayerID]*playerSlot),
		createdAt:      now,
		lastActivity:   now,
		sink:           sink,
		statusCh:       make(chan chan Status, 8),
	}
	go r.run()
	return r
}

func (r *Room) ID() ID           { return r.id }
func (r *Room) GameType() string { return r.gameType }

// run is the actor loop: the only goroutine ever allowed to touch r.state,
// r.players, r.lifecycle, etc. Every other goroutine communicates with it
// exclusively via r.cmdCh (commands) and r.statusCh (status reads).
func (r *Room) run() {
	ticker := time.NewTicker(r.tickInterval)
	defer ticker.Stop()
	defer close(r.stopped)

	for r.lifecycle != Closed {
		select {
		case cmd := <-r.cmdCh:
			r.handle(cmd)
		case now := <-ticker.C:
			r.handleTick(now)
		case respCh := <-r.statusCh:
			respCh <- r.statusLocked()
		}
	}
	if r.onClose != nil {
		r.onClose(r.id)
	}
}

func (r *Room) statusLocked() Status {
	connected := 0
	for _, s := range r.players {
		if s.connected {
			connected++
		}
	}
	players := make([]game.PlayerID, len(r.seatOrder))
	copy(players, r.seatOrder)
	return Status{
		ID:             r.id,
		GameType:       r.gameType,
		Lifecycle:      r.lifecycle,
		Players:        players,
		PlayerCount:    len(r.seatOrder),
		ConnectedCount: connected,
		CreatedAt:      r.createdAt,
		LastActivityAt: r.lastActivity,
		FinishedAt:     r.finishedAt,
	}
}

func (r *Room) touch() { r.lastActivity = time.Now() }

func reply(cmd Command, res CmdResult) {
	if cmd.Reply != nil {
		cmd.Reply <- res
	}
}

func (r *Room) handle(cmd Command) {
	r.touch()
	switch cmd.Kind {
	case CmdJoin:
		r.handleJoin(cmd)
	case CmdLeave:
		r.handleLeave(cmd)
	case CmdAction:
		r.handleAction(cmd)
	case CmdDisconnect:
		r.handleDisconnect(cmd)
	case CmdReconnect:
		r.handleReconnect(cmd)
	case CmdGraceExpired:
		r.handleGraceExpired(cmd)
	case CmdForceClose:
		r.handleForceClose(cmd)
	case CmdSnapshot:
		r.handleSnapshot(cmd)
	}
}

func (r *Room) handleJoin(cmd Command) {
	if r.lifecycle == Closed || r.lifecycle == Finished {
		reply(cmd, CmdResult{Err: ErrRoomClosed})
		return
	}
	if slot, ok := r.players[cmd.Player]; ok {
		// Idempotent: joining twice just reaffirms the existing seat.
		reply(cmd, CmdResult{Seat: slot.seat, Lifecycle: r.lifecycle, Snapshot: r.snapshotBytes(), ServerSeq: r.serverSeq})
		return
	}
	if len(r.seatOrder) >= r.g.MaxPlayers() {
		reply(cmd, CmdResult{Err: ErrRoomFull})
		return
	}

	seat := len(r.seatOrder)
	r.seatOrder = append(r.seatOrder, cmd.Player)
	r.players[cmd.Player] = &playerSlot{seat: seat, connected: true}

	r.sink.Broadcast(RoomEvent{RoomID: r.id, Kind: EventPlayerJoined, ServerSeq: r.serverSeq, Subject: cmd.Player})

	if r.lifecycle == Waiting && len(r.seatOrder) >= r.g.MinPlayers() {
		state, err := r.g.Init(r.seatOrder, nil)
		if err != nil {
			reply(cmd, CmdResult{Err: err})
			return
		}
		r.state = state
		r.lifecycle = Active
		r.sink.Broadcast(RoomEvent{RoomID: r.id, Kind: EventRoomStarted, ServerSeq: r.serverSeq, Snapshot: r.snapshotBytes()})
	}

	reply(cmd, CmdResult{Seat: seat, Lifecycle: r.lifecycle, Snapshot: r.snapshotBytes(), ServerSeq: r.serverSeq})
}

func (r *Room) handleLeave(cmd Command) {
	slot, ok := r.players[cmd.Player]
	if !ok {
		reply(cmd, CmdResult{Lifecycle: r.lifecycle}) // idempotent no-op
		return
	}
	if r.lifecycle != Waiting {
		// Once a game is active, "leaving" is modeled as a disconnect so
		// the grace-window/forfeit flow applies uniformly.
		r.handleDisconnect(cmd)
		return
	}
	delete(r.players, cmd.Player)
	for i, p := range r.seatOrder {
		if p == cmd.Player {
			r.seatOrder = append(r.seatOrder[:i], r.seatOrder[i+1:]...)
			break
		}
	}
	for _, s := range r.players {
		if s.seat > slot.seat {
			s.seat--
		}
	}
	reply(cmd, CmdResult{Lifecycle: r.lifecycle})

	if len(r.seatOrder) == 0 {
		r.lifecycle = Closed
	}
}

func (r *Room) handleAction(cmd Command) {
	if r.lifecycle != Active {
		reply(cmd, CmdResult{Err: ErrRoomNotActive})
		return
	}
	slot, ok := r.players[cmd.Player]
	if !ok {
		reply(cmd, CmdResult{Err: ErrUnknownPlayer})
		return
	}
	if !slot.connected {
		reply(cmd, CmdResult{Err: ErrPlayerDisconnected})
		return
	}
	action := *cmd.Action
	if action.Seq <= slot.lastAppliedSeq && slot.lastAppliedSeq != 0 {
		reply(cmd, CmdResult{Err: ErrStaleAction})
		return
	}
	if err := r.g.Validate(r.state, action); err != nil {
		reply(cmd, CmdResult{Err: err})
		return
	}
	result, err := r.g.ApplyAction(r.state, action)
	if err != nil {
		reply(cmd, CmdResult{Err: err})
		return
	}
	r.state = result.NewState
	slot.lastAppliedSeq = action.Seq
	r.serverSeq++

	r.sink.Broadcast(RoomEvent{
		RoomID: r.id, Kind: EventStateDiff, ServerSeq: r.serverSeq,
		Diff: result.Diff, Events: result.Events, Terminal: result.Terminal, Winner: result.Winner,
		Subject: cmd.Player,
	})

	if result.Terminal {
		r.lifecycle = Finished
		r.finishedAt = time.Now()
	}
	reply(cmd, CmdResult{Lifecycle: r.lifecycle, ServerSeq: r.serverSeq})
}

func (r *Room) handleDisconnect(cmd Command) {
	slot, ok := r.players[cmd.Player]
	if !ok || !slot.connected {
		reply(cmd, CmdResult{Lifecycle: r.lifecycle}) // idempotent no-op
		return
	}
	slot.connected = false
	slot.generation++
	generation := slot.generation

	r.sink.Broadcast(RoomEvent{RoomID: r.id, Kind: EventPlayerDisconnected, ServerSeq: r.serverSeq, Subject: cmd.Player})
	reply(cmd, CmdResult{Lifecycle: r.lifecycle})

	if r.lifecycle == Active {
		cmdCh := r.cmdCh
		stopped := r.stopped
		player := cmd.Player
		time.AfterFunc(r.graceDuration, func() {
			select {
			case cmdCh <- Command{Kind: CmdGraceExpired, Player: player, Generation: generation}:
			case <-stopped:
			}
		})
	}
}

func (r *Room) handleReconnect(cmd Command) {
	slot, ok := r.players[cmd.Player]
	if !ok {
		reply(cmd, CmdResult{Err: ErrUnknownPlayer})
		return
	}
	wasConnected := slot.connected
	slot.connected = true
	if !wasConnected {
		r.sink.Broadcast(RoomEvent{RoomID: r.id, Kind: EventPlayerReconnected, ServerSeq: r.serverSeq, Subject: cmd.Player})
	}
	reply(cmd, CmdResult{
		Seat: slot.seat, Lifecycle: r.lifecycle,
		Snapshot: r.snapshotBytes(), ServerSeq: r.serverSeq,
	})
}

func (r *Room) handleGraceExpired(cmd Command) {
	slot, ok := r.players[cmd.Player]
	if !ok || slot.connected || slot.generation != cmd.Generation {
		return // stale timer: player reconnected or disconnected again since
	}
	if r.lifecycle != Active {
		return
	}
	result, err := r.g.OnPlayerAbandoned(r.state, cmd.Player)
	if err != nil {
		return
	}
	r.state = result.NewState
	r.serverSeq++
	r.lifecycle = Finished
	r.finishedAt = time.Now()
	r.sink.Broadcast(RoomEvent{
		RoomID: r.id, Kind: EventStateDiff, ServerSeq: r.serverSeq,
		Diff: result.Diff, Events: result.Events, Terminal: true, Winner: result.Winner,
		Subject: cmd.Player,
	})
}

func (r *Room) handleForceClose(cmd Command) {
	if r.lifecycle != Closed {
		r.sink.Broadcast(RoomEvent{RoomID: r.id, Kind: EventRoomClosed, ServerSeq: r.serverSeq})
	}
	r.lifecycle = Closed
	reply(cmd, CmdResult{Lifecycle: Closed})
}

func (r *Room) handleSnapshot(cmd Command) {
	reply(cmd, CmdResult{Lifecycle: r.lifecycle, Snapshot: r.snapshotBytes(), ServerSeq: r.serverSeq})
}

func (r *Room) handleTick(now time.Time) {
	if r.lifecycle != Active || r.state == nil {
		return
	}
	result, changed := r.g.Tick(r.state, now)
	if !changed {
		return
	}
	r.state = result.NewState
	r.serverSeq++
	r.sink.Broadcast(RoomEvent{
		RoomID: r.id, Kind: EventStateDiff, ServerSeq: r.serverSeq,
		Diff: result.Diff, Events: result.Events, Terminal: result.Terminal, Winner: result.Winner,
	})
	if result.Terminal {
		r.lifecycle = Finished
		r.finishedAt = now
	}
}

func (r *Room) snapshotBytes() []byte {
	if r.state == nil {
		return nil
	}
	snap, err := r.g.Snapshot(r.state)
	if err != nil {
		return nil
	}
	return snap
}

// --- public, concurrency-safe API -----------------------------------------

func (r *Room) send(cmd Command) error {
	select {
	case r.cmdCh <- cmd:
		return nil
	case <-r.stopped:
		return ErrRoomClosed
	}
}

func (r *Room) submit(ctx context.Context, cmd Command) (CmdResult, error) {
	reply := make(chan CmdResult, 1)
	cmd.Reply = reply
	if err := r.send(cmd); err != nil {
		return CmdResult{}, err
	}
	select {
	case res := <-reply:
		return res, res.Err
	case <-r.stopped:
		return CmdResult{}, ErrRoomClosed
	case <-ctx.Done():
		return CmdResult{}, ctx.Err()
	}
}

func (r *Room) Join(ctx context.Context, player game.PlayerID) (CmdResult, error) {
	return r.submit(ctx, Command{Kind: CmdJoin, Player: player})
}

func (r *Room) Leave(ctx context.Context, player game.PlayerID) (CmdResult, error) {
	return r.submit(ctx, Command{Kind: CmdLeave, Player: player})
}

func (r *Room) SubmitAction(ctx context.Context, action game.Action) (CmdResult, error) {
	return r.submit(ctx, Command{Kind: CmdAction, Player: action.PlayerID, Action: &action})
}

func (r *Room) Disconnect(ctx context.Context, player game.PlayerID) (CmdResult, error) {
	return r.submit(ctx, Command{Kind: CmdDisconnect, Player: player})
}

func (r *Room) Reconnect(ctx context.Context, player game.PlayerID) (CmdResult, error) {
	return r.submit(ctx, Command{Kind: CmdReconnect, Player: player})
}

func (r *Room) ForceClose(ctx context.Context) (CmdResult, error) {
	return r.submit(ctx, Command{Kind: CmdForceClose})
}

func (r *Room) Snapshot(ctx context.Context) (CmdResult, error) {
	return r.submit(ctx, Command{Kind: CmdSnapshot})
}

// GetStatus returns a cheap, eventually-consistent status snapshot. It does
// not use the command channel's Reply pattern so that frequent GC-sweep
// polling never competes with gameplay commands for actor attention beyond
// one extra select case.
func (r *Room) GetStatus(ctx context.Context) (Status, error) {
	respCh := make(chan Status, 1)
	select {
	case r.statusCh <- respCh:
	case <-r.stopped:
		return Status{}, ErrRoomClosed
	case <-ctx.Done():
		return Status{}, ctx.Err()
	}
	select {
	case s := <-respCh:
		return s, nil
	case <-r.stopped:
		return Status{}, ErrRoomClosed
	case <-ctx.Done():
		return Status{}, ctx.Err()
	}
}
