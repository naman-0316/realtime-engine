package integration

import (
	"net/http"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWSHandshakeRejectsMissingToken(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	_, resp, err := websocket.DefaultDialer.Dial(wsURL(srv.URL), nil)
	if err == nil {
		t.Fatalf("expected the handshake to fail without a bearer token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("got status %d, want 401", status)
	}
}

func TestWSHandshakeRejectsGarbageToken(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	header := http.Header{"Authorization": []string{"Bearer not-a-real-jwt"}}
	_, resp, err := websocket.DefaultDialer.Dial(wsURL(srv.URL), header)
	if err == nil {
		t.Fatalf("expected the handshake to fail with a garbage token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("got status %d, want 401", status)
	}
}

func TestSessionThenWSHandshakeSucceeds(t *testing.T) {
	srv, _ := newTestServer(t, nil)
	token := issueToken(t, srv, "alice")
	conn := dialAuthenticated(t, srv, token) // fails the test itself on error
	_ = conn
}
