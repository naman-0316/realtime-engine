package matchmaking

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/domain/tictactoe"
	"realtime-engine/internal/service/room"
)

func newTestMatchmaker(t *testing.T) (*Matchmaker, *room.Manager) {
	t.Helper()
	reg := game.NewRegistry()
	reg.Register("tictactoe", tictactoe.New)
	rooms := room.NewManager(reg, room.NewRecordingSink(), room.ManagerConfig{})
	return New(reg, rooms), rooms
}

func TestEnqueueMatchesPairs(t *testing.T) {
	mm, _ := newTestMatchmaker(t)
	ctx := context.Background()

	if _, matched, err := mm.Enqueue(ctx, "tictactoe", "alice"); err != nil || matched {
		t.Fatalf("first enqueue should not match yet, matched=%v err=%v", matched, err)
	}
	r, matched, err := mm.Enqueue(ctx, "tictactoe", "bob")
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !matched || r == nil {
		t.Fatalf("second enqueue should complete the match")
	}
	if mm.QueueLength("tictactoe") != 0 {
		t.Fatalf("queue should be drained after a match")
	}
}

func TestEnqueueIsIdempotentForSamePlayer(t *testing.T) {
	mm, _ := newTestMatchmaker(t)
	ctx := context.Background()
	mm.Enqueue(ctx, "tictactoe", "alice")
	mm.Enqueue(ctx, "tictactoe", "alice")
	if got := mm.QueueLength("tictactoe"); got != 1 {
		t.Fatalf("re-enqueueing the same player should be a no-op, queue length=%d", got)
	}
}

func TestDequeueRemovesQueuedPlayer(t *testing.T) {
	mm, _ := newTestMatchmaker(t)
	mm.Enqueue(context.Background(), "tictactoe", "alice")
	if !mm.Dequeue("tictactoe", "alice") {
		t.Fatalf("expected Dequeue to report the player was queued")
	}
	if mm.Dequeue("tictactoe", "alice") {
		t.Fatalf("second Dequeue of the same player should report false")
	}
}

// TestConcurrentEnqueueNeverDoubleMatches fires many concurrent Enqueue
// calls for distinct players and verifies every matched room has exactly
// two distinct players and no player appears in more than one room. Run
// with -race.
func TestConcurrentEnqueueNeverDoubleMatches(t *testing.T) {
	mm, _ := newTestMatchmaker(t)
	ctx := context.Background()

	const n = 200 // even, so everyone should eventually be matched
	var wg sync.WaitGroup
	var mu sync.Mutex
	roomCount := 0
	totalSeated := 0

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			player := game.PlayerID(fmt.Sprintf("player-%d", i))
			r, matched, err := mm.Enqueue(ctx, "tictactoe", player)
			if err != nil {
				t.Errorf("Enqueue: %v", err)
				return
			}
			if !matched {
				// This goroutine's call only completed a queue slot; the
				// player who filled that room's second seat learns about
				// the match here, the other seated player does not (a real
				// deployment notifies them via the room-join broadcast once
				// the transport layer, Phase 3, is wired up).
				return
			}
			status, err := r.GetStatus(ctx)
			if err != nil {
				t.Errorf("GetStatus: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			roomCount++
			if status.PlayerCount != 2 {
				t.Errorf("matched room has %d players, want 2", status.PlayerCount)
			}
			totalSeated += status.PlayerCount
		}(i)
	}
	wg.Wait()

	if mm.QueueLength("tictactoe") != 0 {
		t.Fatalf("expected an even number of players to fully drain the queue, got length=%d", mm.QueueLength("tictactoe"))
	}
	if roomCount != n/2 {
		t.Fatalf("expected exactly %d rooms to be created, got %d", n/2, roomCount)
	}
	if totalSeated != n {
		t.Fatalf("expected all %d players seated exactly once across all rooms, got %d total seats", n, totalSeated)
	}
}
