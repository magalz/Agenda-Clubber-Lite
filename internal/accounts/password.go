package accounts

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// changeMsgWrongCurrent is the pt-BR message for a current password that does
// not match.
const changeMsgWrongCurrent = "senha atual incorreta"

// passwordHashPassword hashes the new password; a variable so a test can count
// the calls.
var passwordHashPassword = auth.HashPassword

// ChangePassword sets a new password for a signed-in user after checking the
// current one. domain.FieldErrors use the keys current_password, password and
// password_confirm. On success every other session of the user ends; the
// session whose raw cookie value is keepSession stays.
//
// The checks that need no hashing come first, so a form with a field error never
// spends an argon2 run; the web handler limits the rest per account. Any error
// that is not a domain.FieldErrors is a server error, an unknown or inactive
// account included.
func ChangePassword(ctx context.Context, store domain.Store, userID, keepSession, current, password, confirm string, now time.Time) error {
	u, err := store.Repos().Users.ByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("accounts: change password: find account: %w", err)
	}
	if u.Status != domain.UserStatusActive {
		return fmt.Errorf("accounts: change password: account %s is not active", userID)
	}

	var fe domain.FieldErrors
	if current == "" {
		fe.Add("current_password", domain.MsgRequired)
	}
	switch {
	case password == "":
		fe.Add("password", domain.MsgRequired)
	case !utf8.ValidString(password):
		fe.Add("password", domain.MsgInvalid)
	default:
		if violated := auth.CheckPolicy(password, u.Email); len(violated) > 0 {
			fe.Add("password", resetPolicyMessage(violated))
		}
	}
	if password != confirm {
		fe.Add("password_confirm", signupMsgPasswordMismatch)
	}
	if err := fe.Err(); err != nil {
		return err
	}

	ok, err := auth.VerifyPassword(u.PasswordHash, current)
	if err != nil {
		return fmt.Errorf("accounts: change password: check current password: %w", err)
	}
	if !ok {
		fe.Add("current_password", changeMsgWrongCurrent)
		return fe.Err()
	}

	newHash, err := passwordHashPassword(password)
	if err != nil {
		return fmt.Errorf("accounts: change password: %w", err)
	}

	// The history check and the write share one transaction, so a refusal changes
	// nothing and no session ends. A retried transaction repeats the check.
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		used, err := passwordInHistory(ctx, r.Users, userID, password)
		if err != nil {
			return err
		}
		if used {
			var fe domain.FieldErrors
			fe.Add("password", resetHistoryMessage)
			return fe.Err()
		}
		if err := r.Users.ChangePassword(ctx, userID, newHash, now); err != nil {
			return fmt.Errorf("accounts: change password: %w", err)
		}
		if err := auth.RevokeOtherSessions(ctx, r.Sessions, userID, keepSession); err != nil {
			return fmt.Errorf("accounts: change password: %w", err)
		}
		return nil
	})
}

// PasswordReused reports whether password matches one of the user's last two
// hashes; a hash that cannot be read is an error, never false.
func PasswordReused(ctx context.Context, store domain.Store, userID, password string) (bool, error) {
	return passwordInHistory(ctx, store.Repos().Users, userID, password)
}

// passwordInHistory reports whether password matches one of the user's last
// auth.HistorySize hashes. A failed read, an unknown account or a hash that
// cannot be read is an error, never "no match".
func passwordInHistory(ctx context.Context, users domain.Users, userID, password string) (bool, error) {
	hashes, err := users.RecentPasswordHashes(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("accounts: password history: %w", err)
	}
	used, err := auth.MatchesAny(password, hashes)
	if err != nil {
		return false, fmt.Errorf("accounts: password history: %w", err)
	}
	return used, nil
}
