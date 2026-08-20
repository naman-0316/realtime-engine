// Package ratelimit implements a token-bucket limiter: chosen over a
// sliding-window log because it allows short legitimate bursts (real-time
// player input is inherently bursty) while still enforcing a steady average
// rate, at O(1) memory and O(1) work per check regardless of connection
// count.
package ratelimit

import (
	"sync"
	"time"
)

// TokenBucket is safe for concurrent use.
type TokenBucket struct {
	mu         sync.Mutex
	capacity   float64
	tokens     float64
	refillRate float64 // tokens per second
	lastRefill time.Time
	now        func() time.Time // overridable for deterministic tests
}

// New constructs a TokenBucket with the given burst capacity and steady
// refillRate (tokens/sec), starting full.
func New(capacity, refillRate float64) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		tokens:     capacity,
		refillRate: refillRate,
		lastRefill: time.Now(),
		now:        time.Now,
	}
}

func (b *TokenBucket) refillLocked() {
	now := b.now()
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed * b.refillRate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.lastRefill = now
}

// Allow consumes one token if available, reporting whether it did.
func (b *TokenBucket) Allow() bool { return b.AllowN(1) }

// AllowN consumes n tokens if available, reporting whether it did. A
// request is rejected wholesale (no partial consumption) if fewer than n
// tokens are currently available.
func (b *TokenBucket) AllowN(n float64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked()
	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}
