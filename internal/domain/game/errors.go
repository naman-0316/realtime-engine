package game

import "errors"

// Sentinel errors games return from Validate/ApplyAction. The transport
// layer maps these to stable wire error codes (see transport/ws/envelope.go)
// without needing to know which concrete game produced them.
var (
	ErrNotYourTurn        = errors.New("game: not your turn")
	ErrIllegalMove        = errors.New("game: illegal move")
	ErrGameOver           = errors.New("game: game already over")
	ErrUnknownActionType  = errors.New("game: unknown action type")
	ErrInvalidPayload     = errors.New("game: invalid action payload")
	ErrInvalidPlayerCount = errors.New("game: invalid player count for this game")
	ErrUnknownPlayer      = errors.New("game: player is not seated in this game")
)
