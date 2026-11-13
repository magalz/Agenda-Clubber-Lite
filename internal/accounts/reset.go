package accounts

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// ResetTokenTTL is how long a password reset link works.
const ResetTokenTTL = time.Hour

// ErrResetInvalid is the one outcome of ResetPassword the person can see
// besides field errors: the link is malformed, unknown, expired, already used,
// or its account is not active. Any other error is a server error.
var ErrResetInvalid = errors.New("accounts: reset link invalid, used or expired")

// resetHistoryMessage is the pt-BR message for a password the account used lately.
var resetHistoryMessage = fmt.Sprintf("use uma senha diferente das %d últimas", auth.HistorySize)

// RequestPasswordReset mails a reset link when the email belongs to an active
// account; it returns nil in every other case too. Only store failures are errors.
func RequestPasswordReset(ctx context.Context, store domain.Store, origin, email string, now time.Time) error {
	email, err := domain.NormalizeEmail(email)
	if err != nil {
		return nil
	}
	// The random token is made once, outside the transaction, which may run twice.
	raw, hash, err := auth.NewToken()
	if err != nil {
		return fmt.Errorf("accounts: request password reset: %w", err)
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		u, err := r.Users.ByEmail(ctx, email)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		} else if err != nil {
			return fmt.Errorf("accounts: request password reset: find account: %w", err)
		}
		if u.Status != domain.UserStatusActive {
			return nil
		}
		subject, text, html, err := mail.Render(mail.TemplatePasswordReset, mail.PasswordResetData{
			Name: u.Name,
			Link: origin + "/senha/redefinir?token=" + raw,
		})
		if err != nil {
			return fmt.Errorf("accounts: request password reset: %w", err)
		}
		// Create retires the older unused links of the account.
		t := domain.Token{
			UserID:    u.ID,
			Purpose:   domain.TokenPurposeResetPassword,
			Hash:      hash,
			ExpiresAt: now.Add(ResetTokenTTL),
			CreatedAt: now,
		}
		if err := r.Tokens.Create(ctx, &t); err != nil {
			return fmt.Errorf("accounts: request password reset: create token: %w", err)
		}
		msg := domain.OutboxMessage{To: u.Email, Subject: subject, Text: text, HTML: html, NextAttemptAt: now, CreatedAt: now}
		if err := r.Outbox.Enqueue(ctx, &msg); err != nil {
			return fmt.Errorf("accounts: request password reset: enqueue email: %w", err)
		}
		return nil
	})
}

// resetHashPassword hashes the new password; a variable so a test can count
// the calls.
var resetHashPassword = auth.HashPassword

// ResetPassword consumes a reset_password token and sets the new password;
// domain.FieldErrors (keys password, password_confirm) leave the token unused.
// A link that does not work is ErrResetInvalid; any other error is a server
// error. On success every session of the account ends.
func ResetPassword(ctx context.Context, store domain.Store, rawToken, password, confirm string, now time.Time) error {
	hash, err := auth.HashToken(rawToken)
	if err != nil {
		return ErrResetInvalid
	}

	// The checks that need no account come first, so a field error never
	// spends the link.
	var fe domain.FieldErrors
	passwordFieldErrors(&fe, password, confirm, "")
	if err := fe.Err(); err != nil {
		return err
	}

	// A refusal inside the transaction rolls the consumed token back. The new
	// password is hashed only after the link is consumed, so a forged link costs
	// a lookup, not an argon2 hash. A retried transaction hashes again, which is
	// harmless.
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		t, err := r.Tokens.Consume(ctx, domain.TokenPurposeResetPassword, hash, now)
		if errors.Is(err, domain.ErrNotFound) {
			return ErrResetInvalid
		} else if err != nil {
			return fmt.Errorf("accounts: reset password: consume token: %w", err)
		}
		u, err := r.Users.ByID(ctx, t.UserID)
		if errors.Is(err, domain.ErrNotFound) {
			return ErrResetInvalid
		} else if err != nil {
			return fmt.Errorf("accounts: reset password: find account: %w", err)
		}
		if u.Status != domain.UserStatusActive {
			return ErrResetInvalid
		}

		// Only RuleNotEmail needs the account's address.
		if violated := auth.CheckPolicy(password, u.Email); slices.Contains(violated, auth.RuleNotEmail) {
			var fe domain.FieldErrors
			fe.Add("password", PasswordPolicyMessage([]auth.Rule{auth.RuleNotEmail}))
			return fe.Err()
		}
		// A history that cannot be read is an error, never "no match".
		used, err := passwordInHistory(ctx, r.Users, u.ID, password)
		if err != nil {
			return err
		}
		if used {
			var fe domain.FieldErrors
			fe.Add("password", resetHistoryMessage)
			return fe.Err()
		}

		newHash, err := resetHashPassword(password)
		if err != nil {
			return fmt.Errorf("accounts: reset password: %w", err)
		}
		if err := r.Users.ChangePassword(ctx, u.ID, newHash, now); err != nil {
			return fmt.Errorf("accounts: reset password: change password: %w", err)
		}
		if err := r.Sessions.DeleteForUser(ctx, u.ID, nil); err != nil {
			return fmt.Errorf("accounts: reset password: end sessions: %w", err)
		}
		return nil
	})
}
