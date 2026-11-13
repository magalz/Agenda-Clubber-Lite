package accounts

import (
	"context"
	"errors"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// ResetTokenTTL is how long a password reset link works.
const ResetTokenTTL = time.Hour

// ErrResetInvalid is the one outcome of ResetPassword the person can see
// besides field errors: the link is malformed, unknown, expired, already used,
// or its account is not active. Any other error is a server error.
var ErrResetInvalid = errors.New("accounts: reset link invalid, used or expired")

// RequestPasswordReset mails a reset link when the email belongs to an active
// account; it returns nil in every other case too. Only store failures are errors.
func RequestPasswordReset(ctx context.Context, store domain.Store, origin, email string, now time.Time) error {
	return nil
}

// ResetPassword consumes a reset_password token and sets the new password;
// domain.FieldErrors (keys password, password_confirm) leave the token unused.
func ResetPassword(ctx context.Context, store domain.Store, rawToken, password, confirm string, now time.Time) error {
	return nil
}
