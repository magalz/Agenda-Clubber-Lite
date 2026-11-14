package accounts

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// ErrConfirmInvalid is the one outcome of ConfirmEmail the person can see: the
// link is malformed, unknown, expired, already used, or its account is no longer
// waiting for the confirmation. Any other error is a server error.
var ErrConfirmInvalid = errors.New("accounts: confirmation link invalid, used or expired")

// ConfirmEmail spends the confirmation link rawToken and moves its account from
// pending_email to pending_approval, then queues one admin_new_signup email for
// every active admin so the signup gets looked at. All of it happens in one
// transaction: when any step fails the link stays unspent.
//
// It returns ErrConfirmInvalid, with nothing changed, when the token is
// malformed (the store is not asked), unknown, expired or used, and when its
// account is not pending_email any more. Having no admin to tell is not an
// error. origin is the site address (APP_ORIGIN) the email link starts with.
func ConfirmEmail(ctx context.Context, store domain.Store, origin, rawToken string, now time.Time) error {
	hash, err := auth.HashToken(rawToken)
	if err != nil {
		return ErrConfirmInvalid
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		tok, err := r.Tokens.Consume(ctx, domain.TokenPurposeVerifyEmail, hash, now)
		if errors.Is(err, domain.ErrNotFound) {
			return ErrConfirmInvalid
		} else if err != nil {
			return fmt.Errorf("accounts: confirm: spend token: %w", err)
		}
		// Returning the error rolls the transaction back, so the token is not spent.
		err = r.Users.MarkEmailVerified(ctx, tok.UserID, now)
		if errors.Is(err, domain.ErrConflict) || errors.Is(err, domain.ErrNotFound) {
			return ErrConfirmInvalid
		} else if err != nil {
			return fmt.Errorf("accounts: confirm: mark email verified: %w", err)
		}
		return confirmNotifyAdmins(ctx, r, origin, now, tok.UserID)
	})
}

// confirmNotifyAdmins queues the new-signup notice for every active admin. The
// notice is the same for all of them, so it is rendered once.
func confirmNotifyAdmins(ctx context.Context, r domain.Repos, origin string, now time.Time, userID string) error {
	admins, err := r.Users.ListAdminEmails(ctx)
	if err != nil {
		return fmt.Errorf("accounts: confirm: list admins: %w", err)
	}
	if len(admins) == 0 {
		return nil
	}
	u, err := r.Users.ByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("accounts: confirm: find account: %w", err)
	}
	views, err := r.Memberships.ListByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("accounts: confirm: list memberships: %w", err)
	}
	collectives := make([]mail.SignupCollective, 0, len(views))
	for _, v := range views {
		collectives = append(collectives, mail.SignupCollective{
			Name: v.CollectiveName,
			Kind: confirmKindLabel(v.CollectiveKind),
			Role: confirmRoleLabel(v.Role),
			New:  v.CollectiveStatus == domain.CollectiveStatusPending,
		})
	}
	subject, text, html, err := mail.Render(mail.TemplateAdminNewSignup, mail.AdminNewSignupData{
		UserName:        u.Name,
		UserEmail:       u.Email,
		UserPhone:       confirmPhone(u.Phone),
		PhoneIsWhatsApp: u.PhoneIsWhatsApp,
		Collectives:     collectives,
		AdminLink:       origin + "/admin",
	})
	if err != nil {
		return fmt.Errorf("accounts: confirm: %w", err)
	}
	for _, to := range admins {
		msg := domain.OutboxMessage{To: to, Subject: subject, Text: text, HTML: html, NextAttemptAt: now, CreatedAt: now}
		if err := r.Outbox.Enqueue(ctx, &msg); err != nil {
			return fmt.Errorf("accounts: confirm: enqueue admin notice: %w", err)
		}
	}
	return nil
}

// confirmPhone formats the stored digits the way people write a Brazilian
// number: (81) 98765-4321, or (81) 8765-4321 for ten digits. Stored phones are
// 10 or 11 digits (domain.User.Validate), so the final return is only a guard.
func confirmPhone(digits string) string {
	switch len(digits) {
	case 11:
		return "(" + digits[:2] + ") " + digits[2:7] + "-" + digits[7:]
	case 10:
		return "(" + digits[:2] + ") " + digits[2:6] + "-" + digits[6:]
	}
	return digits
}

// confirmKindLabel and confirmRoleLabel are the Portuguese words the admin
// notice shows.
func confirmKindLabel(k domain.CollectiveKind) string {
	if k == domain.CollectiveKindProdutora {
		return "Produtora"
	}
	return "Coletivo"
}

func confirmRoleLabel(r domain.Role) string {
	if r == domain.RoleOwner {
		return "Administrador"
	}
	return "Membro"
}

// ResendConfirmation mails a new confirmation link to an account that is
// still waiting for one; the older link stops working. For every other address
// (malformed, unknown, or an account that is not pending_email) it does nothing
// and returns nil, so the caller's answer cannot be used to find out which
// addresses are registered. Only a store failure is an error. origin is the
// site address (APP_ORIGIN) the email link starts with.
func ResendConfirmation(ctx context.Context, store domain.Store, origin, email string, now time.Time) error {
	email, err := domain.NormalizeEmail(email)
	if err != nil {
		return nil
	}
	// The random token is made once, outside the transaction, which may run twice.
	raw, hash, err := auth.NewToken()
	if err != nil {
		return fmt.Errorf("accounts: resend confirmation: %w", err)
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		u, err := r.Users.ByEmail(ctx, email)
		if errors.Is(err, domain.ErrNotFound) {
			return nil
		} else if err != nil {
			return fmt.Errorf("accounts: resend confirmation: find account: %w", err)
		}
		if u.Status != domain.UserStatusPendingEmail {
			return nil
		}
		subject, text, html, err := mail.Render(mail.TemplateVerifyEmail, mail.VerifyEmailData{
			Name: u.Name,
			Link: origin + "/confirmar?token=" + raw,
		})
		if err != nil {
			return fmt.Errorf("accounts: resend confirmation: %w", err)
		}
		// Create retires the older unused links of the account.
		t := domain.Token{
			UserID:    u.ID,
			Purpose:   domain.TokenPurposeVerifyEmail,
			Hash:      hash,
			ExpiresAt: now.Add(SignupTokenTTL),
			CreatedAt: now,
		}
		if err := r.Tokens.Create(ctx, &t); err != nil {
			return fmt.Errorf("accounts: resend confirmation: create token: %w", err)
		}
		msg := domain.OutboxMessage{To: u.Email, Subject: subject, Text: text, HTML: html, NextAttemptAt: now, CreatedAt: now}
		if err := r.Outbox.Enqueue(ctx, &msg); err != nil {
			return fmt.Errorf("accounts: resend confirmation: enqueue email: %w", err)
		}
		return nil
	})
}
