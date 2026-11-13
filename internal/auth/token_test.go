package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func TestNewTokenRoundTrip(t *testing.T) {
	raw, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 43 {
		t.Errorf("raw token %q has %d characters, want 43 (32 bytes, unpadded base64url)", raw, len(raw))
	}
	if len(hash) != sha256.Size {
		t.Fatalf("hash has %d bytes, want %d", len(hash), sha256.Size)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("raw token does not decode to 32 bytes: %d bytes, %v", len(decoded), err)
	}
	if want := sha256.Sum256(decoded); !bytes.Equal(hash, want[:]) {
		t.Error("hash is not the SHA-256 of the 32 raw bytes")
	}
	got, err := HashToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, hash) {
		t.Error("HashToken(raw) differs from the hash NewToken returned")
	}
}

func TestNewTokenDistinct(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		raw, _, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[raw] {
			t.Fatalf("token %q was generated twice", raw)
		}
		seen[raw] = true
	}
}

func TestHashTokenRejectsMalformed(t *testing.T) {
	raw, _, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 43 {
		t.Fatalf("NewToken returned %q, want 43 characters", raw)
	}
	ff := bytes.Repeat([]byte{0xff}, 32)
	stdAlphabet := base64.RawStdEncoding.EncodeToString(ff) // contains "/"
	urlAlphabet := base64.RawURLEncoding.EncodeToString(ff) // contains "_"
	if stdAlphabet == urlAlphabet {
		t.Fatal("test setup: the two alphabets must differ")
	}
	// The last character of a 32-byte token carries two unused bits that
	// must be zero; the next character decodes to the same bytes if the
	// decoder is lenient.
	last := strings.IndexByte(b64URLAlphabet, raw[42])
	nonCanonical := raw[:42] + string(b64URLAlphabet[last+1])

	tests := []struct{ name, token string }{
		{"empty", ""},
		{"31 bytes", base64.RawURLEncoding.EncodeToString(make([]byte, 31))},
		{"33 bytes", base64.RawURLEncoding.EncodeToString(make([]byte, 33))},
		{"16 bytes", base64.RawURLEncoding.EncodeToString(make([]byte, 16))},
		{"64 bytes", base64.RawURLEncoding.EncodeToString(make([]byte, 64))},
		{"padded", base64.URLEncoding.EncodeToString(make([]byte, 32))},
		{"standard alphabet", stdAlphabet},
		{"not base64", strings.Repeat("!", 43)},
		{"trailing newline", raw + "\n"},
		{"leading space", " " + raw},
		{"hex of 32 bytes", strings.Repeat("ab", 32)},
		{"trailing bits set", nonCanonical},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := HashToken(tc.token)
			if err == nil || hash != nil {
				t.Fatalf("HashToken(%q) = %x, %v; want nil and an error", tc.token, hash, err)
			}
			if !errors.Is(err, ErrInvalidToken) {
				t.Errorf("error %v is not ErrInvalidToken", err)
			}
			if tc.token != "" && strings.Contains(err.Error(), strings.TrimSpace(tc.token)) {
				t.Errorf("error %q repeats the token", err)
			}
		})
	}
	if _, err := HashToken(urlAlphabet); err != nil {
		t.Errorf("HashToken(url alphabet, 32 bytes) = %v, want nil", err)
	}
}

const b64URLAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
