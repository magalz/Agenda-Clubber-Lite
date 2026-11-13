package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// tokenSize is the number of random bytes in a token.
const tokenSize = 32

// tokenEncoding is the token text form: URL-safe alphabet, no padding, and
// only the canonical encoding of each value.
var tokenEncoding = base64.RawURLEncoding.Strict()

// ErrInvalidToken reports a raw token that is not 32 bytes of canonical
// base64url. Its text never repeats the token.
var ErrInvalidToken = errors.New("auth: invalid token")

// NewToken returns a random raw token (32 random bytes as base64url) and its
// hash (the SHA-256 of those bytes). Only the hash is stored.
func NewToken() (raw string, hash []byte, err error) {
	b := make([]byte, tokenSize)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("auth: read random token: %w", err)
	}
	sum := sha256.Sum256(b)
	return tokenEncoding.EncodeToString(b), sum[:], nil
}

// HashToken returns the hash NewToken produced for raw. Anything that is not
// exactly 32 bytes of canonical base64url is ErrInvalidToken.
func HashToken(raw string) ([]byte, error) {
	b, ok := decodeCanonical(tokenEncoding, raw)
	if !ok || len(b) != tokenSize {
		return nil, ErrInvalidToken
	}
	sum := sha256.Sum256(b)
	return sum[:], nil
}

// decodeCanonical decodes s only when it is the one canonical encoding of the
// bytes it yields. enc must be Strict, which rejects non-zero trailing bits;
// the length check rejects the line breaks the decoder would otherwise skip.
func decodeCanonical(enc *base64.Encoding, s string) ([]byte, bool) {
	b, err := enc.DecodeString(s)
	if err != nil || enc.EncodedLen(len(b)) != len(s) {
		return nil, false
	}
	return b, true
}
