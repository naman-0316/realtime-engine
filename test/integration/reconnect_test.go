package integration

import (
	"testing"
	"time"
)

// TestReconnectWithinGraceWindowResumesSession closes the second-to-move
// player's connection (simulating a dropped network link, not a graceful
// LEAVE), then opens a fresh WebSocket connection with the same bearer
// token — the way a reconnecting client would — and verifies the room
// resumes rather than forfeiting.
func TestReconnectWithinGraceWindowResumesSession(t *testing.T) {
	srv, _ := newTestServer(t, nil) // GraceDuration=300ms

	tokenAlice := issueToken(t, srv, "alice")
	tokenBob := issueToken(t, srv, "bob")
	connAlice := dialAuthenticated(t, srv, tokenAlice)
	connBob := dialAuthenticated(t, srv, tokenBob)

	findMatch(t, connAlice, "tictactoe")
	findMatch(t, connBob, "tictactoe")
	startAlice := readEnvelopeUntil(t, connAlice, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "ROOM_STARTED" })
	readEnvelopeUntil(t, connBob, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "ROOM_STARTED" })

	// FIND_MATCH travels over two independent connections, so either player
	// may end up seated as X (moves first) — determine it from the actual
	// snapshot rather than assuming send order (see turnPlayer). xConn
	// always has a legal move available first; oConn is the one that will
	// disconnect and reconnect mid-game, since it's guaranteed to have a
	// legal move waiting once it resumes (X's opening move passes turn to O).
	xConn, oConn, oToken := connAlice, connBob, tokenBob
	if turnPlayer(t, startAlice.Snapshot) != "alice" {
		xConn, oConn, oToken = connBob, connAlice, tokenAlice
	}

	sendAction(t, xConn, "ACTION_MOVE", 1, map[string]int{"x": 0, "y": 0})
	readEnvelopeUntil(t, xConn, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "STATE_DIFF" })

	if err := oConn.Close(); err != nil {
		t.Fatalf("close O's connection: %v", err)
	}
	readEnvelopeUntil(t, xConn, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "PLAYER_DISCONNECTED" })

	// Reconnect quickly, well within the 300ms grace window, using the same
	// bearer token (a stable JWT is the whole reconnect mechanism — see
	// internal/service/session).
	oConn2 := dialAuthenticated(t, srv, oToken)
	snap := readEnvelope(t, oConn2, 5*time.Second)
	if snap.Type != "STATE_SNAPSHOT" {
		t.Fatalf("expected a STATE_SNAPSHOT on resume, got %+v", snap)
	}
	if len(snap.Snapshot) == 0 {
		t.Fatalf("expected a non-empty snapshot on resume")
	}

	// The room must still be alive and playable: O should be able to move
	// now that they've reconnected — it's their turn after X's opening move.
	sendAction(t, oConn2, "ACTION_MOVE", 1, map[string]int{"x": 1, "y": 0})
	diff := readEnvelopeUntil(t, xConn, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "STATE_DIFF" })
	if diff.Terminal {
		t.Fatalf("did not expect the game to be over yet")
	}
}
