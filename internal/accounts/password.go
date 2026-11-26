package accounts

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
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
	passwordFieldErrors(&fe, password, confirm, u.Email)
	if err := fe.Err(); err != nil {
		return err
	}

	if u.LockedUntil != nil && now.Before(*u.LockedUntil) {
		return ErrLoginLocked
	}

	ok, err := auth.VerifyPassword(u.PasswordHash, current)
	if err != nil {
		return fmt.Errorf("accounts: change password: check current password: %w", err)
	}
	if !ok {
		locked, err := store.Repos().Users.RecordLoginFailure(ctx, u.ID, now, LoginMaxFailures, LoginLockFor)
		if err != nil {
			return fmt.Errorf("accounts: change password: record failure: %w", err)
		}
		if locked {
			return ErrLoginLocked
		}
		fe.Add("current_password", changeMsgWrongCurrent)
		return fe.Err()
	}
	if u.FailedLogins > 0 {
		if err := store.Repos().Users.ResetLoginFailures(ctx, u.ID); err != nil {
			return fmt.Errorf("accounts: change password: reset failures: %w", err)
		}
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

// SendPasswordReminders mails one password-expiry notice per password cycle to
// the active accounts whose password is in the warning window. It returns the
// number of notices sent. origin is the site address (APP_ORIGIN) the link in
// the email starts with.
func SendPasswordReminders(ctx context.Context, store domain.Store, origin string, now time.Time) (sent int, err error) {
	// The store filters PasswordChangedAt in [from, to). A password expires
	// MaxAge after it changed and is warned about during the last WarnBefore, so
	// on the change-time axis that is (now-MaxAge, now-MaxAge+WarnBefore]. The
	// millisecond nudge turns the store's half-open bounds into that pair: a
	// password with no time left is outside the window, one at exactly
	// WarnBefore is inside.
	const precision = time.Millisecond // MongoDB stores times at millisecond precision
	from := now.Add(-auth.MaxAge).Add(precision)
	to := now.Add(auth.WarnBefore - auth.MaxAge).Add(precision)
	users, err := store.Repos().Users.ListActivePasswordChangedBetween(ctx, from, to, domain.ListLimitMax)
	if err != nil {
		return 0, fmt.Errorf("accounts: send password reminders: list users: %w", err)
	}
	for _, u := range users {
		// The banner in web uses the same rule: warn only while time is left.
		// warn also makes days at least 1, so the template never says 0.
		days, warn := auth.ExpiryWarning(u.PasswordChangedAt, now)
		if !warn {
			continue
		}
		// One notice per password cycle; ChangePassword resets the marker.
		if u.PasswordRemindedAt != nil && !u.PasswordRemindedAt.Before(u.PasswordChangedAt) {
			continue
		}
		if err := store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
			subject, text, html, err := mail.Render(mail.TemplatePasswordExpiring, mail.PasswordExpiringData{
				Name: u.Name,
				Days: days,
				Link: origin + "/senha/trocar",
			})
			if err != nil {
				return fmt.Errorf("accounts: send password reminders: %w", err)
			}
			msg := domain.OutboxMessage{To: u.Email, Subject: subject, Text: text, HTML: html, NextAttemptAt: now, CreatedAt: now}
			if err := r.Outbox.Enqueue(ctx, &msg); err != nil {
				return fmt.Errorf("accounts: send password reminders: enqueue email: %w", err)
			}
			if err := r.Users.MarkPasswordReminded(ctx, u.ID, now); err != nil {
				return fmt.Errorf("accounts: send password reminders: mark reminded: %w", err)
			}
			return nil
		}); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// PasswordPolicyMessage is the pt-BR message for the policy rules a password
// violates, in the order given: the labels of the live checklist after
// "requisitos da senha: ".
func PasswordPolicyMessage(violated []auth.Rule) string {
	labels := make([]string, len(violated))
	for i, rule := range violated {
		labels[i] = auth.RuleLabels[rule]
	}
	return "requisitos da senha: " + strings.Join(labels, ", ")
}

// passwordFieldErrors adds to fe what is wrong with a new password and its
// confirmation, under the keys password and password_confirm: missing, not
// UTF-8, against the policy (email may be empty, which skips the rule that the
// password differs from it) or not equal to the confirmation. It needs no
// store and no hashing, so signup, reset and change run it before anything else.
func passwordFieldErrors(fe *domain.FieldErrors, password, confirm, email string) {
	switch {
	case password == "":
		fe.Add("password", domain.MsgRequired)
	case !utf8.ValidString(password):
		fe.Add("password", domain.MsgInvalid)
	default:
		if violated := auth.CheckPolicy(password, email); len(violated) > 0 {
			fe.Add("password", PasswordPolicyMessage(violated))
		}
	}
	if password != confirm {
		fe.Add("password_confirm", signupMsgPasswordMismatch)
	}
}

// CheckPasswordHistory reports whether password is not in the user's recent
// password history after verifying current against the account's password.
// It returns false without hashing when current or password is empty, password
// exceeds auth.MaxLength runes, or the account is locked.
func CheckPasswordHistory(ctx context.Context, store domain.Store, userID, current, password string, now time.Time) (bool, error) {
	if current == "" || password == "" || utf8.RuneCountInString(password) > auth.MaxLength {
		return false, nil
	}
	users := store.Repos().Users
	u, err := users.ByID(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("accounts: check password history: find account: %w", err)
	}
	if u.Status != domain.UserStatusActive {
		return false, fmt.Errorf("accounts: check password history: account %s is not active", userID)
	}
	if u.LockedUntil != nil && now.Before(*u.LockedUntil) {
		return false, nil
	}
	ok, err := auth.VerifyPassword(u.PasswordHash, current)
	if err != nil {
		return false, fmt.Errorf("accounts: check password history: verify current password: %w", err)
	}
	if !ok {
		if _, err := users.RecordLoginFailure(ctx, u.ID, now, LoginMaxFailures, LoginLockFor); err != nil {
			return false, fmt.Errorf("accounts: check password history: record failure: %w", err)
		}
		return false, nil
	}
	if u.FailedLogins > 0 {
		if err := users.ResetLoginFailures(ctx, u.ID); err != nil {
			return false, fmt.Errorf("accounts: check password history: reset failures: %w", err)
		}
	}
	reused, err := passwordInHistory(ctx, users, userID, password)
	if err != nil {
		return false, err
	}
	return !reused, nil
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
