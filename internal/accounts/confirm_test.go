package accounts

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const confirmOrigin = "https://agenda.example"

// confirmAt is when the person follows the link: an hour after it was issued.
var confirmAt = testNow.Add(time.Hour)

// confirmFarFuture is later than every token expiry, so Tokens.DeleteExpired(...,
// confirmFarFuture) removes and counts every token in the test database.
var confirmFarFuture = testNow.Add(10 * 365 * 24 * time.Hour)

// confirmPendingUser stores an account that has not confirmed its email yet.
func confirmPendingUser(t *testing.T, s domain.Store, email string, mutate ...func(*domain.User)) domain.User {
	t.Helper()
	fixture := func(u *domain.User) {
		u.Name = "Ana Souza"
		u.Status = domain.UserStatusPendingEmail
		u.EmailVerifiedAt = nil
		u.PhoneIsWhatsApp = true
	}
	return insertActiveUser(t, s, email, append([]func(*domain.User){fixture}, mutate...)...)
}

// confirmAdmin stores an active admin.
func confirmAdmin(t *testing.T, s domain.Store, email string) domain.User {
	t.Helper()
	return insertActiveUser(t, s, email, func(u *domain.User) { u.IsAdmin = true })
}

// confirmToken stores a verification token for the user, issued at the given
// time, and returns its raw form (what the link carries).
func confirmToken(t *testing.T, s domain.Store, userID string, issued time.Time) string {
	t.Helper()
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	tok := domain.Token{UserID: userID, Purpose: domain.TokenPurposeVerifyEmail, Hash: hash, ExpiresAt: issued.Add(SignupTokenTTL), CreatedAt: issued}
	if err := s.Repos().Tokens.Create(t.Context(), &tok); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return raw
}

// confirmInsertCollective stores a collective in the given state.
func confirmInsertCollective(t *testing.T, s domain.Store, name string, kind domain.CollectiveKind, status domain.CollectiveStatus) domain.Collective {
	t.Helper()
	c := domain.Collective{Name: name, NameKey: domain.NameKey(name), Kind: kind, Status: status, CreatedAt: testNow, UpdatedAt: testNow}
	if err := s.Repos().Collectives.Create(t.Context(), &c); err != nil {
		t.Fatalf("insert collective %q: %v", name, err)
	}
	return c
}

// confirmJoin makes the user a pending member of the collective.
func confirmJoin(t *testing.T, s domain.Store, userID string, c domain.Collective, role domain.Role) {
	t.Helper()
	m := domain.Membership{UserID: userID, CollectiveID: c.ID, Role: role, Status: domain.MembershipStatusPending, RequestedAt: testNow}
	if err := s.Repos().Memberships.Create(t.Context(), m); err != nil {
		t.Fatalf("join %q: %v", c.Name, err)
	}
}

// confirmOutbox claims the messages due at the given time. It leases them, so
// call it once per test.
func confirmOutbox(t *testing.T, s domain.Store, at time.Time) []domain.OutboxMessage {
	t.Helper()
	msgs, err := s.Repos().Outbox.Claim(t.Context(), at, time.Minute, 10)
	if err != nil {
		t.Fatalf("claim outbox: %v", err)
	}
	return msgs
}

// confirmLinkToken returns the token in the confirmation link of an email text.
func confirmLinkToken(t *testing.T, text string) string {
	t.Helper()
	prefix := confirmOrigin + "/confirmar?token="
	i := strings.Index(text, prefix)
	if i < 0 {
		t.Fatalf("email text has no %q link:\n%s", prefix, text)
	}
	raw := text[i+len(prefix):]
	if end := strings.IndexAny(raw, " \t\r\n"); end >= 0 {
		raw = raw[:end]
	}
	return raw
}

// confirmLine returns the trimmed line of text that contains needle.
func confirmLine(t *testing.T, text, needle string) string {
	t.Helper()
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("no line with %q in:\n%s", needle, text)
	return ""
}

// confirmAssertUntouched asserts that a refused link changed nothing: the
// account still waits for its confirmation and no message was queued.
func confirmAssertUntouched(t *testing.T, s domain.Store, userID string) {
	t.Helper()
	u, err := s.Repos().Users.ByID(t.Context(), userID)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if u.Status != domain.UserStatusPendingEmail || u.EmailVerifiedAt != nil {
		t.Errorf("user status %q verified %v after the refused link, want pending_email and unverified", u.Status, u.EmailVerifiedAt)
	}
	if msgs := confirmOutbox(t, s, confirmFarFuture); len(msgs) != 0 {
		t.Errorf("outbox holds %d messages after the refused link, want none", len(msgs))
	}
}

// confirmAssertTokenUnspent asserts that the token still works, which spends it.
func confirmAssertTokenUnspent(t *testing.T, s domain.Store, raw string, at time.Time) {
	t.Helper()
	hash, err := auth.HashToken(raw)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}
	if _, err := s.Repos().Tokens.Consume(t.Context(), domain.TokenPurposeVerifyEmail, hash, at); err != nil {
		t.Errorf("the token was spent by the refused link: %v", err)
	}
}

// confirmFailingStore is a store whose transactions fail.
type confirmFailingStore struct{ domain.Store }

func (confirmFailingStore) WithTx(context.Context, func(context.Context, domain.Repos) error) error {
	return errors.New("the database is down")
}

func TestConfirmEmail(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	u := confirmPendingUser(t, s, "ana@example.com")
	noite := confirmInsertCollective(t, s, "Noite Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusPending)
	casa := confirmInsertCollective(t, s, "Casa Aurora", domain.CollectiveKindProdutora, domain.CollectiveStatusApproved)
	confirmJoin(t, s, u.ID, noite, domain.RoleOwner)
	confirmJoin(t, s, u.ID, casa, domain.RoleMember)
	confirmAdmin(t, s, "admin2@example.com")
	confirmAdmin(t, s, "admin1@example.com")
	insertActiveUser(t, s, "off@example.com", func(u *domain.User) { u.IsAdmin = true; u.Status = domain.UserStatusDisabled })
	insertActiveUser(t, s, "other@example.com")
	raw := confirmToken(t, s, u.ID, testNow)

	if err := ConfirmEmail(ctx, s, confirmOrigin, raw, confirmAt); err != nil {
		t.Fatalf("ConfirmEmail: %v", err)
	}
	got, err := s.Repos().Users.ByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if got.Status != domain.UserStatusPendingApproval {
		t.Errorf("status = %q, want pending_approval", got.Status)
	}
	if got.EmailVerifiedAt == nil || !got.EmailVerifiedAt.Equal(confirmAt) {
		t.Errorf("EmailVerifiedAt = %v, want %v", got.EmailVerifiedAt, confirmAt)
	}
	if got.ApprovedAt != nil || got.ApprovedBy != "" {
		t.Errorf("the account is already approved: at %v by %q", got.ApprovedAt, got.ApprovedBy)
	}

	// The link works once, and its second use queues nothing more.
	if err := ConfirmEmail(ctx, s, confirmOrigin, raw, confirmAt); !errors.Is(err, ErrConfirmInvalid) {
		t.Errorf("second use: err = %v, want ErrConfirmInvalid", err)
	}

	msgs := confirmOutbox(t, s, confirmAt)
	var to []string
	for _, m := range msgs {
		to = append(to, m.To)
	}
	slices.Sort(to)
	if want := []string{"admin1@example.com", "admin2@example.com"}; !slices.Equal(to, want) {
		t.Fatalf("messages to %v, want one for each active admin %v", to, want)
	}
	for _, m := range msgs {
		if m.Subject != "Novo cadastro para aprovar: Ana Souza" {
			t.Errorf("subject to %s = %q", m.To, m.Subject)
		}
		if !m.NextAttemptAt.Equal(confirmAt) || !m.CreatedAt.Equal(confirmAt) {
			t.Errorf("message to %s: next %v created %v, want %v", m.To, m.NextAttemptAt, m.CreatedAt, confirmAt)
		}
		for _, want := range []string{"Nome: Ana Souza", "E-mail: ana@example.com", "Telefone: (81) 98765-4321 (WhatsApp)", "[→] " + confirmOrigin + "/admin"} {
			if !strings.Contains(m.Text, want) {
				t.Errorf("text to %s lacks %q:\n%s", m.To, want, m.Text)
			}
		}
		if line := confirmLine(t, m.Text, "Noite Verde"); line != "- Noite Verde · Coletivo · Dono · novo" {
			t.Errorf("text to %s: line %q, want the new collective marked", m.To, line)
		}
		if line := confirmLine(t, m.Text, "Casa Aurora"); line != "- Casa Aurora · Produtora · Membro" {
			t.Errorf("text to %s: line %q, want the approved collective unmarked", m.To, line)
		}
		if !strings.Contains(m.HTML, confirmOrigin+"/admin") || !strings.Contains(m.HTML, "Noite Verde") {
			t.Errorf("html to %s lacks the admin link or the collective", m.To)
		}
		if strings.Contains(m.Text, raw) || strings.Contains(m.HTML, raw) {
			t.Errorf("message to %s carries the token", m.To)
		}
	}
}

func TestConfirmEmailPhoneFormat(t *testing.T) {
	for _, tc := range []struct {
		name, phone string
		whatsapp    bool
		want        string
		notWant     string
	}{
		{name: "eleven digits", phone: "81987654321", whatsapp: true, want: "Telefone: (81) 98765-4321 (WhatsApp)"},
		{name: "ten digits", phone: "8187654321", whatsapp: false, want: "Telefone: (81) 8765-4321", notWant: "WhatsApp"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			u := confirmPendingUser(t, s, "ana@example.com", func(u *domain.User) { u.Phone = tc.phone; u.PhoneIsWhatsApp = tc.whatsapp })
			c := confirmInsertCollective(t, s, "Noite Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusPending)
			confirmJoin(t, s, u.ID, c, domain.RoleOwner)
			confirmAdmin(t, s, "admin@example.com")
			raw := confirmToken(t, s, u.ID, testNow)

			if err := ConfirmEmail(t.Context(), s, confirmOrigin, raw, confirmAt); err != nil {
				t.Fatalf("ConfirmEmail: %v", err)
			}
			msgs := confirmOutbox(t, s, confirmAt)
			if len(msgs) != 1 {
				t.Fatalf("outbox holds %d messages, want one", len(msgs))
			}
			line := confirmLine(t, msgs[0].Text, "Telefone")
			if line != tc.want {
				t.Errorf("phone line = %q, want %q", line, tc.want)
			}
			if tc.notWant != "" && strings.Contains(msgs[0].Text, tc.notWant) {
				t.Errorf("text mentions %q:\n%s", tc.notWant, msgs[0].Text)
			}
		})
	}
}

func TestConfirmEmailWithoutAdminsQueuesNothing(t *testing.T) {
	s := newTestStore(t)
	u := confirmPendingUser(t, s, "ana@example.com")
	insertActiveUser(t, s, "off@example.com", func(u *domain.User) { u.IsAdmin = true; u.Status = domain.UserStatusDisabled })
	raw := confirmToken(t, s, u.ID, testNow)

	if err := ConfirmEmail(t.Context(), s, confirmOrigin, raw, confirmAt); err != nil {
		t.Fatalf("ConfirmEmail with no admin: %v", err)
	}
	got, err := s.Repos().Users.ByID(t.Context(), u.ID)
	if err != nil || got.Status != domain.UserStatusPendingApproval {
		t.Errorf("user = status %q, err %v; want pending_approval", got.Status, err)
	}
	if msgs := confirmOutbox(t, s, confirmFarFuture); len(msgs) != 0 {
		t.Errorf("outbox holds %d messages, want none", len(msgs))
	}
}

func TestConfirmEmailMalformedTokenNeverReachesTheStore(t *testing.T) {
	good, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	for _, raw := range []string{"", "abc", "not a token!", good + "x", good[:len(good)-1], strings.ToUpper(good) + "\n"} {
		// A nil store panics on any call: the refusal must come first.
		if err := ConfirmEmail(t.Context(), nil, confirmOrigin, raw, confirmAt); !errors.Is(err, ErrConfirmInvalid) {
			t.Errorf("token %q: err = %v, want ErrConfirmInvalid", raw, err)
		}
	}
}

func TestConfirmEmailRefusesLinksThatDoNotWork(t *testing.T) {
	t.Run("unknown token", func(t *testing.T) {
		s := newTestStore(t)
		u := confirmPendingUser(t, s, "ana@example.com")
		confirmAdmin(t, s, "admin@example.com")
		raw, _, err := auth.NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if err := ConfirmEmail(t.Context(), s, confirmOrigin, raw, confirmAt); !errors.Is(err, ErrConfirmInvalid) {
			t.Errorf("err = %v, want ErrConfirmInvalid", err)
		}
		confirmAssertUntouched(t, s, u.ID)
	})

	t.Run("expired token", func(t *testing.T) {
		s := newTestStore(t)
		u := confirmPendingUser(t, s, "ana@example.com")
		confirmAdmin(t, s, "admin@example.com")
		raw := confirmToken(t, s, u.ID, testNow)
		// A token is good until its expiry, not at it.
		if err := ConfirmEmail(t.Context(), s, confirmOrigin, raw, testNow.Add(SignupTokenTTL)); !errors.Is(err, ErrConfirmInvalid) {
			t.Errorf("err = %v, want ErrConfirmInvalid", err)
		}
		confirmAssertUntouched(t, s, u.ID)
		confirmAssertTokenUnspent(t, s, raw, confirmAt)
	})

	t.Run("token of another purpose", func(t *testing.T) {
		s := newTestStore(t)
		u := confirmPendingUser(t, s, "ana@example.com")
		confirmAdmin(t, s, "admin@example.com")
		raw, hash, err := auth.NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		tok := domain.Token{UserID: u.ID, Purpose: domain.TokenPurposeResetPassword, Hash: hash, ExpiresAt: testNow.Add(time.Hour), CreatedAt: testNow}
		if err := s.Repos().Tokens.Create(t.Context(), &tok); err != nil {
			t.Fatalf("create token: %v", err)
		}
		if err := ConfirmEmail(t.Context(), s, confirmOrigin, raw, confirmAt); !errors.Is(err, ErrConfirmInvalid) {
			t.Errorf("err = %v, want ErrConfirmInvalid", err)
		}
		confirmAssertUntouched(t, s, u.ID)
	})
}

func TestConfirmEmailRollsBackWhenTheAccountIsNotPendingEmail(t *testing.T) {
	for _, status := range []domain.UserStatus{domain.UserStatusPendingApproval, domain.UserStatusActive, domain.UserStatusDisabled} {
		t.Run(string(status), func(t *testing.T) {
			s := newTestStore(t)
			ctx := t.Context()
			u := confirmPendingUser(t, s, "ana@example.com")
			confirmAdmin(t, s, "admin@example.com")
			raw := confirmToken(t, s, u.ID, testNow)
			if err := s.Repos().Users.SetStatus(ctx, u.ID, status, testNow); err != nil {
				t.Fatalf("SetStatus: %v", err)
			}

			if err := ConfirmEmail(ctx, s, confirmOrigin, raw, confirmAt); !errors.Is(err, ErrConfirmInvalid) {
				t.Fatalf("err = %v, want ErrConfirmInvalid", err)
			}
			got, err := s.Repos().Users.ByID(ctx, u.ID)
			if err != nil || got.Status != status || got.EmailVerifiedAt != nil {
				t.Errorf("user = status %q verified %v, err %v; want %q unchanged and unverified", got.Status, got.EmailVerifiedAt, err, status)
			}
			if msgs := confirmOutbox(t, s, confirmFarFuture); len(msgs) != 0 {
				t.Errorf("outbox holds %d messages, want none", len(msgs))
			}
			// The refusal rolled the token back: it is still unspent.
			confirmAssertTokenUnspent(t, s, raw, confirmAt)
		})
	}
}

func TestConfirmEmailStoreFailureIsAServerError(t *testing.T) {
	s := newTestStore(t)
	raw, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	err = ConfirmEmail(t.Context(), confirmFailingStore{s}, confirmOrigin, raw, confirmAt)
	if err == nil || errors.Is(err, ErrConfirmInvalid) {
		t.Fatalf("err = %v, want a server error that is not ErrConfirmInvalid", err)
	}
	if strings.Contains(err.Error(), raw) {
		t.Errorf("the error carries the token: %v", err)
	}
}

func TestResendConfirmationIssuesANewLink(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	u := confirmPendingUser(t, s, "ana@example.com")
	confirmAdmin(t, s, "admin@example.com")
	old := confirmToken(t, s, u.ID, testNow)

	// The address is matched as the signup would store it.
	if err := ResendConfirmation(ctx, s, confirmOrigin, "  ANA@Example.com ", confirmAt); err != nil {
		t.Fatalf("ResendConfirmation: %v", err)
	}
	msgs := confirmOutbox(t, s, confirmAt)
	if len(msgs) != 1 {
		t.Fatalf("outbox holds %d messages, want one verification email", len(msgs))
	}
	m := msgs[0]
	if m.To != "ana@example.com" || !strings.Contains(m.Text, "Ana Souza") || !m.NextAttemptAt.Equal(confirmAt) || !m.CreatedAt.Equal(confirmAt) {
		t.Errorf("message = to %q next %v created %v text %q", m.To, m.NextAttemptAt, m.CreatedAt, m.Text)
	}
	fresh := confirmLinkToken(t, m.Text)
	if fresh == old {
		t.Fatal("the new link carries the old token")
	}
	if !strings.Contains(m.HTML, confirmOrigin+"/confirmar?token="+fresh) {
		t.Errorf("the HTML part does not carry the same link")
	}

	// The older link is retired, so it is tried first: were the new one used
	// first, the refusal would only show the account is no longer pending.
	if err := ConfirmEmail(ctx, s, confirmOrigin, old, confirmAt); !errors.Is(err, ErrConfirmInvalid) {
		t.Errorf("old link: err = %v, want ErrConfirmInvalid", err)
	}
	if err := ConfirmEmail(ctx, s, confirmOrigin, fresh, confirmAt); err != nil {
		t.Fatalf("new link: %v", err)
	}
	if got, err := s.Repos().Users.ByID(ctx, u.ID); err != nil || got.Status != domain.UserStatusPendingApproval {
		t.Errorf("user = status %q, err %v; want pending_approval", got.Status, err)
	}
}

func TestResendConfirmationTokenLifetime(t *testing.T) {
	s := newTestStore(t)
	u := confirmPendingUser(t, s, "ana@example.com")
	if err := ResendConfirmation(t.Context(), s, confirmOrigin, u.Email, confirmAt); err != nil {
		t.Fatalf("ResendConfirmation: %v", err)
	}
	msgs := confirmOutbox(t, s, confirmAt)
	if len(msgs) != 1 {
		t.Fatalf("outbox holds %d messages, want one", len(msgs))
	}
	hash, err := auth.HashToken(confirmLinkToken(t, msgs[0].Text))
	if err != nil {
		t.Fatalf("the link token is not a token: %v", err)
	}
	tok, err := s.Repos().Tokens.Consume(t.Context(), domain.TokenPurposeVerifyEmail, hash, confirmAt)
	if err != nil {
		t.Fatalf("Consume the link token: %v", err)
	}
	if tok.UserID != u.ID || !tok.CreatedAt.Equal(confirmAt) || !tok.ExpiresAt.Equal(confirmAt.Add(SignupTokenTTL)) {
		t.Errorf("token = user %q created %v expires %v, want %s, %v, %v", tok.UserID, tok.CreatedAt, tok.ExpiresAt, u.ID, confirmAt, confirmAt.Add(SignupTokenTTL))
	}
}

func TestResendConfirmationSaysNothingForOtherAccounts(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	confirmPendingUser(t, s, "pa@example.com", func(u *domain.User) { u.Status = domain.UserStatusPendingApproval })
	confirmPendingUser(t, s, "off@example.com", func(u *domain.User) { u.Status = domain.UserStatusDisabled })
	insertActiveUser(t, s, "ativo@example.com")

	for _, email := range []string{"ninguem@example.com", "not an address", "", "pa@example.com", "off@example.com", "ativo@example.com"} {
		if err := ResendConfirmation(ctx, s, confirmOrigin, email, confirmAt); err != nil {
			t.Errorf("ResendConfirmation(%q) = %v, want nil: the answer must not tell the cases apart", email, err)
		}
	}
	if msgs := confirmOutbox(t, s, confirmFarFuture); len(msgs) != 0 {
		t.Errorf("outbox holds %d messages, want none", len(msgs))
	}
	if n, err := s.Repos().Tokens.DeleteExpired(ctx, confirmFarFuture); err != nil || n != 0 {
		t.Errorf("tokens after the silent resends: %d, err %v; want none", n, err)
	}
}

func TestResendConfirmationStoreFailureIsAServerError(t *testing.T) {
	s := newTestStore(t)
	u := confirmPendingUser(t, s, "ana@example.com")
	err := ResendConfirmation(t.Context(), confirmFailingStore{s}, confirmOrigin, u.Email, confirmAt)
	if err == nil {
		t.Fatal("err = nil, want the store failure")
	}
	if strings.Contains(err.Error(), u.Email) {
		t.Errorf("the error carries the address: %v", err)
	}
}
