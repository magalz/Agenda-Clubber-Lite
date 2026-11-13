package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
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
//
// Only the right password shows the state of an account. An unknown or
// malformed email, a wrong password and a locked account all cost one password
// verification (a dummy one where there is no hash to check), so the time taken
// does not tell them apart either; a locked account is refused before its
// password is looked at. A wrong password counts against the account and the
// fifth in a row locks it for LoginLockFor. A password that has expired does not
// stop the login: the web layer sends the person to change it. A stored hash
// that cannot be read is a server error, not a failed login.
func Login(ctx context.Context, store domain.Store, email, password string, now time.Time) (domain.User, error) {
	// No transaction: each call below is atomic on its own, and the slow
	// password check must not run inside a body the store may run twice.
	users := store.Repos().Users

	normalized, err := domain.NormalizeEmail(email)
	if err != nil { // not an address, so no account has it
		auth.DummyVerify(password)
		return domain.User{}, ErrLoginFailed
	}
	u, err := users.ByEmail(ctx, normalized)
	if errors.Is(err, domain.ErrNotFound) {
		auth.DummyVerify(password)
		return domain.User{}, ErrLoginFailed
	}
	if err != nil {
		return domain.User{}, fmt.Errorf("accounts: login: find the user: %w", err)
	}

	if u.LockedUntil != nil && now.Before(*u.LockedUntil) {
		auth.DummyVerify(password)
		return domain.User{}, ErrLoginLocked
	}

	ok, err := auth.VerifyPassword(u.PasswordHash, password)
	if err != nil {
		return domain.User{}, fmt.Errorf("accounts: login: user %s: %w", u.ID, err)
	}
	if !ok {
		locked, err := users.RecordLoginFailure(ctx, u.ID, now, LoginMaxFailures, LoginLockFor)
		if err != nil {
			return domain.User{}, fmt.Errorf("accounts: login: record the failure of user %s: %w", u.ID, err)
		}
		if locked {
			return domain.User{}, ErrLoginLocked
		}
		return domain.User{}, ErrLoginFailed
	}

	switch u.Status {
	case domain.UserStatusPendingEmail:
		return domain.User{}, ErrLoginPendingEmail
	case domain.UserStatusPendingApproval:
		return domain.User{}, ErrLoginPendingApproval
	case domain.UserStatusDisabled:
		return domain.User{}, ErrLoginDisabled
	case domain.UserStatusActive:
	default:
		return domain.User{}, fmt.Errorf("accounts: login: user %s has the unknown status %q", u.ID, u.Status)
	}

	if u.FailedLogins > 0 {
		if err := users.ResetLoginFailures(ctx, u.ID); err != nil {
			return domain.User{}, fmt.Errorf("accounts: login: reset the failures of user %s: %w", u.ID, err)
		}
		u.FailedLogins = 0
	}
	return u, nil
}
