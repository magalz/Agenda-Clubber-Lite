// Package auth holds the password policy, hashing and expiry rules and the
// random tokens used by sessions and email links.
package auth

import (
	"errors"
	"time"
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

// The password rules.
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

// ErrInvalidHash reports a stored password hash that is malformed or whose
// parameters are outside the accepted bounds.
var ErrInvalidHash = errors.New("auth: invalid password hash")

// RuleLabels holds the pt-BR checklist text of each rule.
var RuleLabels = map[Rule]string{}

// Rules returns the checklist order.
func Rules() []Rule { return nil }

// CheckPolicy returns the violated rules in Rules order, or nil.
func CheckPolicy(password, email string) []Rule { return nil }

// HashPassword returns an argon2id PHC string.
func HashPassword(password string) (string, error) { return "", nil }

// VerifyPassword compares password with a PHC argon2id hash.
func VerifyPassword(encoded, password string) (bool, error) { return false, nil }

// DummyVerify spends the work of a real verify.
func DummyVerify(password string) {}

// MatchesAny reports whether password matches any of the hashes.
func MatchesAny(password string, hashes []string) (bool, error) { return false, nil }

// PasswordExpired reports whether the password has reached MaxAge.
func PasswordExpired(changedAt, now time.Time) bool { return false }

// DaysUntilExpiry returns the whole days left, rounded up, never below 0.
func DaysUntilExpiry(changedAt, now time.Time) int { return -1 }

// ExpiryWarning reports whether the expiry warning applies.
func ExpiryWarning(changedAt, now time.Time) (days int, warn bool) { return -1, false }
