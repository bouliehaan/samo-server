package shelfuser

import "errors"

var (
	ErrDisabled     = errors.New("shelf user service is disabled")
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrInvalidInput = errors.New("invalid input")
	ErrNotAudiobook = errors.New("item is not an audiobook shelf item")
)
