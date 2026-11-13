package accounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// SignupTokenTTL is how long the email confirmation link works.
const SignupTokenTTL = 24 * time.Hour

// More pt-BR messages shown next to a signup form field. The messages for a
// missing value and for a length come from domain's wording.
const (
	signupMsgPasswordMismatch = "as senhas não conferem"
	signupMsgKind             = "escolha coletivo ou produtora"
	signupMsgRole             = "escolha o seu papel"
	signupMsgPick             = "escolha um coletivo da lista"
	signupMsgNameInUse        = "nome já em uso; escolha outro"
	signupMsgNewAsOwner       = "quem cria um coletivo novo entra como dono"
	signupMsgControl          = "não use quebras de linha nem caracteres de controle"
)

// SignupInput is the signup form as the person filled it in.
type SignupInput struct {
	Name            string
	Email           string
	Phone           string // as typed; only its digits are kept
	PhoneIsWhatsApp bool
	Password        string
	PasswordConfirm string
	CollectiveKind  domain.CollectiveKind // coletivo or produtora
	CollectiveID    string                // an approved collective picked from the suggestions; empty when a name was typed
	CollectiveName  string                // the typed name, used when CollectiveID is empty
	Role            domain.Role
}

// signupFields holds the form values Signup uses once they passed validation.
type signupFields struct {
	name           string
	email          string // normalized
	phone          string // digits only
	collectiveName string // trimmed; empty when a collective was picked by ID
	nameKey        string // domain.NameKey(collectiveName)
}

// signupPrepared holds what Signup computes before the transaction: the slow
// password hash and the confirmation email. None of it touches the store, so a
// transaction that runs again reuses it.
type signupPrepared struct {
	passwordHash string
	tokenHash    []byte
	subject      string
	text         string
	html         string
}

// Signup registers a person who asked for an account. The account starts as
// pending_email, with a confirmation link mailed to the address, and the person
// asks to join a collective or producer: one that is approved, or a new one
// that they create and that waits for the admin. The admin is not told here.
//
// It returns nil on success and also when the email already has an account: the
// person then gets an email saying so, and nothing else is written, so the
// caller shows the same page either way and the form cannot be used to find
// out which addresses are registered. Every field error, those about the
// collective and the role included, is decided before the account lookup, so a
// registered address with a bad collective or role gets the same field errors
// as a new one (and no email). A domain.FieldErrors holds what the person can
// fix, under the keys name, email, phone, password, password_confirm,
// collective_kind, collective and role; any other error is a server error.
// origin is the site address (APP_ORIGIN) the email links start with.
func Signup(ctx context.Context, store domain.Store, origin string, now time.Time, in SignupInput) error {
	f, err := signupValidate(in)
	if err != nil {
		return err
	}

	// The slow and random parts run outside the transaction, which may run twice.
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return fmt.Errorf("accounts: signup: hash password: %w", err)
	}
	raw, tokenHash, err := auth.NewToken()
	if err != nil {
		return fmt.Errorf("accounts: signup: %w", err)
	}
	subject, text, html, err := mail.Render(mail.TemplateVerifyEmail, mail.VerifyEmailData{
		Name: f.name,
		Link: origin + "/confirmar?token=" + raw,
	})
	if err != nil {
		return fmt.Errorf("accounts: signup: %w", err)
	}
	p := signupPrepared{passwordHash: hash, tokenHash: tokenHash, subject: subject, text: text, html: html}

	err = store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		return signupTx(ctx, r, origin, now, in, f, p)
	})
	if errors.Is(err, domain.ErrEmailTaken) {
		// Another signup for the same address committed between our lookup and
		// our insert. Treat this one as the existing-account case it now is.
		err = store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
			u, err := r.Users.ByEmail(ctx, f.email)
			if err != nil {
				return fmt.Errorf("accounts: signup: find the account that took the email: %w", err)
			}
			return signupNotifyExisting(ctx, r, origin, now, u)
		})
	}
	var fe domain.FieldErrors
	if errors.As(err, &fe) {
		return fe
	}
	return err
}

// signupValidate checks every field and returns all the failures together as a
// domain.FieldErrors. It reads nothing from the store.
func signupValidate(in SignupInput) (signupFields, error) {
	var fe domain.FieldErrors
	var f signupFields

	f.name = strings.TrimSpace(in.Name)
	switch {
	case !utf8.ValidString(f.name):
		fe.Add("name", domain.MsgInvalid)
	case strings.ContainsFunc(f.name, unicode.IsControl):
		fe.Add("name", signupMsgControl)
	default:
		if msg := signupLengthProblem(f.name, domain.UserNameMin, domain.UserNameMax); msg != "" {
			fe.Add("name", msg)
		}
	}

	email, err := domain.NormalizeEmail(in.Email)
	if err != nil {
		var ef domain.FieldErrors
		if !errors.As(err, &ef) {
			return signupFields{}, fmt.Errorf("accounts: signup: normalize email: %w", err)
		}
		fe.Add("email", ef["email"])
		email = strings.TrimSpace(in.Email) // still lets the password rule compare against it
	}
	f.email = email

	f.phone = PhoneDigits(in.Phone)
	if n := len(f.phone); n < domain.PhoneMin || n > domain.PhoneMax {
		fe.Add("phone", fmt.Sprintf("informe de %d a %d dígitos", domain.PhoneMin, domain.PhoneMax))
	}

	passwordFieldErrors(&fe, in.Password, in.PasswordConfirm, email)

	if !in.CollectiveKind.Valid() {
		fe.Add("collective_kind", signupMsgKind)
	}
	if !in.Role.Valid() {
		fe.Add("role", signupMsgRole)
	}

	// A picked collective is looked up in the transaction; only a typed name is
	// checked here.
	if in.CollectiveID == "" {
		f.collectiveName = strings.TrimSpace(in.CollectiveName)
		switch {
		case !utf8.ValidString(f.collectiveName):
			fe.Add("collective", domain.MsgInvalid)
		case strings.ContainsFunc(f.collectiveName, unicode.IsControl):
			fe.Add("collective", signupMsgControl)
		default:
			if msg := signupLengthProblem(f.collectiveName, domain.CollectiveNameMin, domain.CollectiveNameMax); msg != "" {
				fe.Add("collective", msg)
				break
			}
			// A name of only combining marks has no comparison key to store.
			if f.nameKey = domain.NameKey(f.collectiveName); f.nameKey == "" {
				fe.Add("collective", domain.MsgInvalid)
			}
		}
	}
	return f, fe.Err()
}

// PhoneDigits returns the ASCII digits of s, in order: the form a phone number
// is stored in. It builds the result in one pass, so its cost stays linear in
// the length of s.
func PhoneDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r < '0' || r > '9' {
			return -1
		}
		return r
	}, s)
}

// signupLengthProblem returns the pt-BR message for s (already trimmed) when its
// length in runes is outside [min, max], or "". The wording matches domain's.
func signupLengthProblem(s string, min, max int) string {
	n := utf8.RuneCountInString(s)
	switch {
	case n == 0:
		return domain.MsgRequired
	case n >= min && n <= max:
		return ""
	case min <= 1:
		return fmt.Sprintf("use no máximo %d caracteres", max)
	default:
		return fmt.Sprintf("use de %d a %d caracteres", min, max)
	}
}

// signupTx is the transaction body. It builds every entity as a local, because
// the backend may run it again.
//
// The collective is resolved before the email is looked up, so the collective
// and role errors do not depend on whether the address has an account: a form
// that said "name taken" only for new addresses would tell a stranger which
// addresses are registered.
func signupTx(ctx context.Context, r domain.Repos, origin string, now time.Time, in SignupInput, f signupFields, p signupPrepared) error {
	existing, isNew, err := signupResolve(ctx, r, in, f)
	if err != nil {
		return err
	}

	switch u, err := r.Users.ByEmail(ctx, f.email); {
	case err == nil:
		return signupNotifyExisting(ctx, r, origin, now, u)
	case !errors.Is(err, domain.ErrNotFound):
		return signupWrap("find account", err)
	}

	u := domain.User{
		Name:              f.name,
		Email:             f.email,
		Phone:             f.phone,
		PhoneIsWhatsApp:   in.PhoneIsWhatsApp,
		PasswordHash:      p.passwordHash,
		PasswordChangedAt: now,
		Status:            domain.UserStatusPendingEmail,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := r.Users.Create(ctx, &u); err != nil {
		return signupWrap("create user", err)
	}

	m := domain.Membership{
		UserID:      u.ID,
		Role:        in.Role,
		Status:      domain.MembershipStatusPending,
		RequestedAt: now,
	}
	if isNew {
		c := domain.Collective{
			Name:      f.collectiveName,
			NameKey:   f.nameKey,
			Kind:      in.CollectiveKind,
			Status:    domain.CollectiveStatusPending,
			CreatedBy: u.ID,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := r.Collectives.Create(ctx, &c); errors.Is(err, domain.ErrNameTaken) {
			return domain.FieldErrors{"collective": signupMsgNameInUse}
		} else if err != nil {
			return signupWrap("create collective", err)
		}
		// Whoever creates the collective vouches for themselves as its owner; the
		// admin still has to approve both.
		ownerApprovedAt := now
		m.CollectiveID = c.ID
		m.OwnerApprovedBy = u.ID
		m.OwnerApprovedAt = &ownerApprovedAt
	} else {
		m.CollectiveID = existing.ID
	}
	if err := r.Memberships.Create(ctx, m); err != nil {
		return signupWrap("create membership", err)
	}

	t := domain.Token{
		UserID:    u.ID,
		Purpose:   domain.TokenPurposeVerifyEmail,
		Hash:      p.tokenHash,
		ExpiresAt: now.Add(SignupTokenTTL),
		CreatedAt: now,
	}
	if err := r.Tokens.Create(ctx, &t); err != nil {
		return signupWrap("create token", err)
	}

	msg := domain.OutboxMessage{
		To:            f.email,
		Subject:       p.subject,
		Text:          p.text,
		HTML:          p.html,
		NextAttemptAt: now,
		CreatedAt:     now,
	}
	if err := r.Outbox.Enqueue(ctx, &msg); err != nil {
		return signupWrap("enqueue email", err)
	}
	return nil
}

// signupResolve finds the collective the person asks to join. It returns
// isNew when a typed name is free and a new collective is to be created. A
// choice that cannot be used is a domain.FieldErrors. It only reads.
func signupResolve(ctx context.Context, r domain.Repos, in SignupInput, f signupFields) (c domain.Collective, isNew bool, err error) {
	if in.CollectiveID != "" {
		c, err := r.Collectives.ByID(ctx, in.CollectiveID)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			return domain.Collective{}, false, domain.FieldErrors{"collective": signupMsgPick}
		case err != nil:
			return domain.Collective{}, false, signupWrap("find collective", err)
		case !signupJoinable(c, in.CollectiveKind):
			return domain.Collective{}, false, domain.FieldErrors{"collective": signupMsgPick}
		}
		return c, false, nil
	}

	c, err = r.Collectives.ByNameKey(ctx, f.nameKey)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		if in.Role != domain.RoleOwner {
			return domain.Collective{}, false, domain.FieldErrors{"role": signupMsgNewAsOwner}
		}
		return domain.Collective{}, true, nil
	case err != nil:
		return domain.Collective{}, false, signupWrap("find collective", err)
	case !signupJoinable(c, in.CollectiveKind):
		// A pending or rejected collective, or one of the other kind, holds the
		// name. All three read the same, so the form does not say which exist.
		return domain.Collective{}, false, domain.FieldErrors{"collective": signupMsgNameInUse}
	}
	return c, false, nil
}

// signupJoinable reports whether a new member may ask to join c: it must be
// approved and of the kind the person chose.
func signupJoinable(c domain.Collective, kind domain.CollectiveKind) bool {
	return c.Status == domain.CollectiveStatusApproved && c.Kind == kind
}

// signupNotifyExisting queues the email that tells the owner of u that someone
// tried to sign up with their address. It writes nothing else.
func signupNotifyExisting(ctx context.Context, r domain.Repos, origin string, now time.Time, u domain.User) error {
	subject, text, html, err := mail.Render(mail.TemplateExistingAccount, mail.ExistingAccountData{
		Name:               u.Name,
		LoginLink:          origin + "/entrar",
		ForgotPasswordLink: origin + "/senha/esqueci",
	})
	if err != nil {
		return fmt.Errorf("accounts: signup: %w", err)
	}
	msg := domain.OutboxMessage{
		To:            u.Email,
		Subject:       subject,
		Text:          text,
		HTML:          html,
		NextAttemptAt: now,
		CreatedAt:     now,
	}
	if err := r.Outbox.Enqueue(ctx, &msg); err != nil {
		return signupWrap("enqueue existing-account email", err)
	}
	return nil
}

// signupWrap turns the error of a store call into a server error that names the
// step. A domain.FieldErrors from the store means Signup built an entity the
// store refuses, which is a bug and not something the person can fix, so it is
// flattened to text and no longer matches errors.As: only the field errors
// Signup raises itself reach the caller.
func signupWrap(step string, err error) error {
	var fe domain.FieldErrors
	if errors.As(err, &fe) {
		return fmt.Errorf("accounts: signup: %s: the store rejected a validated entity: %s", step, fe.Error())
	}
	return fmt.Errorf("accounts: signup: %s: %w", step, err)
}
