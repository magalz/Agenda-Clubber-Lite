package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const (
	// SessionCookieName is the name of the login cookie. The __Host- prefix makes
	// browsers accept it only with Secure, Path=/ and no Domain.
	SessionCookieName = "__Host-acl_session"

	// SessionIdleTimeout is how long a session lives after its last use...
	SessionIdleTimeout = 7 * 24 * time.Hour
	// SessionMaxLifetime ...and how long after its creation it ends at the latest.
	SessionMaxLifetime = 30 * 24 * time.Hour
	// SessionTouchInterval is the least time between two writes of the last use.
	SessionTouchInterval = 5 * time.Minute
)

// ErrNoSession reports a cookie value that is not a live session: malformed,
// unknown, ended or expired. Callers treat every other error as a server error.
var ErrNoSession = errors.New("auth: no valid session")

// StartSession stores a new session for the user and returns its cookie value
// (only the SHA-256 of it is stored) and the session as stored. An unknown user
// is domain.ErrNotFound, wrapped.
func StartSession(ctx context.Context, sessions domain.Sessions, userID, userAgent string, now time.Time) (raw string, s domain.Session, err error) {
	raw, hash, err := NewToken()
	if err != nil {
		return "", domain.Session{}, err
	}
	created := msUTC(now)
	s = domain.Session{
		Hash:       hash,
		UserID:     userID,
		CreatedAt:  created,
		LastSeenAt: created,
		ExpiresAt:  created.Add(SessionIdleTimeout),
		UserAgent:  cleanUserAgent(userAgent),
	}
	if err := sessions.Create(ctx, s); err != nil {
		return "", domain.Session{}, fmt.Errorf("auth: create session: %w", err)
	}
	return raw, s, nil
}

// ResolveSession returns the live session for a cookie value, or ErrNoSession.
// An expired session is deleted. When the last use is SessionTouchInterval or
// more old it is written again and the expiry slides, never past
// SessionMaxLifetime after the creation; the session returned has those values.
func ResolveSession(ctx context.Context, sessions domain.Sessions, raw string, now time.Time) (domain.Session, error) {
	hash, err := HashToken(raw)
	if err != nil {
		return domain.Session{}, ErrNoSession
	}
	s, err := sessions.Get(ctx, hash)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Session{}, ErrNoSession
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("auth: read session: %w", err)
	}

	lastDay := s.CreatedAt.Add(SessionMaxLifetime)
	if !now.Before(s.ExpiresAt) || !now.Before(lastDay) {
		if err := sessions.Delete(ctx, hash); err != nil {
			return domain.Session{}, fmt.Errorf("auth: delete expired session: %w", err)
		}
		return domain.Session{}, ErrNoSession
	}

	if now.Sub(s.LastSeenAt) < SessionTouchInterval {
		return s, nil
	}
	lastSeen := msUTC(now)
	expiresAt := lastSeen.Add(SessionIdleTimeout)
	if lastDay.Before(expiresAt) {
		expiresAt = lastDay
	}
	err = sessions.Touch(ctx, hash, lastSeen, expiresAt)
	if errors.Is(err, domain.ErrNotFound) { // ended after the read
		return domain.Session{}, ErrNoSession
	}
	if err != nil {
		return domain.Session{}, fmt.Errorf("auth: touch session: %w", err)
	}
	s.LastSeenAt, s.ExpiresAt = lastSeen, expiresAt
	return s, nil
}

// EndSession deletes the session of a cookie value. A malformed or unknown
// value has nothing to end and is not an error.
func EndSession(ctx context.Context, sessions domain.Sessions, raw string) error {
	hash, err := HashToken(raw)
	if err != nil {
		return nil
	}
	if err := sessions.Delete(ctx, hash); err != nil {
		return fmt.Errorf("auth: end session: %w", err)
	}
	return nil
}

// RevokeOtherSessions deletes every session of the user except the one whose
// cookie value is keepRaw. A malformed or empty keepRaw keeps none, which is
// what a password reset wants; a password change keeps the current one.
func RevokeOtherSessions(ctx context.Context, sessions domain.Sessions, userID, keepRaw string) error {
	keep, err := HashToken(keepRaw)
	if err != nil {
		keep = nil
	}
	if err := sessions.DeleteForUser(ctx, userID, keep); err != nil {
		return fmt.Errorf("auth: revoke sessions: %w", err)
	}
	return nil
}

// msUTC returns t in UTC at millisecond precision, the form the store keeps.
func msUTC(t time.Time) time.Time { return t.UTC().Truncate(time.Millisecond) }

// cleanUserAgent returns ua as valid UTF-8 (each run of invalid bytes becomes
// one U+FFFD) cut to domain.UserAgentMax runes, never inside a rune.
func cleanUserAgent(ua string) string {
	ua = strings.ToValidUTF8(ua, "�")
	runes := 0
	for i := range ua {
		if runes == domain.UserAgentMax {
			return ua[:i]
		}
		runes++
	}
	return ua
}
