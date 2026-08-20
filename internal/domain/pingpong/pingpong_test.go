package pingpong

import (
	"testing"
	"time"

	"realtime-engine/internal/domain/game"
)

const (
	playerA game.PlayerID = "alice"
	playerB game.PlayerID = "bob"
)

func newGame(t *testing.T) (game.Game, game.State) {
	t.Helper()
	g := New()
	st, err := g.Init([]game.PlayerID{playerA, playerB}, nil)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	return g, st
}

func buzz(player game.PlayerID, seq uint64) game.Action {
	return game.Action{Type: ActionBuzz, PlayerID: player, Seq: seq, Timestamp: time.Now()}
}

func TestInitRejectsWrongPlayerCount(t *testing.T) {
	g := New()
	if _, err := g.Init([]game.PlayerID{playerA}, nil); err != game.ErrInvalidPlayerCount {
		t.Fatalf("got err=%v, want ErrInvalidPlayerCount", err)
	}
}

func TestFalseStartLosesToOpponent(t *testing.T) {
	g, st := newGame(t)
	res, err := g.ApplyAction(st, buzz(playerA, 1))
	if err != nil {
		t.Fatalf("ApplyAction: %v", err)
	}
	if !res.Terminal || res.Winner == nil || *res.Winner != playerB {
		t.Fatalf("expected playerB to win on playerA's false start, got %+v", res)
	}
}

func TestFirstBuzzAfterArmedWins(t *testing.T) {
	g, st := newGame(t)
	base := time.Now()

	// First tick establishes ArmAt; still in countdown.
	res, changed := g.Tick(st, base)
	if !changed {
		t.Fatalf("expected first Tick to change state (establishes ArmAt)")
	}
	st = res.NewState

	// A tick after the arm delay flips to Armed.
	res, changed = g.Tick(st, base.Add(defaultArmDelay+time.Millisecond))
	if !changed {
		t.Fatalf("expected Tick past ArmAt to change state to Armed")
	}
	st = res.NewState

	applyRes, err := g.ApplyAction(st, buzz(playerB, 1))
	if err != nil {
		t.Fatalf("ApplyAction: %v", err)
	}
	if !applyRes.Terminal || applyRes.Winner == nil || *applyRes.Winner != playerB {
		t.Fatalf("expected playerB to win the legal buzz, got %+v", applyRes)
	}
}

func TestTickNoopWhenNothingChanges(t *testing.T) {
	g, st := newGame(t)
	base := time.Now()
	res, changed := g.Tick(st, base)
	if !changed {
		t.Fatalf("expected first tick to establish ArmAt")
	}
	// A second tick before the arm deadline should be a genuine no-op.
	_, changed = g.Tick(res.NewState, base.Add(time.Millisecond))
	if changed {
		t.Fatalf("expected no-op tick before ArmAt deadline")
	}
}

func TestActionAfterTerminalRejected(t *testing.T) {
	g, st := newGame(t)
	res, err := g.ApplyAction(st, buzz(playerA, 1)) // false start, game over
	if err != nil {
		t.Fatalf("ApplyAction: %v", err)
	}
	if _, err := g.ApplyAction(res.NewState, buzz(playerB, 2)); err != game.ErrGameOver {
		t.Fatalf("got err=%v, want ErrGameOver", err)
	}
}

func TestOnPlayerAbandonedDeclaresOpponentWinner(t *testing.T) {
	g, st := newGame(t)
	res, err := g.OnPlayerAbandoned(st, playerA)
	if err != nil {
		t.Fatalf("OnPlayerAbandoned: %v", err)
	}
	if !res.Terminal || res.Winner == nil || *res.Winner != playerB {
		t.Fatalf("expected playerB declared winner, got %+v", res)
	}
	if !g.IsTerminal(res.NewState) {
		t.Fatalf("IsTerminal should be true after abandonment")
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	g, st := newGame(t)
	snap, err := g.Snapshot(st)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	direct, err := st.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(snap) != string(direct) {
		t.Fatalf("Snapshot and State.Marshal diverged:\n%s\nvs\n%s", snap, direct)
	}
}
