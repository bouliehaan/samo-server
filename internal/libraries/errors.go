package libraries

import "errors"

var (
	ErrNotFound         = errors.New("library not found")
	ErrScanJobNotFound  = errors.New("scan job not found")
	ErrScanInProgress   = errors.New("scan already in progress")
	ErrProtectedLibrary = errors.New("library cannot be modified")
	ErrInvalidLibrary   = errors.New("invalid library")
	ErrPathNotDirectory = errors.New("library path must be an existing directory")
	ErrDuplicatePath    = errors.New("library path already exists")
)
