package users

import "errors"

var (
	ErrProtectedUser   = errors.New("the server account and your own admin access cannot be removed")
	ErrLastAdmin       = errors.New("at least one administrator with a password is required")
	ErrInvalidRole     = errors.New("role must be admin or user")
	ErrDisabled        = errors.New("user service is not configured")
	ErrNotFound        = errors.New("user not found")
	ErrInvalidUsername = errors.New("username is invalid")
	ErrInvalidPassword = errors.New("password is invalid")
	ErrUsernameTaken   = errors.New("username is already taken")
	ErrUnauthorized    = errors.New("unauthorized")
	ErrForbidden       = errors.New("forbidden")
	ErrInvalidToken    = errors.New("invalid token")
	ErrSharedToken     = errors.New("the server's shared API token cannot be revoked by signing out")
)
