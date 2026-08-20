package redis

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"realtime-engine/internal/storage"
)

// RoomLocator implements storage.RoomLocator with a single Redis key per
// room ("room:{id}:owner", value = owning node's address, expiring after
// the lease TTL). Renew and Release use Lua scripts so "is this still my
// lease" and "act on it" happen atomically — without the script, a
// GET-then-SET/DEL from Go would race a concurrent lease handoff after this
// node's lease already expired and another node acquired it.
type RoomLocator struct {
	rdb *redis.Client
}

func NewRoomLocator(c *Client) *RoomLocator {
	return &RoomLocator{rdb: c.rdb}
}

var _ storage.RoomLocator = (*RoomLocator)(nil)

func ownerKey(roomID string) string { return "room:" + roomID + ":owner" }

func (l *RoomLocator) AcquireRoomLease(ctx context.Context, roomID, nodeID string, ttl time.Duration) (bool, error) {
	ok, err := l.rdb.SetNX(ctx, ownerKey(roomID), nodeID, ttl).Result()
	if err != nil {
		return false, err
	}
	return ok, nil
}

var renewScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("PEXPIRE", KEYS[1], ARGV[2])
else
	return 0
end
`)

func (l *RoomLocator) RenewRoomLease(ctx context.Context, roomID, nodeID string, ttl time.Duration) (bool, error) {
	res, err := renewScript.Run(ctx, l.rdb, []string{ownerKey(roomID)}, nodeID, ttl.Milliseconds()).Int()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

var releaseScript = redis.NewScript(`
if redis.call("GET", KEYS[1]) == ARGV[1] then
	return redis.call("DEL", KEYS[1])
else
	return 0
end
`)

func (l *RoomLocator) ReleaseRoomLease(ctx context.Context, roomID, nodeID string) error {
	_, err := releaseScript.Run(ctx, l.rdb, []string{ownerKey(roomID)}, nodeID).Int()
	return err
}

func (l *RoomLocator) RoomOwner(ctx context.Context, roomID string) (string, error) {
	v, err := l.rdb.Get(ctx, ownerKey(roomID)).Result()
	if errors.Is(err, redis.Nil) {
		return "", storage.ErrNotFound
	}
	return v, err
}
