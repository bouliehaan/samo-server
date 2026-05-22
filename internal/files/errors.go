package files

import "errors"

var (
	ErrNotFound    = errors.New("media file not found")
	ErrForbidden   = errors.New("path is outside configured libraries")
	ErrMissing     = errors.New("media file is missing on disk")
	ErrDisabled    = errors.New("files service is not configured")
	ErrInvalidPath = errors.New("invalid file path")
)
