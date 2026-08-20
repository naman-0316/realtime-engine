package ws

import (
	"net/http"
	"strings"

	"realtime-engine/internal/service/session"
)

// extractToken reads the bearer token from the Authorization header,
// falling back to a "token" query parameter. The query-param fallback
// exists because browser WebSocket clients cannot set arbitrary headers on
// the upgrade request; this project ships no browser client, but keeping
// the fallback documents the tradeoff for any future one (see README).
func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); auth != "" {
		if strings.HasPrefix(auth, "Bearer ") {
			return strings.TrimPrefix(auth, "Bearer ")
		}
		return auth
	}
	return r.URL.Query().Get("token")
}

// authenticate verifies the request's bearer token and returns its claims.
func authenticate(issuer *session.Issuer, r *http.Request) (*session.Claims, error) {
	token := extractToken(r)
	if token == "" {
		return nil, session.ErrInvalidToken
	}
	return issuer.Verify(token)
}
