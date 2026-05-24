package bookmarks

import "errors"

var (
	ErrDisabled     = errors.New("bookmarks service is disabled")
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid input")
	ErrNotAudiobook = errors.New("audiobook not found")
)
