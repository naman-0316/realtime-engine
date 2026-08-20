package room

// Lifecycle is the coarse-grained state of a Room. Per-player connection
// state (connected / disconnected+grace-timer) is tracked separately in
// each playerSlot and does not by itself change the room Lifecycle, except
// when a grace-window expiry causes the underlying Game to terminate.
type Lifecycle int

const (
	// Waiting: below the game's MinPlayers; accepting joins via matchmaking
	// or direct join-by-ID.
	Waiting Lifecycle = iota
	// Active: MinPlayers reached, Game.Init has run, actions are accepted.
	Active
	// Finished: the Game reached a terminal state (win/draw/abandonment).
	// Lingers briefly so late clients can still fetch a final snapshot
	// before garbage collection.
	Finished
	// Closed: the room actor has stopped; the RoomManager has removed (or
	// is removing) it from its registry.
	Closed
)

func (l Lifecycle) String() string {
	switch l {
	case Waiting:
		return "waiting"
	case Active:
		return "active"
	case Finished:
		return "finished"
	case Closed:
		return "closed"
	default:
		return "unknown"
	}
}
