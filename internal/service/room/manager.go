package room

import (
	"context"
	"log"
	"sync"
	"time"

	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/storage"
)

// Clock abstracts time.Now so GC-sweep behavior can be tested without real
// sleeps. Production code uses realClock; tests use a manually-advanced
// fake.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// ManagerConfig tunes room behavior and garbage-collection thresholds.
type ManagerConfig struct {
	GraceDuration  time.Duration // disconnect grace window before forfeit
	TickInterval   time.Duration // how often each active room's Game.Tick runs
	FinishedLinger time.Duration // how long a Finished room stays before GC
	WaitingTTL     time.Duration // how long an empty/understaffed Waiting room survives
	Clock          Clock

	// Locator and NodeID enable multi-node coordination: when Locator is
	// non-nil, CreateRoom acquires a Redis-backed ownership lease for the
	// new room under NodeID and renews it for the room's lifetime (see
	// maintainLease). Leave Locator nil for single-node operation — every
	// room is trivially "owned" by the only node running it.
	Locator  storage.RoomLocator
	NodeID   string
	LeaseTTL time.Duration
}

func (c ManagerConfig) withDefaults() ManagerConfig {
	if c.GraceDuration == 0 {
		c.GraceDuration = 30 * time.Second
	}
	if c.TickInterval == 0 {
		c.TickInterval = 100 * time.Millisecond
	}
	if c.FinishedLinger == 0 {
		c.FinishedLinger = 10 * time.Second
	}
	if c.WaitingTTL == 0 {
		c.WaitingTTL = 60 * time.Second
	}
	if c.Clock == nil {
		c.Clock = realClock{}
	}
	if c.LeaseTTL == 0 {
		c.LeaseTTL = 15 * time.Second
	}
	return c
}

// Manager is the thread-safe registry of live rooms. Room creation, lookup,
// and removal are guarded by a single RWMutex over a flat map — a
// deliberately different concurrency strategy than the per-room actor
// model, appropriate here because registry operations are short,
// read-heavy, and unrelated to any single room's game logic.
type Manager struct {
	registry *game.Registry
	sink     Sink
	cfg      ManagerConfig

	mu    sync.RWMutex
	rooms map[ID]*Room

	stopCh chan struct{}
}

// NewManager constructs a Manager. registry supplies Game factories by
// name; sink receives every room's broadcast/unicast events (a real
// deployment wires this to the WebSocket transport layer; tests typically
// use a RecordingSink or NewFanoutSink per test).
func NewManager(registry *game.Registry, sink Sink, cfg ManagerConfig) *Manager {
	return &Manager{
		registry: registry,
		sink:     sink,
		cfg:      cfg.withDefaults(),
		rooms:    make(map[ID]*Room),
		stopCh:   make(chan struct{}),
	}
}

// CreateRoom creates a new room for gameType, immediately seating
// initialPlayers (in order; duplicates are ignored idempotently). If enough
// players are seated to satisfy the game's MinPlayers, the room starts
// Active immediately; otherwise it stays Waiting for further Join calls
// (e.g. from direct join-by-room-ID flows outside the matchmaking queue).
func (m *Manager) CreateRoom(ctx context.Context, gameType string, initialPlayers []game.PlayerID) (*Room, error) {
	g, err := m.registry.New(gameType)
	if err != nil {
		return nil, ErrUnknownGameType
	}

	id := NewID()
	cfg := roomConfig{
		graceDuration:  m.cfg.GraceDuration,
		tickInterval:   m.cfg.TickInterval,
		finishedLinger: m.cfg.FinishedLinger,
	}
	r := newRoom(id, gameType, g, m.sink, cfg, m.remove)

	m.mu.Lock()
	m.rooms[id] = r
	m.mu.Unlock()

	if m.cfg.Locator != nil {
		ok, err := m.cfg.Locator.AcquireRoomLease(ctx, string(id), m.cfg.NodeID, m.cfg.LeaseTTL)
		if err != nil || !ok {
			log.Printf("room %s: failed to acquire ownership lease (err=%v ok=%v); proceeding locally", id, err, ok)
		} else {
			go m.maintainLease(r)
		}
	}

	for _, p := range initialPlayers {
		if _, err := r.Join(ctx, p); err != nil {
			return r, err
		}
	}
	return r, nil
}

// maintainLease renews r's Redis room-ownership lease at LeaseTTL/3 until
// the room's actor stops, then releases it. Runs only when a Locator is
// configured (see CreateRoom).
func (m *Manager) maintainLease(r *Room) {
	interval := m.cfg.LeaseTTL / 3
	if interval <= 0 {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopped:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = m.cfg.Locator.ReleaseRoomLease(ctx, string(r.id), m.cfg.NodeID)
			cancel()
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			ok, err := m.cfg.Locator.RenewRoomLease(ctx, string(r.id), m.cfg.NodeID, m.cfg.LeaseTTL)
			cancel()
			if err == nil && ok {
				continue
			}
			// Renew fails permanently once the key has actually expired
			// (e.g. this goroutine got scheduled late past the TTL, or a
			// transient Redis error caused one cycle to be skipped) —
			// RenewRoomLease's Lua script only PEXPIREs an existing,
			// value-matching key, so it can never recover the key on its
			// own. Self-heal by re-acquiring: if no other node has taken
			// ownership in the meantime this is safe and restores the
			// lease; if another node *did* acquire it, AcquireRoomLease's
			// SetNX simply fails too and we log loudly instead of silently
			// renewing forever against a room ID nobody else can route to.
			acquireCtx, acquireCancel := context.WithTimeout(context.Background(), 2*time.Second)
			reacquired, acquireErr := m.cfg.Locator.AcquireRoomLease(acquireCtx, string(r.id), m.cfg.NodeID, m.cfg.LeaseTTL)
			acquireCancel()
			if acquireErr != nil || !reacquired {
				log.Printf("room %s: lost ownership lease and failed to re-acquire it (renewErr=%v renewOk=%v acquireErr=%v acquireOk=%v)", r.id, err, ok, acquireErr, reacquired)
			}
		}
	}
}

// Get returns the room for id, if it is still registered.
func (m *Manager) Get(id ID) (*Room, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.rooms[id]
	return r, ok
}

// Count returns the number of currently registered rooms.
func (m *Manager) Count() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rooms)
}

// snapshot returns the current room list without holding the lock while
// callers query each room's status (which itself hops through that room's
// actor goroutine and must not happen under m.mu).
func (m *Manager) snapshot() []*Room {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Room, 0, len(m.rooms))
	for _, r := range m.rooms {
		out = append(out, r)
	}
	return out
}

func (m *Manager) remove(id ID) {
	m.mu.Lock()
	delete(m.rooms, id)
	m.mu.Unlock()
}

// RunGC starts a background goroutine sweeping for abandoned Waiting rooms
// and lingering Finished rooms every interval, until ctx is done. Call
// GCSweepOnce directly in tests instead, with a fake Clock, to avoid real
// sleeps.
func (m *Manager) RunGC(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.GCSweepOnce(ctx)
		}
	}
}

// GCSweepOnce inspects every registered room once and force-closes any that
// have become abandoned (Waiting past WaitingTTL with no players, or
// understaffed past WaitingTTL) or have lingered in Finished past
// FinishedLinger. It uses m.cfg.Clock so tests can control "now" precisely.
func (m *Manager) GCSweepOnce(ctx context.Context) {
	now := m.cfg.Clock.Now()
	for _, r := range m.snapshot() {
		status, err := r.GetStatus(ctx)
		if err != nil {
			continue // room already closing/closed
		}
		switch status.Lifecycle {
		case Waiting:
			if now.Sub(status.LastActivityAt) > m.cfg.WaitingTTL {
				_, _ = r.ForceClose(ctx)
			}
		case Finished:
			if now.Sub(status.FinishedAt) > m.cfg.FinishedLinger {
				_, _ = r.ForceClose(ctx)
			}
		}
	}
}
