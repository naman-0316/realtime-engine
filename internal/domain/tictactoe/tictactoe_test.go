package tictactoe

import (
	"encoding/json"
	"testing"
	"time"

	"realtime-engine/internal/domain/game"
)

const (
	playerX game.PlayerID = "alice"
	playerO game.PlayerID = "bob"
)

func newGame(t *testing.T) (game.Game, game.State) {
	t.Helper()
	g := New()
	st, err := g.Init([]game.PlayerID{playerX, playerO}, nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return g, st
}

func move(player game.PlayerID, seq uint64, x, y int) game.Action {
	payload, _ := json.Marshal(movePayload{X: x, Y: y})
	return game.Action{Type: ActionMove, PlayerID: player, Seq: seq, Timestamp: time.Now(), Payload: payload}
}

func mustApply(t *testing.T, g game.Game, st game.State, a game.Action) (game.Result, game.State) {
	t.Helper()
	res, err := g.ApplyAction(st, a)
	if err != nil {
		t.Fatalf("ApplyAction(%+v): unexpected error: %v", a, err)
	}
	return res, res.NewState
}

func TestInitRejectsWrongPlayerCount(t *testing.T) {
	g := New()
	for _, players := range [][]game.PlayerID{nil, {playerX}, {playerX, playerO, "carol"}} {
		if _, err := g.Init(players, nil); err != game.ErrInvalidPlayerCount {
			t.Errorf("Init(%v players): got err=%v, want ErrInvalidPlayerCount", len(players), err)
		}
	}
}

func TestAllEightWinLines(t *testing.T) {
	// Each case is a sequence of (player, x, y) moves; the last move must win.
	cases := []struct {
		name  string
		moves [][3]int // {seatIdx(0=X,1=O), x, y}
	}{
		{"row0", [][3]int{{0, 0, 0}, {1, 0, 1}, {0, 1, 0}, {1, 1, 1}, {0, 2, 0}}},
		{"row1", [][3]int{{0, 0, 1}, {1, 0, 0}, {0, 1, 1}, {1, 1, 0}, {0, 2, 1}}},
		{"row2", [][3]int{{0, 0, 2}, {1, 0, 0}, {0, 1, 2}, {1, 1, 0}, {0, 2, 2}}},
		{"col0", [][3]int{{0, 0, 0}, {1, 1, 0}, {0, 0, 1}, {1, 1, 1}, {0, 0, 2}}},
		{"col1", [][3]int{{0, 1, 0}, {1, 0, 0}, {0, 1, 1}, {1, 0, 1}, {0, 1, 2}}},
		{"col2", [][3]int{{0, 2, 0}, {1, 0, 0}, {0, 2, 1}, {1, 0, 1}, {0, 2, 2}}},
		{"diag", [][3]int{{0, 0, 0}, {1, 1, 0}, {0, 1, 1}, {1, 2, 0}, {0, 2, 2}}},
		{"anti-diag", [][3]int{{0, 2, 0}, {1, 0, 0}, {0, 1, 1}, {1, 1, 0}, {0, 0, 2}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, st := newGame(t)
			var res game.Result
			for i, m := range tc.moves {
				player := playerX
				if m[0] == 1 {
					player = playerO
				}
				res, st = mustApply(t, g, st, move(player, uint64(i+1), m[1], m[2]))
			}
			if !res.Terminal {
				t.Fatalf("expected terminal result after winning move")
			}
			if res.Winner == nil || *res.Winner != playerX {
				t.Fatalf("expected playerX to win, got %+v", res.Winner)
			}
			if !g.IsTerminal(st) {
				t.Fatalf("IsTerminal should be true after a win")
			}
		})
	}
}

func TestDraw(t *testing.T) {
	g, st := newGame(t)
	// X O X / X O O / O X X -> full board, no winner.
	seq := []struct {
		player game.PlayerID
		x, y   int
	}{
		{playerX, 0, 0}, {playerO, 1, 0}, {playerX, 2, 0},
		{playerO, 1, 1}, {playerX, 0, 1}, {playerO, 2, 1},
		{playerX, 1, 2}, {playerO, 0, 2}, {playerX, 2, 2},
	}
	var res game.Result
	for i, m := range seq {
		res, st = mustApply(t, g, st, move(m.player, uint64(i+1), m.x, m.y))
	}
	if !res.Terminal || res.Winner != nil {
		t.Fatalf("expected a terminal draw with no winner, got terminal=%v winner=%+v", res.Terminal, res.Winner)
	}
	if !g.IsTerminal(st) {
		t.Fatalf("IsTerminal should be true after a draw")
	}
}

func TestIllegalMoveOccupiedCell(t *testing.T) {
	g, st := newGame(t)
	_, st = mustApply(t, g, st, move(playerX, 1, 0, 0))
	if _, err := g.ApplyAction(st, move(playerO, 2, 0, 0)); err != game.ErrIllegalMove {
		t.Fatalf("got err=%v, want ErrIllegalMove", err)
	}
}

func TestNotYourTurn(t *testing.T) {
	g, st := newGame(t)
	if _, err := g.ApplyAction(st, move(playerO, 1, 0, 0)); err != game.ErrNotYourTurn {
		t.Fatalf("got err=%v, want ErrNotYourTurn (X moves first)", err)
	}
}

func TestMoveAfterTerminalRejected(t *testing.T) {
	g, st := newGame(t)
	moves := [][3]int{{0, 0, 0}, {1, 1, 0}, {0, 0, 1}, {1, 1, 1}, {0, 0, 2}} // X wins col0
	for i, m := range moves {
		player := playerX
		if m[0] == 1 {
			player = playerO
		}
		_, st = mustApply(t, g, st, move(player, uint64(i+1), m[1], m[2]))
	}
	if _, err := g.ApplyAction(st, move(playerO, 99, 2, 2)); err != game.ErrGameOver {
		t.Fatalf("got err=%v, want ErrGameOver", err)
	}
}

func TestUnknownPlayerRejected(t *testing.T) {
	g, st := newGame(t)
	if _, err := g.ApplyAction(st, move("stranger", 1, 0, 0)); err != game.ErrUnknownPlayer {
		t.Fatalf("got err=%v, want ErrUnknownPlayer", err)
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	g, st := newGame(t)
	_, st = mustApply(t, g, st, move(playerX, 1, 1, 1))

	snap, err := g.Snapshot(st)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	var w wireState
	if err := json.Unmarshal(snap, &w); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if w.Board[4] != "X" {
		t.Fatalf("expected center cell to be X in snapshot, got %q", w.Board[4])
	}
	if w.Turn != string(playerO) {
		t.Fatalf("expected turn=%s, got %s", playerO, w.Turn)
	}

	directMarshal, err := st.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(snap) != string(directMarshal) {
		t.Fatalf("Snapshot and State.Marshal diverged:\n%s\nvs\n%s", snap, directMarshal)
	}
}

func TestOnPlayerAbandonedDeclaresOpponentWinner(t *testing.T) {
	g, st := newGame(t)
	_, st = mustApply(t, g, st, move(playerX, 1, 0, 0))

	res, err := g.OnPlayerAbandoned(st, playerO)
	if err != nil {
		t.Fatalf("OnPlayerAbandoned: %v", err)
	}
	if !res.Terminal || res.Winner == nil || *res.Winner != playerX {
		t.Fatalf("expected playerX declared winner, got %+v", res)
	}
	if !g.IsTerminal(res.NewState) {
		t.Fatalf("IsTerminal should be true after abandonment")
	}
}
