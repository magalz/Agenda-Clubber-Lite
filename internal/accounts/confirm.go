package accounts

import (
	"context"
	"errors"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// ErrConfirmInvalid is the one outcome of ConfirmEmail the person can see: the
// link is malformed, unknown, expired, already used, or its account is no longer
// waiting for the confirmation. Any other error is a server error.
var ErrConfirmInvalid = errors.New("accounts: confirmation link invalid, used or expired")

// ConfirmEmail is not implemented yet.
func ConfirmEmail(ctx context.Context, store domain.Store, origin, rawToken string, now time.Time) error {
	return errors.New("accounts: confirm: not implemented")
}

// ResendConfirmation is not implemented yet.
func ResendConfirmation(ctx context.Context, store domain.Store, origin, email string, now time.Time) error {
	return errors.New("accounts: resend confirmation: not implemented")
}
