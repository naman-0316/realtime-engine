// Package integration exercises the full stack (HTTP session issuance +
// real WebSocket connections + room/matchmaking service layer) the way an
// actual client would, as opposed to internal/service/room's tests, which
// drive the room actor directly with no networking involved.
package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"realtime-engine/internal/app"
	"realtime-engine/internal/config"
)

func newTestServer(t *testing.T, mutate func(*config.Config)) (*httptest.Server, *app.App) {
	t.Helper()
	cfg := config.Load()
	cfg.HTTPAddr = ":0"
	cfg.JWTSecret = "integration-test-secret"
	cfg.GraceDuration = 300 * time.Millisecond
	cfg.TickInterval = 20 * time.Millisecond
	cfg.FinishedLinger = time.Second
	cfg.WaitingTTL = time.Second
	cfg.GCInterval = 50 * time.Millisecond
	cfg.PingInterval = 200 * time.Millisecond
	cfg.PongTimeout = 400 * time.Millisecond
	if mutate != nil {
		mutate(&cfg)
	}

	a := app.Build(cfg)
	srv := httptest.NewServer(a.Mux)
	t.Cleanup(func() {
		srv.Close()
		a.Shutdown()
	})
	return srv, a
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + "/ws"
}

// issueToken calls POST /session and returns the bearer token for a fresh
// player identity.
func issueToken(t *testing.T, srv *httptest.Server, playerID string) string {
	t.Helper()
	body := `{}`
	if playerID != "" {
		b, _ := json.Marshal(map[string]string{"playerId": playerID})
		body = string(b)
	}
	resp, err := http.Post(srv.URL+"/session", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /session: status %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode /session response: %v", err)
	}
	return out.Token
}

// dialAuthenticated opens a WebSocket connection authenticated with token.
func dialAuthenticated(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	header := http.Header{"Authorization": []string{"Bearer " + token}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL(srv.URL), header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial ws (http status %d): %v", status, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

type wireEnvelope struct {
	Type      string          `json:"type"`
	RoomID    string          `json:"roomId,omitempty"`
	ServerSeq uint64          `json:"serverSeq,omitempty"`
	AckPlayer string          `json:"ackPlayer,omitempty"`
	Diff      json.RawMessage `json:"diff,omitempty"`
	Snapshot  json.RawMessage `json:"snapshot,omitempty"`
	Terminal  bool            `json:"terminal,omitempty"`
	Winner    string          `json:"winner,omitempty"`
	Error     *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
	Redirect string `json:"redirectAddr,omitempty"`
}

// turnPlayer extracts the "turn" field (the player ID to move next) from a
// tictactoe STATE_SNAPSHOT/STATE_DIFF payload. Tests must not assume which
// client gets seated as X: FIND_MATCH is sent over two independent TCP
// connections, so the server can process either one first, and the caller
// who happens to complete the match is whichever goroutine wins that race
// — not necessarily the one the test wrote FIND_MATCH for first.
func turnPlayer(t *testing.T, snapshot json.RawMessage) string {
	t.Helper()
	var s struct {
		Turn string `json:"turn"`
	}
	if err := json.Unmarshal(snapshot, &s); err != nil {
		t.Fatalf("parse snapshot for turn: %v", err)
	}
	if s.Turn == "" {
		t.Fatalf("snapshot had no turn field: %s", snapshot)
	}
	return s.Turn
}

func readEnvelope(t *testing.T, conn *websocket.Conn, timeout time.Duration) wireEnvelope {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	var env wireEnvelope
	if err := conn.ReadJSON(&env); err != nil {
		t.Fatalf("read envelope: %v", err)
	}
	return env
}

// readEnvelopeUntil reads envelopes until one satisfies match or timeout
// elapses, skipping any that don't (useful when other broadcast traffic —
// e.g. PLAYER_JOINED — may interleave with the message under test).
func readEnvelopeUntil(t *testing.T, conn *websocket.Conn, timeout time.Duration, match func(wireEnvelope) bool) wireEnvelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatalf("timed out waiting for a matching envelope")
		}
		env := readEnvelope(t, conn, remaining)
		if match(env) {
			return env
		}
	}
}

func findMatch(t *testing.T, conn *websocket.Conn, gameType string) {
	t.Helper()
	msg := map[string]any{"type": "FIND_MATCH", "payload": map[string]string{"gameType": gameType}}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("write FIND_MATCH: %v", err)
	}
}

func sendAction(t *testing.T, conn *websocket.Conn, actionType string, seq uint64, payload any) {
	t.Helper()
	msg := map[string]any{"type": actionType, "seq": seq, "ts": time.Now().UnixMilli(), "payload": payload}
	if err := conn.WriteJSON(msg); err != nil {
		t.Fatalf("write %s: %v", actionType, err)
	}
}
