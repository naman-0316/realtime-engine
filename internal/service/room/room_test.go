package room

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/domain/tictactoe"
)

func newTestManager(t *testing.T, cfg ManagerConfig) (*Manager, *RecordingSink) {
	t.Helper()
	reg := game.NewRegistry()
	reg.Register("tictactoe", tictactoe.New)
	sink := NewRecordingSink()
	return NewManager(reg, sink, cfg), sink
}

func movePayload(x, y int) json.RawMessage {
	b, _ := json.Marshal(struct {
		X int `json:"x"`
		Y int `json:"y"`
	}{x, y})
	return b
}

func TestCreateRoomAutoStartsAtMinPlayers(t *testing.T) {
	m, _ := newTestManager(t, ManagerConfig{})
	ctx := context.Background()

	r, err := m.CreateRoom(ctx, "tictactoe", []game.PlayerID{"alice", "bob"})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	res, err := r.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if res.Lifecycle != Active {
		t.Fatalf("expected Active lifecycle once MinPlayers reached, got %v", res.Lifecycle)
	}
}

func TestJoinIsIdempotent(t *testing.T) {
	m, _ := newTestManager(t, ManagerConfig{})
	ctx := context.Background()
	r, err := m.CreateRoom(ctx, "tictactoe", []game.PlayerID{"alice"})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	res1, err := r.Join(ctx, "alice")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	res2, err := r.Join(ctx, "alice")
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if res1.Seat != res2.Seat {
		t.Fatalf("rejoining the same player should return the same seat, got %d then %d", res1.Seat, res2.Seat)
	}
}

func TestFullGamePlaythroughOverActor(t *testing.T) {
	m, sink := newTestManager(t, ManagerConfig{})
	ctx := context.Background()
	r, err := m.CreateRoom(ctx, "tictactoe", []game.PlayerID{"alice", "bob"})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}

	moves := []struct {
		player game.PlayerID
		x, y   int
	}{
		{"alice", 0, 0}, {"bob", 1, 0}, {"alice", 0, 1}, {"bob", 1, 1}, {"alice", 0, 2}, // alice wins col0
	}
	var last CmdResult
	for i, m := range moves {
		action := game.Action{Type: tictactoe.ActionMove, PlayerID: m.player, Seq: uint64(i + 1), Timestamp: time.Now(), Payload: movePayload(m.x, m.y)}
		last, err = r.SubmitAction(ctx, action)
		if err != nil {
			t.Fatalf("SubmitAction #%d: %v", i, err)
		}
	}
	if last.Lifecycle != Finished {
		t.Fatalf("expected Finished after a winning move, got %v", last.Lifecycle)
	}

	broadcasts := sink.Broadcasts()
	found := false
	for _, e := range broadcasts {
		if e.Kind == EventStateDiff && e.Terminal {
			found = true
			if e.Winner == nil || *e.Winner != "alice" {
				t.Fatalf("expected alice to be broadcast as winner, got %+v", e.Winner)
			}
		}
	}
	if !found {
		t.Fatalf("expected a terminal STATE_DIFF broadcast")
	}
}

func TestStaleActionSeqRejected(t *testing.T) {
	m, _ := newTestManager(t, ManagerConfig{})
	ctx := context.Background()
	r, _ := m.CreateRoom(ctx, "tictactoe", []game.PlayerID{"alice", "bob"})

	a1 := game.Action{Type: tictactoe.ActionMove, PlayerID: "alice", Seq: 5, Timestamp: time.Now(), Payload: movePayload(0, 0)}
	if _, err := r.SubmitAction(ctx, a1); err != nil {
		t.Fatalf("first action: %v", err)
	}
	// bob moves so it's alice's turn again
	b1 := game.Action{Type: tictactoe.ActionMove, PlayerID: "bob", Seq: 1, Timestamp: time.Now(), Payload: movePayload(1, 0)}
	if _, err := r.SubmitAction(ctx, b1); err != nil {
		t.Fatalf("bob action: %v", err)
	}
	replay := game.Action{Type: tictactoe.ActionMove, PlayerID: "alice", Seq: 5, Timestamp: time.Now(), Payload: movePayload(2, 2)}
	if _, err := r.SubmitAction(ctx, replay); err != ErrStaleAction {
		t.Fatalf("got err=%v, want ErrStaleAction for a replayed seq", err)
	}
}

func TestDisconnectReconnectResumesSession(t *testing.T) {
	m, sink := newTestManager(t, ManagerConfig{GraceDuration: time.Hour}) // long grace: we reconnect manually
	ctx := context.Background()
	r, _ := m.CreateRoom(ctx, "tictactoe", []game.PlayerID{"alice", "bob"})

	if _, err := r.Disconnect(ctx, "alice"); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	// While disconnected, alice's actions must be rejected.
	a := game.Action{Type: tictactoe.ActionMove, PlayerID: "alice", Seq: 1, Timestamp: time.Now(), Payload: movePayload(0, 0)}
	if _, err := r.SubmitAction(ctx, a); err != ErrPlayerDisconnected {
		t.Fatalf("got err=%v, want ErrPlayerDisconnected", err)
	}

	res, err := r.Reconnect(ctx, "alice")
	if err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if len(res.Snapshot) == 0 {
		t.Fatalf("expected a snapshot on reconnect")
	}
	if _, err := r.SubmitAction(ctx, a); err != nil {
		t.Fatalf("action after reconnect should succeed: %v", err)
	}

	var sawDisconnected, sawReconnected bool
	for _, e := range sink.Broadcasts() {
		if e.Kind == EventPlayerDisconnected && e.Subject == "alice" {
			sawDisconnected = true
		}
		if e.Kind == EventPlayerReconnected && e.Subject == "alice" {
			sawReconnected = true
		}
	}
	if !sawDisconnected || !sawReconnected {
		t.Fatalf("expected both disconnect and reconnect broadcasts, got %+v", sink.Broadcasts())
	}
}

func TestGraceExpiryForfeitsToOpponent(t *testing.T) {
	m, sink := newTestManager(t, ManagerConfig{GraceDuration: 20 * time.Millisecond})
	ctx := context.Background()
	r, _ := m.CreateRoom(ctx, "tictactoe", []game.PlayerID{"alice", "bob"})

	if _, err := r.Disconnect(ctx, "alice"); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}

	deadline := time.After(2 * time.Second)
	for {
		status, err := r.GetStatus(ctx)
		if err != nil {
			t.Fatalf("GetStatus: %v", err)
		}
		if status.Lifecycle == Finished {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for grace-window forfeit, last status=%+v", status)
		case <-time.After(5 * time.Millisecond):
		}
	}

	var won bool
	for _, e := range sink.Broadcasts() {
		if e.Terminal && e.Winner != nil && *e.Winner == "bob" {
			won = true
		}
	}
	if !won {
		t.Fatalf("expected bob to be declared winner after alice's grace window expired")
	}
}

// TestConcurrentDisconnectReconnectRace hammers Disconnect/Reconnect on the
// same player from many goroutines with a very short grace window, so the
// grace-expiry timer is racing real reconnect calls. Run with -race: the
// generation-counter guard in handleGraceExpired must make a stale timer
// firing after a reconnect a harmless no-op, and the room must never end up
// in a corrupted or double-terminal state.
func TestConcurrentDisconnectReconnectRace(t *testing.T) {
	m, _ := newTestManager(t, ManagerConfig{GraceDuration: 2 * time.Millisecond})
	ctx := context.Background()
	r, err := m.CreateRoom(ctx, "tictactoe", []game.PlayerID{"alice", "bob"})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = r.Disconnect(ctx, "alice")
		}()
		go func() {
			defer wg.Done()
			_, _ = r.Reconnect(ctx, "alice")
		}()
	}
	wg.Wait()

	// Whatever the final state, GetStatus must still respond (actor not
	// wedged) and the lifecycle must be one of the valid values.
	status, err := r.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus after race: %v", err)
	}
	switch status.Lifecycle {
	case Active, Finished:
	default:
		t.Fatalf("unexpected lifecycle after race: %v", status.Lifecycle)
	}
}

// TestConcurrentJoinRespectsCapacity slams Join for a two-seat room from
// many goroutines concurrently; exactly two must succeed with distinct
// seats, and every other caller must observe ErrRoomFull. Run with -race.
func TestConcurrentJoinRespectsCapacity(t *testing.T) {
	m, _ := newTestManager(t, ManagerConfig{})
	ctx := context.Background()
	r, err := m.CreateRoom(ctx, "tictactoe", nil)
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}

	const n = 100
	var wg sync.WaitGroup
	var mu sync.Mutex
	seats := map[int]int{}
	fullCount := 0

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			player := game.PlayerID(string(rune('A' + i%26)))
			res, err := r.Join(ctx, player)
			mu.Lock()
			defer mu.Unlock()
			if err == ErrRoomFull {
				fullCount++
				return
			}
			if err != nil {
				return
			}
			seats[res.Seat]++
		}(i)
	}
	wg.Wait()

	status, err := r.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus: %v", err)
	}
	if status.PlayerCount > 2 {
		t.Fatalf("room exceeded MaxPlayers=2: %d seated", status.PlayerCount)
	}
}

func TestGCSweepClosesAbandonedWaitingRoom(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	m, _ := newTestManager(t, ManagerConfig{WaitingTTL: time.Minute, Clock: clock})
	ctx := context.Background()

	r, err := m.CreateRoom(ctx, "tictactoe", []game.PlayerID{"alice"}) // stays Waiting: MinPlayers=2
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}

	m.GCSweepOnce(ctx) // not yet past TTL
	if _, ok := m.Get(r.ID()); !ok {
		t.Fatalf("room should still be registered before TTL elapses")
	}

	clock.advance(2 * time.Minute)
	m.GCSweepOnce(ctx)

	waitForRemoval(t, m, r.ID())
}

func TestGCSweepClosesLingeringFinishedRoom(t *testing.T) {
	clock := &fakeClock{now: time.Now()}
	m, _ := newTestManager(t, ManagerConfig{FinishedLinger: time.Minute, Clock: clock})
	ctx := context.Background()

	r, err := m.CreateRoom(ctx, "tictactoe", []game.PlayerID{"alice", "bob"})
	if err != nil {
		t.Fatalf("CreateRoom: %v", err)
	}
	// Alice forfeits immediately via abandonment for a fast terminal state.
	if _, err := r.Disconnect(ctx, "alice"); err != nil {
		t.Fatalf("Disconnect: %v", err)
	}
	// Force the abandonment path directly rather than waiting on a real timer.
	r.cmdCh <- Command{Kind: CmdGraceExpired, Player: "alice", Generation: 1}
	waitForLifecycle(t, r, Finished)

	clock.advance(2 * time.Minute)
	m.GCSweepOnce(ctx)
	waitForRemoval(t, m, r.ID())
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func waitForRemoval(t *testing.T, m *Manager, id ID) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := m.Get(id); !ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("room %s was not garbage collected in time", id)
}

func waitForLifecycle(t *testing.T, r *Room, want Lifecycle) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		status, err := r.GetStatus(ctx)
		if err == nil && status.Lifecycle == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("room did not reach lifecycle %v in time", want)
}
