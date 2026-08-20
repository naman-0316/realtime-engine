// Package tictactoe is a concrete Game plugin implementing classic 3x3
// tic-tac-toe on top of the generic internal/domain/game engine contract.
// It is the reference implementation proving the engine's abstraction is
// usable, not a special-cased part of the engine itself.
package tictactoe

import (
	"encoding/json"
	"time"

	"realtime-engine/internal/domain/game"
)

// ActionMove is the sole action type this game accepts.
const ActionMove game.ActionType = "ACTION_MOVE"

type seat int8

const (
	emptySeat seat = 0
	seatX     seat = 1
	seatO     seat = 2
)

func (s seat) mark() string {
	switch s {
	case seatX:
		return "X"
	case seatO:
		return "O"
	default:
		return ""
	}
}

// State is the authoritative tic-tac-toe board state. It implements
// game.State via Marshal.
type State struct {
	Board     [9]seat
	Players   [2]game.PlayerID // index 0 plays X, index 1 plays O
	Turn      int              // seat index (0 or 1) to move next
	MoveCount int
	Done      bool
	Draw      bool
	Winner    *game.PlayerID
}

type wireState struct {
	Board     [9]string `json:"board"`
	Players   [2]string `json:"players"`
	Turn      string    `json:"turn"`
	MoveCount int       `json:"moveCount"`
	Done      bool      `json:"done"`
	Draw      bool      `json:"draw,omitempty"`
	Winner    string    `json:"winner,omitempty"`
}

func (s *State) toWire() wireState {
	w := wireState{
		Players:   [2]string{string(s.Players[0]), string(s.Players[1])},
		Turn:      string(s.Players[s.Turn]),
		MoveCount: s.MoveCount,
		Done:      s.Done,
		Draw:      s.Draw,
	}
	for i, cell := range s.Board {
		w.Board[i] = cell.mark()
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

type movePayload struct {
	X int `json:"x"`
	Y int `json:"y"`
}

type cellDiff struct {
	X    int    `json:"x"`
	Y    int    `json:"y"`
	Mark string `json:"mark"`
}

type diffPayload struct {
	Cell     cellDiff `json:"cell"`
	Turn     string   `json:"turn,omitempty"`
	Terminal bool     `json:"terminal,omitempty"`
	Winner   string   `json:"winner,omitempty"`
	Draw     bool     `json:"draw,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// TicTacToe implements game.Game. It is stateless itself; all mutable state
// lives in *State values passed to each method.
type TicTacToe struct{}

// New constructs a fresh TicTacToe game instance. Matches game.Factory.
func New() game.Game { return &TicTacToe{} }

func (TicTacToe) MinPlayers() int { return 2 }
func (TicTacToe) MaxPlayers() int { return 2 }

func (TicTacToe) Init(players []game.PlayerID, _ json.RawMessage) (game.State, error) {
	if len(players) != 2 {
		return nil, game.ErrInvalidPlayerCount
	}
	return &State{
		Players: [2]game.PlayerID{players[0], players[1]},
		Turn:    0, // X moves first
	}, nil
}

func asState(s game.State) (*State, error) {
	st, ok := s.(*State)
	if !ok {
		return nil, game.ErrInvalidPayload
	}
	return st, nil
}

func decodeMove(action game.Action) (movePayload, error) {
	var mv movePayload
	if action.Type != ActionMove {
		return mv, game.ErrUnknownActionType
	}
	if err := json.Unmarshal(action.Payload, &mv); err != nil {
		return mv, game.ErrInvalidPayload
	}
	if mv.X < 0 || mv.X > 2 || mv.Y < 0 || mv.Y > 2 {
		return mv, game.ErrInvalidPayload
	}
	return mv, nil
}

func (TicTacToe) Validate(s game.State, action game.Action) error {
	st, err := asState(s)
	if err != nil {
		return err
	}
	if st.Done {
		return game.ErrGameOver
	}
	seatIdx, ok := st.seatOf(action.PlayerID)
	if !ok {
		return game.ErrUnknownPlayer
	}
	if seatIdx != st.Turn {
		return game.ErrNotYourTurn
	}
	mv, err := decodeMove(action)
	if err != nil {
		return err
	}
	idx := mv.Y*3 + mv.X
	if st.Board[idx] != emptySeat {
		return game.ErrIllegalMove
	}
	return nil
}

func (t TicTacToe) ApplyAction(s game.State, action game.Action) (game.Result, error) {
	if err := t.Validate(s, action); err != nil {
		return game.Result{}, err
	}
	st, _ := asState(s)
	mv, _ := decodeMove(action)
	idx := mv.Y*3 + mv.X

	next := st.clone()
	mark := seatX
	if st.Turn == 1 {
		mark = seatO
	}
	next.Board[idx] = mark
	next.MoveCount++

	diff := diffPayload{Cell: cellDiff{X: mv.X, Y: mv.Y, Mark: mark.mark()}}
	var events []game.Event
	var winner *game.PlayerID
	terminal := false

	if winSeat := winningSeat(next.Board); winSeat >= 0 {
		next.Done = true
		w := next.Players[winSeat]
		next.Winner = &w
		winner = &w
		terminal = true
		diff.Terminal = true
		diff.Winner = string(w)
		events = append(events, game.Event{Type: "player_won"})
	} else if next.MoveCount == 9 {
		next.Done = true
		next.Draw = true
		terminal = true
		diff.Terminal = true
		diff.Draw = true
		events = append(events, game.Event{Type: "draw"})
	} else {
		next.Turn = 1 - st.Turn
		diff.Turn = string(next.Players[next.Turn])
		events = append(events, game.Event{Type: "turn_changed"})
	}

	diffBytes, err := json.Marshal(diff)
	if err != nil {
		return game.Result{}, err
	}
	return game.Result{
		NewState: next,
		Diff:     diffBytes,
		Events:   events,
		Terminal: terminal,
		Winner:   winner,
	}, nil
}

// Tick is a no-op: tic-tac-toe is purely turn-based and never changes state
// due to the passage of time alone.
func (TicTacToe) Tick(_ game.State, _ time.Time) (game.Result, bool) {
	return game.Result{}, false
}

func (TicTacToe) Snapshot(s game.State) ([]byte, error) {
	return s.Marshal()
}

func (TicTacToe) IsTerminal(s game.State) bool {
	st, err := asState(s)
	return err == nil && st.Done
}

func (TicTacToe) OnPlayerAbandoned(s game.State, playerID game.PlayerID) (game.Result, error) {
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
	winnerSeat := 1 - abandonedSeat
	w := next.Players[winnerSeat]
	next.Winner = &w

	diff := diffPayload{Terminal: true, Winner: string(w), Reason: "opponent_abandoned"}
	diffBytes, err := json.Marshal(diff)
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
