package covers

import (
	"errors"
	"fmt"
)

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

// HTTPStatusError reports a non-2xx answer from a remote image host. It is a
// distinct type because the status is the only thing that separates "this host
// is refusing us right now" from "there is no such image": a CDN that blocks an
// egress range answers 403 for every artist alike, and a caller that cannot see
// the code has no way to keep that from being recorded as a permanent absence.
type HTTPStatusError struct {
	StatusCode int
	URL        string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("fetch cover: status %d", e.StatusCode)
}
