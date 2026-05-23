package covers

import "errors"

var (
	ErrNotFound        = errors.New("cover not found")
	ErrNoArtwork       = errors.New("no embedded artwork in audio file")
	ErrDisabled        = errors.New("covers service is not configured")
	ErrInvalidPath     = errors.New("invalid cover path")
	ErrInvalidURL      = errors.New("invalid cover url")
	ErrForbiddenHost   = errors.New("cover host is not allowed")
	ErrUnsupportedType = errors.New("unsupported cover content type")
	ErrTooLarge        = errors.New("cover exceeds maximum size")
)
