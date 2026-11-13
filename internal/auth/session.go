package auth

import (
	"context"
	"errors"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const (
	SessionCookieName    = "__Host-acl_session"
	SessionIdleTimeout   = 7 * 24 * time.Hour
	SessionMaxLifetime   = 30 * 24 * time.Hour
	SessionTouchInterval = 5 * time.Minute
)

var ErrNoSession = errors.New("auth: no valid session")

var errNotImplemented = errors.New("auth: not implemented")

func StartSession(ctx context.Context, sessions domain.Sessions, userID, userAgent string, now time.Time) (raw string, s domain.Session, err error) {
	return "", domain.Session{}, errNotImplemented
}

func ResolveSession(ctx context.Context, sessions domain.Sessions, raw string, now time.Time) (domain.Session, error) {
	return domain.Session{}, errNotImplemented
}

func EndSession(ctx context.Context, sessions domain.Sessions, raw string) error {
	return errNotImplemented
}

func RevokeOtherSessions(ctx context.Context, sessions domain.Sessions, userID, keepRaw string) error {
	return errNotImplemented
}

func cleanUserAgent(ua string) string { return ua }
