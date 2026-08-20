// Package session issues and verifies the JWTs that identify a player
// across a WebSocket connection's lifetime, including reconnects. The token
// itself never rotates within a logical session — the disconnect grace
// window is enforced separately via storage.SessionRecovery's TTL, not by
// token expiry, which keeps reconnect simple (present the same bearer
// token; the server decides whether the grace window has passed).
package session

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/oklog/ulid/v2"

	"realtime-engine/internal/domain/game"
)

var ErrInvalidToken = errors.New("session: invalid or expired token")

// Claims is the JWT payload identifying a player and their session.
type Claims struct {
	jwt.RegisteredClaims
	PlayerID string `json:"pid"`
}

// Issuer mints and verifies session JWTs with a fixed HMAC secret.
type Issuer struct {
	secret []byte
	ttl    time.Duration
}

func NewIssuer(secret string, ttl time.Duration) *Issuer {
	return &Issuer{secret: []byte(secret), ttl: ttl}
}

// TTL returns the session lifetime this Issuer mints tokens with.
func (iss *Issuer) TTL() time.Duration { return iss.ttl }

// Issue mints a fresh JWT for a newly assigned playerID, embedding a new
// session ID (JWT "jti") used elsewhere to key SessionRecovery records.
func (iss *Issuer) Issue(playerID game.PlayerID) (token string, sessionID string, err error) {
	sessionID = ulid.Make().String()
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   string(playerID),
			ID:        sessionID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(iss.ttl)),
		},
		PlayerID: string(playerID),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(iss.secret)
	if err != nil {
		return "", "", err
	}
	return signed, sessionID, nil
}

// Verify parses and validates token, returning its Claims if valid.
func (iss *Issuer) Verify(token string) (*Claims, error) {
	claims := &Claims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return iss.secret, nil
	})
	if err != nil || !parsed.Valid {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
