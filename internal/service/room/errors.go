package room

import "errors"

var (
	ErrRoomClosed         = errors.New("room: room is closed")
	ErrRoomFull           = errors.New("room: room is full")
	ErrRoomNotActive      = errors.New("room: room is not active")
	ErrUnknownPlayer      = errors.New("room: player is not part of this room")
	ErrPlayerDisconnected = errors.New("room: player is currently disconnected")
	ErrStaleAction        = errors.New("room: duplicate or stale action sequence number")
	ErrUnknownGameType    = errors.New("room: unknown game type")
)
