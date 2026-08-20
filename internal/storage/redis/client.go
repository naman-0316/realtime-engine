// Package redis implements the storage adapter interfaces
// (internal/storage) against a real Redis instance, enabling horizontally
// scaled, multi-node deployments: session recovery works even when a
// reconnect lands on a different node than the one running the room, and
// room-ownership leases ensure exactly one node's actor goroutine is
// authoritative for any given room.
package redis

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// Client wraps a *redis.Client so the other adapters in this package
// (SessionStore, RoomLocator, EventBus) can share one connection pool.
type Client struct {
	rdb *redis.Client
}

// NewClient connects to a single Redis instance at addr (host:port).
func NewClient(addr string) *Client {
	return &Client{rdb: redis.NewClient(&redis.Options{Addr: addr})}
}

// Ping verifies connectivity, used by the /readyz probe.
func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Close releases the underlying connection pool.
func (c *Client) Close() error { return c.rdb.Close() }
