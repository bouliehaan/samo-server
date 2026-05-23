package users

import (
	"crypto/md5"
	"encoding/hex"
)

func legacyTokenAuth(token, salt string) string {
	sum := md5.Sum([]byte(token + salt))
	return hex.EncodeToString(sum[:])
}

func verifyTokenAuth(password, salt, token string) bool {
	sum := md5.Sum([]byte(password + salt))
	return hex.EncodeToString(sum[:]) == token
}

// VerifyTokenAuth checks Subsonic token auth (md5(password+salt)).
func VerifyTokenAuth(password, salt, token string) bool {
	return verifyTokenAuth(password, salt, token)
}
