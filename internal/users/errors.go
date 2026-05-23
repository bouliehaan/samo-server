package users

import "errors"

var (
	ErrDisabled        = errors.New("user service is not configured")
	ErrNotFound        = errors.New("user not found")
	ErrInvalidUsername = errors.New("username is invalid")
	ErrInvalidPassword = errors.New("password is invalid")
	ErrUsernameTaken   = errors.New("username is already taken")
	ErrUnauthorized    = errors.New("unauthorized")
	ErrForbidden       = errors.New("forbidden")
	ErrInvalidToken    = errors.New("invalid token")
)
