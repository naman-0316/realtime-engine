package ratelimit

import (
	"testing"
	"time"
)

func TestAllowsBurstUpToCapacity(t *testing.T) {
	b := New(5, 1)
	fake := time.Now()
	b.now = func() time.Time { return fake }

	for i := 0; i < 5; i++ {
		if !b.Allow() {
			t.Fatalf("expected token %d of 5 to be allowed", i+1)
		}
	}
	if b.Allow() {
		t.Fatalf("expected 6th immediate call to be rejected (bucket exhausted)")
	}
}

func TestRefillsOverTime(t *testing.T) {
	b := New(2, 1) // 1 token/sec
	fake := time.Now()
	b.now = func() time.Time { return fake }

	if !b.Allow() || !b.Allow() {
		t.Fatalf("expected initial burst of 2 to succeed")
	}
	if b.Allow() {
		t.Fatalf("expected bucket to be empty")
	}

	fake = fake.Add(1500 * time.Millisecond) // ~1.5 tokens refilled
	if !b.Allow() {
		t.Fatalf("expected a token to be available after 1.5s at 1/sec refill")
	}
	if b.Allow() {
		t.Fatalf("expected only ~1 token available, not 2")
	}
}

func TestRefillNeverExceedsCapacity(t *testing.T) {
	b := New(3, 100) // fast refill
	fake := time.Now()
	b.now = func() time.Time { return fake }
	b.Allow()

	fake = fake.Add(time.Hour) // plenty of time to overflow if unclamped
	if !b.AllowN(3) {
		t.Fatalf("expected bucket to be refilled to capacity (3), not overflowed")
	}
	if b.Allow() {
		t.Fatalf("expected bucket to be empty immediately after draining exactly capacity")
	}
}

func TestAllowNRejectsWithoutPartialConsumption(t *testing.T) {
	b := New(5, 0)
	fake := time.Now()
	b.now = func() time.Time { return fake }

	if b.AllowN(10) {
		t.Fatalf("expected AllowN(10) to fail against a 5-capacity bucket")
	}
	if !b.AllowN(5) {
		t.Fatalf("expected all 5 tokens to still be available after the rejected request")
	}
}
