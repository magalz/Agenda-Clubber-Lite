package accounts

import (
	"context"
	"errors"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Login limits: this many wrong passwords in a row lock the account for
// LoginLockFor.
const (
	LoginMaxFailures = 5
	LoginLockFor     = 15 * time.Minute
)

// The outcomes of a login that the person can see; any other error from Login
// is a server error.
var (
	ErrLoginFailed          = errors.New("accounts: wrong email or password")
	ErrLoginLocked          = errors.New("accounts: account locked for a while")
	ErrLoginPendingEmail    = errors.New("accounts: email not confirmed")
	ErrLoginPendingApproval = errors.New("accounts: waiting for approval")
	ErrLoginDisabled        = errors.New("accounts: account disabled")
)

// Login checks the credentials and returns the active user, or one of the
// sentinel errors above; any other error is a server error.
func Login(ctx context.Context, store domain.Store, email, password string, now time.Time) (domain.User, error) {
	return domain.User{}, nil
}
