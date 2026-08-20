package session

import (
	"testing"
	"time"

	"realtime-engine/internal/domain/game"
)

func TestIssueThenVerifyRoundTrip(t *testing.T) {
	iss := NewIssuer("test-secret", time.Hour)
	token, sessionID, err := iss.Issue(game.PlayerID("alice"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	claims, err := iss.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims.PlayerID != "alice" {
		t.Errorf("got PlayerID=%q, want alice", claims.PlayerID)
	}
	if claims.ID != sessionID {
		t.Errorf("claims.ID=%q does not match issued sessionID=%q", claims.ID, sessionID)
	}
}

func TestVerifyRejectsExpiredToken(t *testing.T) {
	iss := NewIssuer("test-secret", -time.Second) // already expired
	token, _, err := iss.Issue(game.PlayerID("alice"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := iss.Verify(token); err != ErrInvalidToken {
		t.Fatalf("got err=%v, want ErrInvalidToken for an expired token", err)
	}
}

func TestVerifyRejectsWrongSecret(t *testing.T) {
	iss := NewIssuer("secret-a", time.Hour)
	token, _, err := iss.Issue(game.PlayerID("alice"))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	other := NewIssuer("secret-b", time.Hour)
	if _, err := other.Verify(token); err != ErrInvalidToken {
		t.Fatalf("got err=%v, want ErrInvalidToken for a token signed with a different secret", err)
	}
}

func TestVerifyRejectsGarbageToken(t *testing.T) {
	iss := NewIssuer("secret", time.Hour)
	if _, err := iss.Verify("not-a-jwt"); err != ErrInvalidToken {
		t.Fatalf("got err=%v, want ErrInvalidToken", err)
	}
}
