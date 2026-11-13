package accounts

import (
	"context"
	"errors"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// passwordHashPassword hashes the new password; a variable so a test can count
// the calls.
var passwordHashPassword = auth.HashPassword

// ChangePassword sets a new password for a signed-in user after checking the
// current one. domain.FieldErrors use the keys current_password, password and
// password_confirm. On success every other session of the user ends; the
// session whose raw cookie value is keepSession stays.
func ChangePassword(ctx context.Context, store domain.Store, userID, keepSession, current, password, confirm string, now time.Time) error {
	return errors.New("accounts: change password: not implemented")
}

// PasswordReused reports whether password matches one of the user's last two
// hashes; a hash that cannot be read is an error, never false.
func PasswordReused(ctx context.Context, store domain.Store, userID, password string) (bool, error) {
	return false, errors.New("accounts: password reused: not implemented")
}
