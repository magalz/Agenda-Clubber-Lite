package accounts

import (
	"context"
	"errors"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// ChangeEmailTokenTTL is how long an email-change link works.
const ChangeEmailTokenTTL = 24 * time.Hour

// ErrEmailChangeInvalid is the one outcome of ConfirmEmailChange the person can
// see: the link is malformed, unknown, expired or already used.
var ErrEmailChangeInvalid = errors.New("accounts: email change link invalid, used or expired")

// UpdateProfile is a RED stub; the implementation follows in the GREEN commit.
func UpdateProfile(context.Context, domain.Store, domain.User, string, string, bool, time.Time) error {
	return nil
}

// RequestEmailChange is a RED stub; the implementation follows in the GREEN commit.
func RequestEmailChange(context.Context, domain.Store, string, domain.User, string, time.Time) error {
	return nil
}

// ConfirmEmailChange is a RED stub; the implementation follows in the GREEN commit.
func ConfirmEmailChange(context.Context, domain.Store, string, time.Time) (domain.User, error) {
	return domain.User{}, ErrEmailChangeInvalid
}

// PurgeUnconfirmedSignups is a RED stub; the implementation follows in the GREEN commit.
func PurgeUnconfirmedSignups(context.Context, domain.Store, time.Time) (int, error) {
	return 0, nil
}
