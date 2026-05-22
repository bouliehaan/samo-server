package playback

import "errors"

var (
	ErrNotFound      = errors.New("playback target not found")
	ErrInvalidTarget = errors.New("invalid playback target kind")
	ErrInvalidState  = errors.New("invalid playback state")
	ErrDisabled      = errors.New("playback service is not configured")
)
