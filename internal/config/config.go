// Package config loads runtime configuration from environment variables,
// with sensible defaults for local development.
package config

import (
	"os"
	"strconv"
	"time"
)

// Config holds every tunable the server needs at startup.
type Config struct {
	HTTPAddr string // e.g. ":8080"

	// NodeID identifies this process both as a Redis room-ownership lease
	// holder and, doubling as PublicAddr, as the ws:// base URL a
	// reconnecting client on a *different* node gets redirected to (see
	// transport/ws/server.go tryResume and storage.SessionRecord.OwnerNode).
	// Defaults to a loopback URL suitable for single-node/local development
	// only; a real multi-node deployment must set NODE_ID to this
	// instance's externally reachable address.
	NodeID string

	JWTSecret     string
	SessionTTL    time.Duration // how long an issued JWT/session stays valid
	GraceDuration time.Duration // disconnect grace window before forfeit

	TickInterval   time.Duration
	FinishedLinger time.Duration
	WaitingTTL     time.Duration
	GCInterval     time.Duration

	PingInterval time.Duration // server->client WS heartbeat interval
	PongTimeout  time.Duration // how long without a pong before a connection is dead

	RateLimitCapacity   float64 // token bucket burst capacity
	RateLimitRefillRate float64 // tokens/sec refill rate

	RedisAddr string // empty disables Redis/multi-node coordination

	LeaseTTL time.Duration // Redis room-ownership lease TTL (renewed at LeaseTTL/3)
}

// Load reads Config from the environment, filling in defaults for anything
// unset. It never fails: invalid numeric/duration values are ignored in
// favor of the default.
func Load() Config {
	httpAddr := envOr("HTTP_ADDR", ":8080")
	c := Config{
		HTTPAddr:            httpAddr,
		NodeID:              envOr("NODE_ID", "ws://localhost"+httpAddr+"/ws"),
		JWTSecret:           envOr("JWT_SECRET", "dev-secret-change-me"),
		SessionTTL:          envDuration("SESSION_TTL", time.Hour),
		GraceDuration:       envDuration("GRACE_DURATION", 30*time.Second),
		TickInterval:        envDuration("TICK_INTERVAL", 100*time.Millisecond),
		FinishedLinger:      envDuration("FINISHED_LINGER", 10*time.Second),
		WaitingTTL:          envDuration("WAITING_TTL", 60*time.Second),
		GCInterval:          envDuration("GC_INTERVAL", 5*time.Second),
		PingInterval:        envDuration("PING_INTERVAL", 15*time.Second),
		PongTimeout:         envDuration("PONG_TIMEOUT", 10*time.Second),
		RateLimitCapacity:   envFloat("RATE_LIMIT_CAPACITY", 20),
		RateLimitRefillRate: envFloat("RATE_LIMIT_REFILL", 10),
		RedisAddr:           envOr("REDIS_ADDR", ""),
		LeaseTTL:            envDuration("LEASE_TTL", 15*time.Second),
	}
	return c
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}
