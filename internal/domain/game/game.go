// Package game defines the engine-agnostic contract every real-time game
// plugs into. Nothing in this package knows about tic-tac-toe, ping-pong,
// or any other concrete game — that is the whole point.
package game

import (
	"encoding/json"
	"time"
)

// PlayerID identifies a player within a room. It is opaque to the engine.
type PlayerID string

// ActionType is a game-defined action name (e.g. "ACTION_MOVE", "ACTION_BUZZ").
type ActionType string

// Action is a single validated player intent submitted to a room. Seq is
// assigned by the client and used by the room actor for replay/duplicate
// detection before the action ever reaches a Game implementation.
type Action struct {
	Type      ActionType
	PlayerID  PlayerID
	Seq       uint64
	Timestamp time.Time
	Payload   json.RawMessage
}

// State is an opaque, game-owned representation of a room's authoritative
// state. The engine never inspects it directly — it only asks the owning
// Game to Marshal it for snapshots or to compute diffs via ApplyAction.
type State interface {
	// Marshal returns a full authoritative snapshot suitable for sending to
	// a newly joined or resuming client.
	Marshal() ([]byte, error)
}

// Diff is an opaque, game-defined minimal delta describing how state changed
// as a result of one action or tick. The engine broadcasts it verbatim.
type Diff []byte

// EventType names a broadcast-worthy occurrence that isn't itself a state
// diff (e.g. "player_won", "turn_changed"), useful for client UX/telemetry.
type EventType string

// Event is an out-of-band notification accompanying a Result.
type Event struct {
	Type EventType
	Data json.RawMessage
}

// Result is returned by any state-mutating Game method.
type Result struct {
	NewState State
	Diff     Diff
	Events   []Event
	Terminal bool
	Winner   *PlayerID
}

// Game is the generic contract a concrete game (tic-tac-toe, ping-pong, ...)
// implements to plug into the room/session engine. Implementations are NOT
// required to be safe for concurrent use — the room actor guarantees all
// methods are invoked from a single goroutine, serialized per room.
type Game interface {
	// Init creates the initial authoritative state for a fresh room given
	// its seated players (len must satisfy MinPlayers/MaxPlayers) and an
	// optional game-specific config blob.
	Init(players []PlayerID, config json.RawMessage) (State, error)

	// Validate performs a pure, non-mutating legality check of action
	// against state. It must not mutate state or have side effects.
	Validate(state State, action Action) error

	// ApplyAction validates and applies action to state, returning the new
	// state plus a diff/events describing the change. Callers guarantee
	// action has already passed Validate and is not a stale/duplicate seq.
	ApplyAction(state State, action Action) (Result, error)

	// Tick advances state purely due to the passage of time (e.g. countdown
	// timers, physics). changed reports whether anything actually happened;
	// pure turn-based games can return changed=false unconditionally.
	Tick(state State, now time.Time) (result Result, changed bool)

	// Snapshot returns the full authoritative state, identical in shape to
	// what State.Marshal produces — used by the room actor without needing
	// a concrete State reference (e.g. after a restart or lock handoff).
	Snapshot(state State) ([]byte, error)

	// IsTerminal reports whether state represents a finished game.
	IsTerminal(state State) bool

	// OnPlayerAbandoned is invoked when a player's disconnect grace window
	// expires without reconnection. Implementations decide the policy:
	// forfeit, pause, substitute a bot, etc.
	OnPlayerAbandoned(state State, playerID PlayerID) (Result, error)

	// MinPlayers and MaxPlayers bound how many seats a room needs/allows
	// before matchmaking will start or accept further joins.
	MinPlayers() int
	MaxPlayers() int
}

// Factory constructs a fresh, independent Game instance. Registered under a
// game-type name so the service layer can create games by name without
// importing any concrete game package.
type Factory func() Game
