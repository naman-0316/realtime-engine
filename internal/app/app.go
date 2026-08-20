// Package app wires the domain/service/storage/transport layers into one
// runnable HTTP mux. It exists so cmd/server/main.go and the integration
// tests (test/integration) build the exact same stack instead of
// duplicating wiring logic.
package app

import (
	"context"
	"log"
	"net/http"
	"time"

	"realtime-engine/internal/config"
	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/domain/pingpong"
	"realtime-engine/internal/domain/tictactoe"
	"realtime-engine/internal/service/matchmaking"
	"realtime-engine/internal/service/room"
	"realtime-engine/internal/service/session"
	"realtime-engine/internal/storage"
	"realtime-engine/internal/storage/memory"
	redisstorage "realtime-engine/internal/storage/redis"
	"realtime-engine/internal/transport/httpapi"
	"realtime-engine/internal/transport/ws"
)

// App holds every long-lived component so callers can access them directly
// (tests query the Rooms manager, for instance) alongside the ready-to-serve
// Mux.
type App struct {
	Mux         *http.ServeMux
	Rooms       *room.Manager
	Matchmaker  *matchmaking.Matchmaker
	Registry    *game.Registry
	Issuer      *session.Issuer
	shutdownFns []func()
}

// Build wires the full stack from cfg and returns it ready to serve. Call
// Shutdown when done (stops background goroutines; safe to defer).
func Build(cfg config.Config) *App {
	registry := game.NewRegistry()
	registry.Register("tictactoe", tictactoe.New)
	registry.Register("pingpong", pingpong.New)

	a := &App{Registry: registry}

	var sessions storage.SessionRecovery
	var locator storage.RoomLocator
	var readyProbe httpapi.ReadyProbe
	if cfg.RedisAddr != "" {
		client := redisstorage.NewClient(cfg.RedisAddr)
		sessions = redisstorage.NewSessionStore(client)
		locator = redisstorage.NewRoomLocator(client)
		readyProbe = func() error {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			return client.Ping(ctx)
		}
		a.shutdownFns = append(a.shutdownFns, func() { _ = client.Close() })
		log.Printf("using Redis-backed session recovery and room-ownership leases at %s", cfg.RedisAddr)
	} else {
		memStore := memory.NewSessionStore()
		sessions = memStore
		stopSweep := make(chan struct{})
		go sweepLoop(memStore, cfg.GCInterval, stopSweep)
		a.shutdownFns = append(a.shutdownFns, func() { close(stopSweep) })
	}

	a.Issuer = session.NewIssuer(cfg.JWTSecret, cfg.SessionTTL)

	hub := ws.NewHub()
	a.Rooms = room.NewManager(registry, hub, room.ManagerConfig{
		GraceDuration:  cfg.GraceDuration,
		TickInterval:   cfg.TickInterval,
		FinishedLinger: cfg.FinishedLinger,
		WaitingTTL:     cfg.WaitingTTL,
		Locator:        locator,
		NodeID:         cfg.NodeID,
		LeaseTTL:       cfg.LeaseTTL,
	})
	a.Matchmaker = matchmaking.New(registry, a.Rooms)
	srv := ws.NewServer(cfg, registry, a.Rooms, a.Matchmaker, hub, a.Issuer, sessions)

	gcCtx, cancelGC := context.WithCancel(context.Background())
	go a.Rooms.RunGC(gcCtx, cfg.GCInterval)
	a.shutdownFns = append(a.shutdownFns, cancelGC)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", httpapi.Healthz)
	mux.HandleFunc("/readyz", httpapi.Readyz(readyProbe))
	mux.Handle("/session", httpapi.NewAuthHandler(a.Issuer))
	mux.HandleFunc("/ws", srv.HandleWS)
	a.Mux = mux

	return a
}

// Shutdown stops every background goroutine Build started.
func (a *App) Shutdown() {
	for _, fn := range a.shutdownFns {
		fn()
	}
}

func sweepLoop(store *memory.SessionStore, interval time.Duration, stop <-chan struct{}) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			store.Sweep()
		}
	}
}
