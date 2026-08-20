package ws

import (
	"context"
	"errors"

	"realtime-engine/internal/domain/game"
	"realtime-engine/internal/service/room"
)

// errorCode maps a domain/room/transport error to a stable wire code a
// client can branch on, independent of the (potentially game-specific)
// human-readable message.
func errorCode(err error) string {
	switch {
	case errors.Is(err, game.ErrNotYourTurn):
		return "NOT_YOUR_TURN"
	case errors.Is(err, game.ErrIllegalMove):
		return "ILLEGAL_MOVE"
	case errors.Is(err, game.ErrGameOver):
		return "GAME_OVER"
	case errors.Is(err, game.ErrUnknownActionType):
		return "UNKNOWN_ACTION_TYPE"
	case errors.Is(err, game.ErrInvalidPayload):
		return "INVALID_PAYLOAD"
	case errors.Is(err, game.ErrUnknownPlayer):
		return "UNKNOWN_PLAYER"
	case errors.Is(err, game.ErrInvalidPlayerCount):
		return "INVALID_PLAYER_COUNT"
	case errors.Is(err, room.ErrRoomFull):
		return "ROOM_FULL"
	case errors.Is(err, room.ErrRoomClosed):
		return "ROOM_CLOSED"
	case errors.Is(err, room.ErrRoomNotActive):
		return "ROOM_NOT_ACTIVE"
	case errors.Is(err, room.ErrPlayerDisconnected):
		return "PLAYER_DISCONNECTED"
	case errors.Is(err, room.ErrStaleAction):
		return "STALE_OR_DUPLICATE_SEQ"
	case errors.Is(err, room.ErrUnknownGameType):
		return "UNKNOWN_GAME_TYPE"
	case errors.Is(err, context.DeadlineExceeded):
		return "TIMEOUT"
	default:
		return "INTERNAL_ERROR"
	}
}
