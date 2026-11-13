package auth

import "errors"

// ErrInvalidToken reports a raw token that is not 32 bytes of canonical
// base64url.
var ErrInvalidToken = errors.New("auth: invalid token")

// NewToken returns a random raw token and its SHA-256 hash.
func NewToken() (raw string, hash []byte, err error) { return "", nil, nil }

// HashToken returns the SHA-256 hash of a raw token.
func HashToken(raw string) ([]byte, error) { return nil, nil }
