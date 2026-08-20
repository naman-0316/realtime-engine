package integration

import (
	"testing"
	"time"
)

// TestHeartbeatTimeoutTriggersDisconnectAndForfeit simulates a client that
// stops responding to server pings (by installing a ping handler that
// swallows them instead of the default auto-pong behavior). The server
// should detect the dead connection via its read-deadline/pong-timeout,
// disconnect that player from their room, and — since this test's
// GraceDuration is very short — forfeit the game to the opponent.
func TestHeartbeatTimeoutTriggersDisconnectAndForfeit(t *testing.T) {
	srv, _ := newTestServer(t, nil) // PingInterval=200ms, PongTimeout=400ms, GraceDuration=300ms

	tokenAlice := issueToken(t, srv, "alice")
	tokenBob := issueToken(t, srv, "bob")
	connAlice := dialAuthenticated(t, srv, tokenAlice)
	connBob := dialAuthenticated(t, srv, tokenBob)

	// Stop bob's connection from auto-replying to pings (gorilla's default
	// ping handler would otherwise transparently keep the connection alive).
	connBob.SetPingHandler(func(string) error { return nil })

	findMatch(t, connAlice, "tictactoe")
	findMatch(t, connBob, "tictactoe")
	readEnvelopeUntil(t, connAlice, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "ROOM_STARTED" })
	readEnvelopeUntil(t, connBob, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "ROOM_STARTED" })

	// alice should see bob disconnect (pong timeout) and then, after the
	// grace window, a terminal diff declaring her the winner.
	readEnvelopeUntil(t, connAlice, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "PLAYER_DISCONNECTED" })
	terminal := readEnvelopeUntil(t, connAlice, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "STATE_DIFF" && e.Terminal })
	if terminal.Winner != "alice" {
		t.Fatalf("expected alice to win by forfeit after bob's heartbeat timeout, got %+v", terminal)
	}
}
