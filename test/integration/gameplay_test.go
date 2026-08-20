package integration

import (
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestFullTicTacToeGameOverRealWebSockets(t *testing.T) {
	srv, _ := newTestServer(t, nil)

	tokenAlice := issueToken(t, srv, "alice")
	tokenBob := issueToken(t, srv, "bob")
	connAlice := dialAuthenticated(t, srv, tokenAlice)
	connBob := dialAuthenticated(t, srv, tokenBob)

	findMatch(t, connAlice, "tictactoe")
	findMatch(t, connBob, "tictactoe")

	startAlice := readEnvelopeUntil(t, connAlice, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "ROOM_STARTED" })
	startBob := readEnvelopeUntil(t, connBob, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "ROOM_STARTED" })
	if startAlice.RoomID == "" || startAlice.RoomID != startBob.RoomID {
		t.Fatalf("expected both players seated in the same room, got %q vs %q", startAlice.RoomID, startBob.RoomID)
	}

	// FIND_MATCH travels over two independent connections, so either player
	// may end up seated as X (moves first) — determine it from the actual
	// snapshot rather than assuming send order. See helpers_test.go:turnPlayer.
	first, second := connAlice, connBob
	if turnPlayer(t, startAlice.Snapshot) != "alice" {
		first, second = connBob, connAlice
	}

	// Column-0 win for whichever player is X: (0,0) (1,0) (0,1) (1,1) (0,2).
	type move struct {
		conn *websocket.Conn
		x, y int
	}
	moves := []move{
		{first, 0, 0},
		{second, 1, 0},
		{first, 0, 1},
		{second, 1, 1},
		{first, 0, 2},
	}

	var lastDiff wireEnvelope
	for i, m := range moves {
		sendAction(t, m.conn, "ACTION_MOVE", uint64(i+1), map[string]int{"x": m.x, "y": m.y})
		// Every seated connection observes every broadcast diff, in order;
		// read the next STATE_DIFF from alice's connection regardless of
		// who made the move.
		lastDiff = readEnvelopeUntil(t, connAlice, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "STATE_DIFF" })
	}

	if !lastDiff.Terminal || lastDiff.Winner == "" {
		t.Fatalf("expected a terminal diff with a winner, got %+v", lastDiff)
	}

	// bob must have observed the same terminal broadcast.
	bobTerminal := readEnvelopeUntil(t, connBob, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "STATE_DIFF" && e.Terminal })
	if bobTerminal.Winner != lastDiff.Winner {
		t.Fatalf("expected bob to observe the same winner (%s), got %+v", lastDiff.Winner, bobTerminal)
	}
}

func TestUnknownActionAgainstWrongPlayerRejectedWithError(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	tokenAlice := issueToken(t, srv, "alice")
	tokenBob := issueToken(t, srv, "bob")
	connAlice := dialAuthenticated(t, srv, tokenAlice)
	connBob := dialAuthenticated(t, srv, tokenBob)

	findMatch(t, connAlice, "tictactoe")
	findMatch(t, connBob, "tictactoe")
	startAlice := readEnvelopeUntil(t, connAlice, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "ROOM_STARTED" })
	readEnvelopeUntil(t, connBob, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "ROOM_STARTED" })

	// Whichever connection is NOT seated to move first tries to move
	// anyway — determined dynamically, see turnPlayer.
	notFirst := connBob
	if turnPlayer(t, startAlice.Snapshot) != "alice" {
		notFirst = connAlice
	}

	sendAction(t, notFirst, "ACTION_MOVE", 1, map[string]int{"x": 0, "y": 0})
	errEnv := readEnvelopeUntil(t, notFirst, 5*time.Second, func(e wireEnvelope) bool { return e.Type == "ERROR" })
	if errEnv.Error == nil || errEnv.Error.Code != "NOT_YOUR_TURN" {
		t.Fatalf("expected NOT_YOUR_TURN error, got %+v", errEnv.Error)
	}
}
