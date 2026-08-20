// Package pingpong is a second, deliberately different Game plugin: a
// tick-driven reflex "buzzer" duel, in contrast to tictactoe's purely
// turn-based logic. Its only purpose is to prove the engine's Game
// abstraction (internal/domain/game) is genuinely generic and not secretly
// shaped around tic-tac-toe — nothing outside this package and its tests
// references it.
package pingpong

import (
	"encoding/json"
	"time"

	"realtime-engine/internal/domain/game"
)

// ActionBuzz is the sole action type this game accepts.
const ActionBuzz game.ActionType = "ACTION_BUZZ"

// defaultArmDelay is how long players must wait before the "go" signal
// fires and buzzing becomes legal.
const defaultArmDelay = 2 * time.Second

type phase int8

const (
	phaseCountdown phase = iota // buzzing now is a false start (instant loss)
	phaseArmed                  // "go" signal has fired; first legal buzz wins
	phaseDone
)

// State is the authoritative reflex-duel state. It implements game.State.
type State struct {
	Players  [2]game.PlayerID
	ArmDelay time.Duration
	ArmAt    time.Time // zero until the first Tick establishes it
	Phase    phase
	Done     bool
	Winner   *game.PlayerID
	Reason   string
}

type wireState struct {
	Players []string `json:"players"`
	Phase   string   `json:"phase"`
	Done    bool     `json:"done"`
	Winner  string   `json:"winner,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

func (p phase) String() string {
	switch p {
	case phaseCountdown:
		return "countdown"
	case phaseArmed:
		return "armed"
	default:
		return "done"
	}
}

func (s *State) toWire() wireState {
	w := wireState{
		Players: []string{string(s.Players[0]), string(s.Players[1])},
		Phase:   s.Phase.String(),
		Done:    s.Done,
		Reason:  s.Reason,
	}
	if s.Winner != nil {
		w.Winner = string(*s.Winner)
	}
	return w
}

// Marshal implements game.State.
func (s *State) Marshal() ([]byte, error) {
	return json.Marshal(s.toWire())
}

func (s *State) clone() *State {
	cp := *s
	if s.Winner != nil {
		w := *s.Winner
		cp.Winner = &w
	}
	return &cp
}

func (s *State) seatOf(player game.PlayerID) (int, bool) {
	for i, p := range s.Players {
		if p == player {
			return i, true
		}
	}
	return 0, false
}

// PingPong implements game.Game as a reflex buzzer duel.
type PingPong struct{}

// New constructs a fresh PingPong game instance. Matches game.Factory.
func New() game.Game { return &PingPong{} }

func (PingPong) MinPlayers() int { return 2 }
func (PingPong) MaxPlayers() int { return 2 }

type initConfig struct {
	ArmDelayMillis int `json:"armDelayMillis"`
}

func (PingPong) Init(players []game.PlayerID, config json.RawMessage) (game.State, error) {
	if len(players) != 2 {
		return nil, game.ErrInvalidPlayerCount
	}
	delay := defaultArmDelay
	if len(config) > 0 {
		var cfg initConfig
		if err := json.Unmarshal(config, &cfg); err == nil && cfg.ArmDelayMillis > 0 {
			delay = time.Duration(cfg.ArmDelayMillis) * time.Millisecond
		}
	}
	return &State{
		Players:  [2]game.PlayerID{players[0], players[1]},
		ArmDelay: delay,
		Phase:    phaseCountdown,
	}, nil
}

func asState(s game.State) (*State, error) {
	st, ok := s.(*State)
	if !ok {
		return nil, game.ErrInvalidPayload
	}
	return st, nil
}

func (PingPong) Validate(s game.State, action game.Action) error {
	st, err := asState(s)
	if err != nil {
		return err
	}
	if st.Done {
		return game.ErrGameOver
	}
	if action.Type != ActionBuzz {
		return game.ErrUnknownActionType
	}
	if _, ok := st.seatOf(action.PlayerID); !ok {
		return game.ErrUnknownPlayer
	}
	return nil
}

// ApplyAction handles ACTION_BUZZ. Because the room actor serializes all
// calls per room, whichever buzz arrives first genuinely is first — no
// additional locking or timestamp arbitration is needed here.
func (p PingPong) ApplyAction(s game.State, action game.Action) (game.Result, error) {
	if err := p.Validate(s, action); err != nil {
		return game.Result{}, err
	}
	st, _ := asState(s)
	buzzerSeat, _ := st.seatOf(action.PlayerID)

	next := st.clone()
	next.Done = true
	next.Phase = phaseDone

	var winnerSeat int
	if st.Phase == phaseArmed {
		winnerSeat = buzzerSeat
		next.Reason = "first_buzz"
	} else {
		winnerSeat = 1 - buzzerSeat
		next.Reason = "false_start"
	}
	w := next.Players[winnerSeat]
	next.Winner = &w

	diffBytes, err := next.Marshal()
	if err != nil {
		return game.Result{}, err
	}
	return game.Result{
		NewState: next,
		Diff:     diffBytes,
		Events:   []game.Event{{Type: "player_won"}},
		Terminal: true,
		Winner:   &w,
	}, nil
}

// Tick establishes ArmAt on first call (relative to the tick clock, so unit
// tests can drive it with synthetic timestamps) and flips Countdown->Armed
// once that deadline passes.
func (PingPong) Tick(s game.State, now time.Time) (game.Result, bool) {
	st, err := asState(s)
	if err != nil || st.Done {
		return game.Result{}, false
	}
	next := st.clone()
	changed := false

	if next.ArmAt.IsZero() {
		next.ArmAt = now.Add(next.ArmDelay)
		changed = true
	}
	if next.Phase == phaseCountdown && !now.Before(next.ArmAt) {
		next.Phase = phaseArmed
		changed = true
	}
	if !changed {
		return game.Result{}, false
	}

	diffBytes, err := next.Marshal()
	if err != nil {
		return game.Result{}, false
	}
	var events []game.Event
	if next.Phase == phaseArmed {
		events = append(events, game.Event{Type: "armed"})
	}
	return game.Result{NewState: next, Diff: diffBytes, Events: events}, true
}

func (PingPong) Snapshot(s game.State) ([]byte, error) {
	return s.Marshal()
}

func (PingPong) IsTerminal(s game.State) bool {
	st, err := asState(s)
	return err == nil && st.Done
}

func (PingPong) OnPlayerAbandoned(s game.State, playerID game.PlayerID) (game.Result, error) {
	st, err := asState(s)
	if err != nil {
		return game.Result{}, err
	}
	if st.Done {
		return game.Result{NewState: st}, nil
	}
	abandonedSeat, ok := st.seatOf(playerID)
	if !ok {
		return game.Result{}, game.ErrUnknownPlayer
	}
	next := st.clone()
	next.Done = true
	next.Phase = phaseDone
	w := next.Players[1-abandonedSeat]
	next.Winner = &w
	next.Reason = "opponent_abandoned"

	diffBytes, err := next.Marshal()
	if err != nil {
		return game.Result{}, err
	}
	return game.Result{
		NewState: next,
		Diff:     diffBytes,
		Events:   []game.Event{{Type: "player_abandoned"}, {Type: "player_won"}},
		Terminal: true,
		Winner:   &w,
	}, nil
}
