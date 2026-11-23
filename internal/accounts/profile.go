package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// ChangeEmailTokenTTL is how long an email-change link works.
const ChangeEmailTokenTTL = 24 * time.Hour

// ErrEmailChangeInvalid is the one outcome of ConfirmEmailChange the person can
// see: the link is malformed, unknown, expired or already used. Any other error
// is a server error.
var ErrEmailChangeInvalid = errors.New("accounts: email change link invalid, used or expired")

// profileMsgOwnEmail is shown next to an address the account already uses.
const profileMsgOwnEmail = "este já é o seu e-mail"

// UpdateProfile saves the person's own name and phone. The values are cleaned
// the way signup cleans them and checked by Validate in the service, because
// Users.UpdateProfile is a filtered write that only the service may validate.
// Sessions are keyed by user id, so none of them changes here. Any other error
// than the returned domain.FieldErrors is a server error.
func UpdateProfile(ctx context.Context, store domain.Store, actor domain.User, name, phone string, phoneIsWhatsApp bool, now time.Time) error {
	updated := actor
	updated.Name = strings.TrimSpace(name)
	updated.Phone = PhoneDigits(phone)
	updated.PhoneIsWhatsApp = phoneIsWhatsApp
	if err := updated.Validate(); err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		if err := r.Users.UpdateProfile(ctx, actor.ID, updated.Name, updated.Phone, phoneIsWhatsApp, now); err != nil {
			return fmt.Errorf("accounts: update profile: update profile: %w", err)
		}
		return nil
	})
}

// RequestEmailChange mails a confirmation link to newEmail and a notice to the
// account's current address. Nothing about the account changes until the link
// is followed; the token carries the new address, and Create retires an older
// unused change_email token of the account.
//
// An address the account already uses is a domain.FieldErrors with the key
// "email". An address another account uses gets only that account's
// existing-account email, and this call still returns nil: the answer cannot
// tell the person whether the address was free. Any other error is a server
// error. origin is the site address (APP_ORIGIN) the link starts with.
func RequestEmailChange(ctx context.Context, store domain.Store, origin string, actor domain.User, newEmail string, now time.Time) error {
	email, err := domain.NormalizeEmail(newEmail)
	if err != nil {
		return err
	}
	if email == actor.Email {
		return domain.FieldErrors{"email": profileMsgOwnEmail}
	}

	// The slow and random parts run outside the transaction, which may run twice.
	raw, hash, err := auth.NewToken()
	if err != nil {
		return fmt.Errorf("accounts: request email change: %w", err)
	}
	subject, text, html, err := mail.Render(mail.TemplateChangeEmail, mail.ChangeEmailData{
		Name: actor.Name,
		Link: origin + "/confirmar-email?token=" + raw,
	})
	if err != nil {
		return fmt.Errorf("accounts: request email change: %w", err)
	}
	noticeSubject, noticeText, noticeHTML, err := mail.Render(mail.TemplateEmailChangeNotice, mail.EmailChangeNoticeData{
		Name:     actor.Name,
		NewEmail: email,
	})
	if err != nil {
		return fmt.Errorf("accounts: request email change: %w", err)
	}

	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		owner, err := r.Users.ByEmail(ctx, email)
		switch {
		case err == nil:
			return profileNotifyExisting(ctx, r, origin, now, owner, email)
		case !errors.Is(err, domain.ErrNotFound):
			return fmt.Errorf("accounts: request email change: find account: %w", err)
		}

		t := domain.Token{
			UserID:    actor.ID,
			Purpose:   domain.TokenPurposeChangeEmail,
			Hash:      hash,
			NewEmail:  email,
			ExpiresAt: now.Add(ChangeEmailTokenTTL),
			CreatedAt: now,
		}
		if err := r.Tokens.Create(ctx, &t); err != nil {
			return fmt.Errorf("accounts: request email change: create token: %w", err)
		}
		msg := domain.OutboxMessage{
			To: email, Subject: subject, Text: text, HTML: html,
			NextAttemptAt: now, CreatedAt: now,
		}
		if err := r.Outbox.Enqueue(ctx, &msg); err != nil {
			return fmt.Errorf("accounts: request email change: enqueue confirmation: %w", err)
		}
		notice := domain.OutboxMessage{
			To: actor.Email, Subject: noticeSubject, Text: noticeText, HTML: noticeHTML,
			NextAttemptAt: now, CreatedAt: now,
		}
		if err := r.Outbox.Enqueue(ctx, &notice); err != nil {
			return fmt.Errorf("accounts: request email change: enqueue notice: %w", err)
		}
		return nil
	})
}

// profileNotifyExisting queues the signup's existing-account email, with the
// owner's name and the usual login and forgot-password links, to the address
// that is already taken. It writes nothing else.
func profileNotifyExisting(ctx context.Context, r domain.Repos, origin string, now time.Time, owner domain.User, to string) error {
	subject, text, html, err := mail.Render(mail.TemplateExistingAccount, mail.ExistingAccountData{
		Name:               owner.Name,
		LoginLink:          origin + "/entrar",
		ForgotPasswordLink: origin + "/senha/esqueci",
	})
	if err != nil {
		return fmt.Errorf("accounts: request email change: %w", err)
	}
	msg := domain.OutboxMessage{
		To: to, Subject: subject, Text: text, HTML: html,
		NextAttemptAt: now, CreatedAt: now,
	}
	if err := r.Outbox.Enqueue(ctx, &msg); err != nil {
		return fmt.Errorf("accounts: request email change: enqueue existing-account email: %w", err)
	}
	return nil
}

// ConfirmEmailChange spends the link rawToken and sets the account's email to
// the one the token carries, in one transaction: a refusal leaves the link
// unspent. The re-read user is returned on success. Sessions are keyed by user
// id, so they all survive the change. A link that does not work is
// ErrEmailChangeInvalid; any other error is a server error.
func ConfirmEmailChange(ctx context.Context, store domain.Store, rawToken string, now time.Time) (domain.User, error) {
	hash, err := auth.HashToken(rawToken)
	if err != nil {
		return domain.User{}, ErrEmailChangeInvalid
	}
	var confirmed domain.User
	err = store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		t, err := r.Tokens.Consume(ctx, domain.TokenPurposeChangeEmail, hash, now)
		if errors.Is(err, domain.ErrNotFound) {
			return ErrEmailChangeInvalid
		} else if err != nil {
			return fmt.Errorf("accounts: confirm email change: consume token: %w", err)
		}
		if err := r.Users.SetEmail(ctx, t.UserID, t.NewEmail, now); err != nil {
			switch {
			case errors.Is(err, domain.ErrNotFound):
				return ErrEmailChangeInvalid
			case errors.Is(err, domain.ErrEmailTaken):
				return fmt.Errorf("accounts: confirm email change: the new address was taken: %w", err)
			default:
				return fmt.Errorf("accounts: confirm email change: set email: %w", err)
			}
		}
		u, err := r.Users.ByID(ctx, t.UserID)
		if err != nil {
			return fmt.Errorf("accounts: confirm email change: find account: %w", err)
		}
		confirmed = u
		return nil
	})
	if err != nil {
		return domain.User{}, err
	}
	return confirmed, nil
}

// PurgeUnconfirmedSignups deletes accounts whose email was never confirmed and
// that were created before the cutoff (seven days before now). Each account is
// deleted in its own transaction, together with the pending collectives it
// created and whose membership is still pending, the way an account rejection
// removes them. Nothing is audited or emailed: the address was never confirmed.
// The count is of accounts deleted by this call; a second call purges nothing.
func PurgeUnconfirmedSignups(ctx context.Context, store domain.Store, now time.Time) (purged int, err error) {
	cutoff := now.Add(-7 * 24 * time.Hour)
	stale, err := store.Repos().Users.ListByStatusCreatedBefore(ctx, domain.UserStatusPendingEmail, cutoff, domain.ListLimitMax)
	if err != nil {
		return 0, fmt.Errorf("accounts: purge unconfirmed signups: list accounts: %w", err)
	}
	for _, u := range stale {
		txErr := store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
			if err := profileDeleteOwnCollectives(ctx, r, u.ID); err != nil {
				return err
			}
			if err := r.Users.Delete(ctx, u.ID); err != nil {
				return fmt.Errorf("accounts: purge unconfirmed signups: delete user: %w", err)
			}
			return nil
		})
		if txErr != nil {
			return purged, txErr
		}
		purged++
	}
	return purged, nil
}

// profileDeleteOwnCollectives deletes the pending collectives the account
// created that still have a pending membership. It reads each collective in the
// caller's transaction, right before the write, as every operation that depends
// on a collective's state does (see internal/domain/repo.go). A membership that
// points at a collective that does not exist is a broken invariant, so the
// caller rolls the whole purge back.
func profileDeleteOwnCollectives(ctx context.Context, r domain.Repos, userID string) error {
	views, err := r.Memberships.ListByUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("accounts: purge unconfirmed signups: list memberships: %w", err)
	}
	for _, v := range views {
		if v.Status != domain.MembershipStatusPending {
			continue
		}
		c, err := r.Collectives.ByID(ctx, v.CollectiveID)
		if errors.Is(err, domain.ErrNotFound) {
			return errors.New("accounts: purge unconfirmed signups: the collective of a membership does not exist")
		} else if err != nil {
			return fmt.Errorf("accounts: purge unconfirmed signups: read collective: %w", err)
		}
		if c.Status != domain.CollectiveStatusPending || c.CreatedBy != userID {
			continue
		}
		if err := r.Collectives.Delete(ctx, c.ID); err != nil {
			return fmt.Errorf("accounts: purge unconfirmed signups: delete collective: %w", err)
		}
	}
	return nil
}
