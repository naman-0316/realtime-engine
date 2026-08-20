package redis

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"

	"realtime-engine/internal/storage"
)

// SessionStore implements storage.SessionRecovery on top of Redis: each
// session is a JSON blob under "session:{id}" with a TTL matching the
// disconnect grace window, so an expired session simply stops existing —
// no separate cleanup sweep is needed (contrast with
// storage/memory.SessionStore, which sweeps explicitly since it has no
// built-in expiry mechanism).
type SessionStore struct {
	rdb *redis.Client
}

func NewSessionStore(c *Client) *SessionStore {
	return &SessionStore{rdb: c.rdb}
}

var _ storage.SessionRecovery = (*SessionStore)(nil)

func sessionKey(sessionID string) string { return "session:" + sessionID }

func (s *SessionStore) SaveSession(ctx context.Context, sessionID string, rec storage.SessionRecord, ttl time.Duration) error {
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	return s.rdb.Set(ctx, sessionKey(sessionID), data, ttl).Err()
}

func (s *SessionStore) LoadSession(ctx context.Context, sessionID string) (storage.SessionRecord, error) {
	data, err := s.rdb.Get(ctx, sessionKey(sessionID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return storage.SessionRecord{}, storage.ErrNotFound
	}
	if err != nil {
		return storage.SessionRecord{}, err
	}
	var rec storage.SessionRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return storage.SessionRecord{}, err
	}
	return rec, nil
}

func (s *SessionStore) DeleteSession(ctx context.Context, sessionID string) error {
	return s.rdb.Del(ctx, sessionKey(sessionID)).Err()
}
