// Package memory provides single-process, in-memory implementations of the
// storage adapter interfaces — the default when REDIS_ADDR is unset, and
// used throughout the service-layer tests so they never depend on a real
// Redis instance.
package memory

import (
	"context"
	"sync"
	"time"

	"realtime-engine/internal/storage"
)

type sessionEntry struct {
	record  storage.SessionRecord
	expires time.Time
}

// SessionStore implements storage.SessionRecovery with a mutex-guarded map
// and lazy expiry (checked on read; a background sweep also runs to bound
// memory growth from sessions that are never looked up again).
type SessionStore struct {
	mu      sync.Mutex
	entries map[string]sessionEntry
	now     func() time.Time
}

func NewSessionStore() *SessionStore {
	return &SessionStore{entries: make(map[string]sessionEntry), now: time.Now}
}

var _ storage.SessionRecovery = (*SessionStore)(nil)

func (s *SessionStore) SaveSession(_ context.Context, sessionID string, rec storage.SessionRecord, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[sessionID] = sessionEntry{record: rec, expires: s.now().Add(ttl)}
	return nil
}

func (s *SessionStore) LoadSession(_ context.Context, sessionID string) (storage.SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[sessionID]
	if !ok || s.now().After(e.expires) {
		return storage.SessionRecord{}, storage.ErrNotFound
	}
	return e.record, nil
}

func (s *SessionStore) DeleteSession(_ context.Context, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, sessionID)
	return nil
}

// Sweep removes expired entries; call periodically from a background
// goroutine in production to bound memory (see cmd/server/main.go).
func (s *SessionStore) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, e := range s.entries {
		if now.After(e.expires) {
			delete(s.entries, id)
		}
	}
}
