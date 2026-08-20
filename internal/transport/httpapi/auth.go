// Package httpapi holds the plain HTTP (non-WebSocket) endpoints: session
// issuance and health checks.
package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/oklog/ulid/v2"

	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/service/session"
)

type AuthHandler struct {
	issuer *session.Issuer
}

func NewAuthHandler(issuer *session.Issuer) *AuthHandler {
	return &AuthHandler{issuer: issuer}
}

type sessionRequest struct {
	PlayerID string `json:"playerId"`
}

type sessionResponse struct {
	PlayerID  string `json:"playerId"`
	SessionID string `json:"sessionId"`
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expiresAt"`
}

// ServeHTTP handles POST /session: mints a JWT identifying the caller as a
// player. If no playerId is supplied, one is generated — real deployments
// would authenticate the caller through some other identity provider first
// and pass its stable ID here; that integration is out of scope for this
// engine, which only needs a stable PlayerID to key rooms/sessions by.
func (h *AuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req sessionRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req) // absent/empty body is fine
	}
	playerID := req.PlayerID
	if playerID == "" {
		playerID = "player-" + ulid.Make().String()
	}

	token, sessionID, err := h.issuer.Issue(game.PlayerID(playerID))
	if err != nil {
		http.Error(w, "failed to issue session", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionResponse{
		PlayerID:  playerID,
		SessionID: sessionID,
		Token:     token,
		ExpiresAt: time.Now().Add(h.issuer.TTL()).UnixMilli(),
	})
}
