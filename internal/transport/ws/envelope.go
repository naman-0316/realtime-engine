package ws

import (
	"encoding/json"
	"errors"
	"time"

	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/service/room"
)

// Client -> server message types not forwarded as game actions.
const (
	TypeFindMatch = "FIND_MATCH"
	TypeResync    = "RESYNC"
)

// Server -> client message types for control/error flow. Game-state types
// (STATE_DIFF, STATE_SNAPSHOT, PLAYER_JOINED, ...) reuse room.EventKind
// constants directly, since those were defined to already match the wire
// vocabulary described in the project README.
const (
	TypeError    = "ERROR"
	TypeRedirect = "REDIRECT"
)

// ClientEnvelope is the wire shape of every client -> server message:
//
//	{ "type": "ACTION_MOVE", "seq": 42, "ts": 1755678900123, "payload": {"x":1,"y":2} }
type ClientEnvelope struct {
	Type    string          `json:"type"`
	Seq     uint64          `json:"seq,omitempty"`
	Ts      int64           `json:"ts,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ServerEnvelope is the wire shape of every server -> client message.
type ServerEnvelope struct {
	Type      string          `json:"type"`
	RoomID    string          `json:"roomId,omitempty"`
	ServerSeq uint64          `json:"serverSeq,omitempty"`
	Ts        int64           `json:"ts"`
	AckPlayer string          `json:"ackPlayer,omitempty"`
	AckSeq    uint64          `json:"ackSeq,omitempty"`
	Diff      json.RawMessage `json:"diff,omitempty"`
	Snapshot  json.RawMessage `json:"snapshot,omitempty"`
	Events    []string        `json:"events,omitempty"`
	Terminal  bool            `json:"terminal,omitempty"`
	Winner    string          `json:"winner,omitempty"`
	Error     *WireError      `json:"error,omitempty"`
	Redirect  string          `json:"redirectAddr,omitempty"`
}

type WireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

const (
	maxIncomingMessageBytes = 8 * 1024
	maxPayloadBytes         = 4 * 1024
)

var (
	errEnvelopeTooLarge = errors.New("ws: message exceeds maximum size")
	errMissingType      = errors.New("ws: envelope missing required \"type\" field")
	errPayloadTooLarge  = errors.New("ws: action payload exceeds maximum size")
)

// decodeEnvelope performs structural, game-agnostic validation of a raw
// incoming message: valid JSON, required "type" field present, payload
// bounded in size. This is the transport-boundary validation step — it
// runs before a game.Action is ever constructed, let alone handed to
// Game.Validate for game-rules-level checks.
func decodeEnvelope(raw []byte) (ClientEnvelope, error) {
	if len(raw) > maxIncomingMessageBytes {
		return ClientEnvelope{}, errEnvelopeTooLarge
	}
	var env ClientEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return ClientEnvelope{}, err
	}
	if env.Type == "" {
		return ClientEnvelope{}, errMissingType
	}
	if len(env.Payload) > maxPayloadBytes {
		return ClientEnvelope{}, errPayloadTooLarge
	}
	return env, nil
}

func eventNames(events []game.Event) []string {
	if len(events) == 0 {
		return nil
	}
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = string(e.Type)
	}
	return out
}

// buildEnvelope translates a room.RoomEvent (engine-internal) into the wire
// ServerEnvelope shape.
func buildEnvelope(event room.RoomEvent) ServerEnvelope {
	env := ServerEnvelope{
		Type:      string(event.Kind),
		RoomID:    string(event.RoomID),
		ServerSeq: event.ServerSeq,
		Ts:        time.Now().UnixMilli(),
		AckPlayer: string(event.Subject),
		Diff:      json.RawMessage(event.Diff),
		Snapshot:  json.RawMessage(event.Snapshot),
		Events:    eventNames(event.Events),
		Terminal:  event.Terminal,
	}
	if event.Winner != nil {
		env.Winner = string(*event.Winner)
	}
	return env
}

func errorEnvelope(code, message string) ServerEnvelope {
	return ServerEnvelope{Type: TypeError, Ts: time.Now().UnixMilli(), Error: &WireError{Code: code, Message: message}}
}
