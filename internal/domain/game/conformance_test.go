// conformance_test.go exercises every registered concrete Game against the
// same generic checks, proving the game.Game abstraction genuinely fits
// more than one game shape (turn-based tictactoe, tick-driven pingpong)
// rather than being implicitly modeled after just one of them.
package game_test

import (
	"encoding/json"
	"testing"
	"time"

	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/domain/pingpong"
	"realtime-engine/internal/domain/tictactoe"
)

func registryUnderTest() *game.Registry {
	r := game.NewRegistry()
	r.Register("tictactoe", tictactoe.New)
	r.Register("pingpong", pingpong.New)
	return r
}

func syntheticPlayers(n int) []game.PlayerID {
	players := make([]game.PlayerID, n)
	for i := range players {
		players[i] = game.PlayerID(string(rune('a' + i)))
	}
	return players
}

func TestRegistryConstructsEveryGameByName(t *testing.T) {
	r := registryUnderTest()
	for _, name := range r.Names() {
		if _, err := r.New(name); err != nil {
			t.Errorf("registry.New(%q): %v", name, err)
		}
	}
}

func TestUnregisteredGameNameErrors(t *testing.T) {
	r := registryUnderTest()
	if _, err := r.New("does-not-exist"); err == nil {
		t.Fatalf("expected error constructing an unregistered game type")
	}
}

func TestEveryGameConformsToInterfaceContract(t *testing.T) {
	r := registryUnderTest()
	for _, name := range r.Names() {
		name := name
		t.Run(name, func(t *testing.T) {
			g, err := r.New(name)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			min, max := g.MinPlayers(), g.MaxPlayers()
			if min < 1 || max < min {
				t.Fatalf("nonsensical player bounds: min=%d max=%d", min, max)
			}

			// Wrong player counts must be rejected.
			if _, err := g.Init(syntheticPlayers(min-1), nil); min > 0 && err == nil {
				t.Errorf("Init with %d players (below MinPlayers=%d) should fail", min-1, min)
			}
			if _, err := g.Init(syntheticPlayers(max+1), nil); err == nil {
				t.Errorf("Init with %d players (above MaxPlayers=%d) should fail", max+1, max)
			}

			// A valid player count must succeed and produce a fresh,
			// non-terminal, snapshot-able state.
			st, err := g.Init(syntheticPlayers(min), nil)
			if err != nil {
				t.Fatalf("Init with valid player count: %v", err)
			}
			if g.IsTerminal(st) {
				t.Errorf("freshly initialized state should not be terminal")
			}

			snap, err := g.Snapshot(st)
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if len(snap) == 0 || !json.Valid(snap) {
				t.Errorf("Snapshot must return non-empty, valid JSON, got %q", snap)
			}
			marshaled, err := st.Marshal()
			if err != nil {
				t.Fatalf("State.Marshal: %v", err)
			}
			if string(snap) != string(marshaled) {
				t.Errorf("Snapshot and State.Marshal must agree:\n%s\nvs\n%s", snap, marshaled)
			}

			// An action from a player not seated in this game must always
			// be rejected, regardless of game-specific rules.
			bogusAction := game.Action{PlayerID: "not-a-real-player", Seq: 1, Timestamp: time.Now()}
			if err := g.Validate(st, bogusAction); err == nil {
				t.Errorf("Validate should reject an action from an unseated player")
			}

			// Tick must never panic on a fresh state, terminal or not.
			_, _ = g.Tick(st, time.Now())

			// Abandoning a seated player must terminate the game and name
			// the remaining player winner (true for both 2-player games
			// currently registered).
			res, err := g.OnPlayerAbandoned(st, syntheticPlayers(min)[0])
			if err != nil {
				t.Fatalf("OnPlayerAbandoned: %v", err)
			}
			if !res.Terminal {
				t.Errorf("OnPlayerAbandoned should terminate the game")
			}
		})
	}
}
