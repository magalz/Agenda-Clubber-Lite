// Package auth holds the password policy, hashing and expiry rules and the
// random tokens used by sessions and email links.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// Password policy limits.
const (
	MinLength   = 8
	MaxLength   = 128 // in runes
	MaxAge      = 90 * 24 * time.Hour
	WarnBefore  = 7 * 24 * time.Hour
	HistorySize = 2
)

// Rule names one password rule.
type Rule string

// The password rules. RuleHistory is not checked by CheckPolicy: the
// password-change flow checks it with MatchesAny, but the UI lists it.
const (
	RuleMinLength Rule = "min_length"
	RuleMaxLength Rule = "max_length"
	RuleLower     Rule = "lower"
	RuleUpper     Rule = "upper"
	RuleDigit     Rule = "digit"
	RuleSymbol    Rule = "symbol"
	RuleNotEmail  Rule = "not_email"
	RuleHistory   Rule = "history"
)

// RuleLabels holds the short pt-BR text the live checklist shows for each rule.
var RuleLabels = map[Rule]string{
	RuleMinLength: "8 caracteres ou mais",
	RuleLower:     "uma letra minúscula",
	RuleUpper:     "uma letra maiúscula",
	RuleDigit:     "um número",
	RuleSymbol:    "um símbolo",
	RuleNotEmail:  "diferente do seu e-mail",
	RuleMaxLength: "até 128 caracteres",
	RuleHistory:   "diferente das 2 últimas senhas",
}

// Rules returns the rules in the order the checklist shows them. History is
// not included: the UI appends it where it applies.
func Rules() []Rule {
	return []Rule{RuleMinLength, RuleLower, RuleUpper, RuleDigit, RuleSymbol, RuleNotEmail, RuleMaxLength}
}

// CheckPolicy returns the rules password violates, in Rules order, or nil when
// it passes. Length is counted in runes. A symbol is any rune that is neither
// a letter nor a digit, spaces included. email may be empty, which skips
// RuleNotEmail.
func CheckPolicy(password, email string) []Rule {
	n := utf8.RuneCountInString(password)
	var lower, upper, digit, symbol bool
	for _, r := range password {
		switch {
		case unicode.IsLower(r):
			lower = true
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsDigit(r):
			digit = true
		case !unicode.IsLetter(r):
			symbol = true
		}
	}

	var violated []Rule
	for _, rule := range Rules() {
		var ok bool
		switch rule {
		case RuleMinLength:
			ok = n >= MinLength
		case RuleLower:
			ok = lower
		case RuleUpper:
			ok = upper
		case RuleDigit:
			ok = digit
		case RuleSymbol:
			ok = symbol
		case RuleNotEmail:
			ok = differsFromEmail(password, email)
		case RuleMaxLength:
			ok = n <= MaxLength
		}
		if !ok {
			violated = append(violated, rule)
		}
	}
	return violated
}

// differsFromEmail reports whether password, ignoring case and surrounding
// space, is neither the whole email nor its local part (when that has three
// or more runes). A blank email has nothing to differ from.
func differsFromEmail(password, email string) bool {
	email = strings.TrimSpace(email)
	if email == "" {
		return true
	}
	password = strings.TrimSpace(password)
	if strings.EqualFold(password, email) {
		return false
	}
	if at := strings.LastIndexByte(email, '@'); at > 0 {
		local := email[:at]
		if utf8.RuneCountInString(local) >= 3 && strings.EqualFold(password, local) {
			return false
		}
	}
	return true
}

// Hashing parameters (OWASP minimum for argon2id) and the bounds VerifyPassword
// accepts in a stored hash, so a crafted hash cannot make a login burn memory
// or CPU.
const (
	hashMemory  uint32 = 19456 // KiB
	hashTime    uint32 = 2
	hashThreads uint8  = 1
	hashKeyLen  uint32 = 32
	hashSaltLen        = 16

	maxMemory  = 65536 // KiB
	maxTime    = 10
	maxThreads = 4
	minSaltLen = 8
	maxSaltLen = 64
	minKeyLen  = 16
	maxKeyLen  = 64

	paramsFormat = "m=%d,t=%d,p=%d"
)

// phcEncoding is the PHC string base64: standard alphabet, no padding, and
// only the canonical encoding of each value.
var phcEncoding = base64.RawStdEncoding.Strict()

// ErrInvalidHash reports a stored password hash that is malformed or whose
// parameters are outside the accepted bounds. Its text never repeats the hash.
var ErrInvalidHash = errors.New("auth: invalid password hash")

// HashPassword returns the argon2id hash of password with a fresh random salt,
// as a PHC string: $argon2id$v=19$m=19456,t=2,p=1$<salt>$<key>.
func HashPassword(password string) (string, error) {
	salt := make([]byte, hashSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read random salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, hashTime, hashMemory, hashThreads, hashKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$"+paramsFormat+"$%s$%s", argon2.Version,
		hashMemory, hashTime, hashThreads, phcEncoding.EncodeToString(salt), phcEncoding.EncodeToString(key)), nil
}

// phc is a parsed, bounds-checked PHC argon2id hash.
type phc struct {
	memory, time uint32
	threads      uint8
	salt, key    []byte
}

// parsePHC parses encoded strictly: it only accepts the exact form HashPassword
// writes (with any parameters inside the bounds above).
func parsePHC(encoded string) (phc, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return phc{}, fmt.Errorf("%w: unsupported format", ErrInvalidHash)
	}
	var m, t, p uint32
	// Sscanf is lenient about spaces and trailing text; the round trip through
	// Sprintf makes the match exact.
	if _, err := fmt.Sscanf(parts[3], paramsFormat, &m, &t, &p); err != nil || fmt.Sprintf(paramsFormat, m, t, p) != parts[3] {
		return phc{}, fmt.Errorf("%w: malformed parameters", ErrInvalidHash)
	}
	if t < 1 || t > maxTime || p < 1 || p > maxThreads || m < 8*p || m > maxMemory {
		return phc{}, fmt.Errorf("%w: parameters out of bounds", ErrInvalidHash)
	}
	salt, ok := decodeCanonical(phcEncoding, parts[4])
	if !ok || len(salt) < minSaltLen || len(salt) > maxSaltLen {
		return phc{}, fmt.Errorf("%w: malformed salt", ErrInvalidHash)
	}
	key, ok := decodeCanonical(phcEncoding, parts[5])
	if !ok || len(key) < minKeyLen || len(key) > maxKeyLen {
		return phc{}, fmt.Errorf("%w: malformed key", ErrInvalidHash)
	}
	return phc{memory: m, time: t, threads: uint8(p), salt: salt, key: key}, nil
}

// VerifyPassword reports whether password matches the PHC argon2id hash
// encoded. A wrong password is (false, nil); a malformed or out-of-bounds
// hash is an error wrapping ErrInvalidHash.
func VerifyPassword(encoded, password string) (bool, error) {
	h, err := parsePHC(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), h.salt, h.time, h.memory, h.threads, uint32(len(h.key)))
	return subtle.ConstantTimeCompare(got, h.key) == 1, nil
}

// dummyHash is built on first use from a random secret, so DummyVerify costs
// what a verify of a real hash costs.
var dummyHash = sync.OnceValue(func() string {
	h, err := HashPassword(rand.Text())
	if err != nil {
		panic(err) // crypto/rand is unusable
	}
	return h
})

// DummyVerify spends the work of a real VerifyPassword and discards the
// result, so a login for an unknown email takes as long as one for a known
// email. The first call also builds the shared hash.
func DummyVerify(password string) {
	if _, err := VerifyPassword(dummyHash(), password); err != nil {
		panic(err) // the hash was written by HashPassword
	}
}

// MatchesAny reports whether password matches any of hashes (the password
// history). A malformed hash is an error.
func MatchesAny(password string, hashes []string) (bool, error) {
	for _, h := range hashes {
		ok, err := VerifyPassword(h, password)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// untilExpiry returns the time left before a password changed at changedAt
// expires; it is zero or negative once expired.
func untilExpiry(changedAt, now time.Time) time.Duration {
	return changedAt.Add(MaxAge).Sub(now)
}

// PasswordExpired reports whether a password set at changedAt has reached
// MaxAge at now.
func PasswordExpired(changedAt, now time.Time) bool {
	return !now.Before(changedAt.Add(MaxAge))
}

// DaysUntilExpiry returns the whole days left before the password expires,
// rounded up, and 0 once it has expired.
func DaysUntilExpiry(changedAt, now time.Time) int {
	const day = 24 * time.Hour
	left := untilExpiry(changedAt, now)
	if left <= 0 {
		return 0
	}
	days := left / day
	if left%day != 0 { // round up without adding to left: Time.Sub saturates at the largest Duration
		days++
	}
	return int(days)
}

// ExpiryWarning returns DaysUntilExpiry and whether to warn: the password has
// not expired and at most WarnBefore is left.
func ExpiryWarning(changedAt, now time.Time) (days int, warn bool) {
	left := untilExpiry(changedAt, now)
	return DaysUntilExpiry(changedAt, now), left > 0 && left <= WarnBefore
}
