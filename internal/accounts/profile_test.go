package accounts

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const profileNewEmail = "nova@example.com"

// profileConfirmAt is when the person follows an email-change link issued at testNow.
var profileConfirmAt = testNow.Add(time.Hour)

// profileSession stores a session of userID whose hash is n repeated.
func profileSession(t *testing.T, s domain.Store, userID string, n byte) []byte {
	t.Helper()
	hash := bytes.Repeat([]byte{n}, domain.TokenHashLen)
	err := s.Repos().Sessions.Create(t.Context(), domain.Session{
		Hash:       hash,
		UserID:     userID,
		CreatedAt:  testNow,
		LastSeenAt: testNow,
		ExpiresAt:  signupFarFuture,
		UserAgent:  "test",
	})
	if err != nil {
		t.Fatalf("insert session %d: %v", n, err)
	}
	return hash
}

// profileSessionAlive fails unless the session is still stored.
func profileSessionAlive(t *testing.T, s domain.Store, hash []byte) {
	t.Helper()
	if _, err := s.Repos().Sessions.Get(t.Context(), hash); err != nil {
		t.Errorf("session no longer stored: %v", err)
	}
}

// profileOutbox claims the messages due at at. It leases them, so call it once per test.
func profileOutbox(t *testing.T, s domain.Store, at time.Time) []domain.OutboxMessage {
	t.Helper()
	msgs, err := s.Repos().Outbox.Claim(t.Context(), at, time.Minute, 20)
	if err != nil {
		t.Fatalf("claim outbox: %v", err)
	}
	return msgs
}

// profileFieldErrors asserts that err is a domain.FieldErrors and returns it.
func profileFieldErrors(t *testing.T, err error) domain.FieldErrors {
	t.Helper()
	var fe domain.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want domain.FieldErrors", err)
	}
	return fe
}

// profileChangeToken stores a change_email token issued at issued and returns its
// raw form (what the link carries).
func profileChangeToken(t *testing.T, s domain.Store, userID, newEmail string, issued time.Time) string {
	t.Helper()
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	tok := domain.Token{
		UserID:    userID,
		Purpose:   domain.TokenPurposeChangeEmail,
		Hash:      hash,
		NewEmail:  newEmail,
		ExpiresAt: issued.Add(ChangeEmailTokenTTL),
		CreatedAt: issued,
	}
	if err := s.Repos().Tokens.Create(t.Context(), &tok); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return raw
}

// profileLinkToken returns the token in the confirmation link of an email text.
func profileLinkToken(t *testing.T, text string) string {
	t.Helper()
	prefix := signupOrigin + "/confirmar-email?token="
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

// profileTokensGone fails unless no token is left in the test database.
func profileTokensGone(t *testing.T, s domain.Store) {
	t.Helper()
	if n, err := s.Repos().Tokens.DeleteExpired(t.Context(), signupFarFuture); err != nil || n != 0 {
		t.Errorf("tokens = %d, err %v; want none", n, err)
	}
}

// profileInsertCollective stores a pending collective created by creator.
func profileInsertCollective(t *testing.T, s domain.Store, name string, creator domain.User, created time.Time) domain.Collective {
	t.Helper()
	c := domain.Collective{
		Name:      name,
		NameKey:   domain.NameKey(name),
		Kind:      domain.CollectiveKindColetivo,
		Status:    domain.CollectiveStatusPending,
		CreatedBy: creator.ID,
		CreatedAt: created,
		UpdatedAt: created,
	}
	if err := s.Repos().Collectives.Create(t.Context(), &c); err != nil {
		t.Fatalf("insert collective %q: %v", name, err)
	}
	return c
}

// profileMembership makes user a pending owner of collective.
func profileMembership(t *testing.T, s domain.Store, user domain.User, c domain.Collective, at time.Time) {
	t.Helper()
	m := domain.Membership{
		UserID:       user.ID,
		CollectiveID: c.ID,
		Role:         domain.RoleOwner,
		Status:       domain.MembershipStatusPending,
		RequestedAt:  at,
	}
	if err := s.Repos().Memberships.Create(t.Context(), m); err != nil {
		t.Fatalf("insert membership in %q: %v", c.Name, err)
	}
}

func TestUpdateProfileValidatesAndSaves(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	u := insertLoginUser(t, s)
	session := profileSession(t, s, u.ID, 1)

	// Bad values are refused in the service, before any write.
	for _, tt := range []struct{ name, phone, field string }{
		{"", "(81) 98765-4321", "name"},
		{strings.Repeat("ã", domain.UserNameMax+1), "(81) 98765-4321", "name"},
		{"Ana Souza", "81 9876-543", "phone"},
		{"Ana Souza", "819876543210", "phone"},
	} {
		fe := profileFieldErrors(t, UpdateProfile(ctx, s, u, tt.name, tt.phone, false, testNow))
		if fe[tt.field] == "" {
			t.Errorf("name %q phone %q: field errors %v, want %q", tt.name, tt.phone, fe, tt.field)
		}
		got, err := s.Repos().Users.ByID(ctx, u.ID)
		if err != nil {
			t.Fatalf("reload user: %v", err)
		}
		if got.Name != u.Name || got.Phone != u.Phone || got.PhoneIsWhatsApp != u.PhoneIsWhatsApp {
			t.Errorf("user after a refused update = name %q phone %q whatsapp %v; want it unchanged", got.Name, got.Phone, got.PhoneIsWhatsApp)
		}
	}

	err := UpdateProfile(ctx, s, u, "  Ana Maria Souza  ", "(81) 99999-1234", true, testNow)
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	got, err := s.Repos().Users.ByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.Name != "Ana Maria Souza" || got.Phone != "81999991234" || !got.PhoneIsWhatsApp {
		t.Errorf("user = name %q phone %q whatsapp %v; want the trimmed name, digits and true", got.Name, got.Phone, got.PhoneIsWhatsApp)
	}
	if got.Email != u.Email || got.PasswordHash != u.PasswordHash || got.Status != u.Status {
		t.Errorf("a field other than the profile changed: %+v", got)
	}
	if !got.UpdatedAt.Truncate(time.Millisecond).Equal(testNow) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, testNow)
	}
	profileSessionAlive(t, s, session)
}

// A name is copied into emails, so a line break in it must be refused the way
// signup refuses one, before any write.
func TestUpdateProfileRefusesControlCharactersInName(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	u := insertLoginUser(t, s)
	session := profileSession(t, s, u.ID, 1)

	fe := profileFieldErrors(t, UpdateProfile(ctx, s, u, "Ana\nSouza", "(81) 98765-4321", false, testNow))
	if len(fe) != 1 || fe["name"] != domain.MsgControl {
		t.Errorf("field errors = %v, want only name with %q", fe, domain.MsgControl)
	}
	got, err := s.Repos().Users.ByID(ctx, u.ID)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.Name != u.Name || got.Phone != u.Phone {
		t.Errorf("user after the refused update = name %q phone %q; want it unchanged", got.Name, got.Phone)
	}
	profileSessionAlive(t, s, session)
}

func TestRequestEmailChangeRefusesOwnAddress(t *testing.T) {
	s := newTestStore(t)
	u := insertActiveUser(t, s, "ana@example.com")
	fe := profileFieldErrors(t, RequestEmailChange(t.Context(), s, signupOrigin, u, "  ANA@Example.com ", testNow))
	if len(fe) != 1 || fe["email"] != "este já é o seu e-mail" {
		t.Errorf("field errors = %v, want only email with the own-address message", fe)
	}
	profileTokensGone(t, s)
	if msgs := profileOutbox(t, s, testNow); len(msgs) != 0 {
		t.Errorf("outbox holds %d messages, want none", len(msgs))
	}
}

func TestRequestEmailChangeOccupiedAddressNotifiesOnlyItsOwner(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	actor := insertActiveUser(t, s, "ana@example.com")
	owner := insertActiveUser(t, s, "bia@example.com", func(u *domain.User) { u.Name = "Bia Dona" })

	// The requester sees success, whether or not the address is taken.
	if err := RequestEmailChange(ctx, s, signupOrigin, actor, " BIA@Example.com ", testNow); err != nil {
		t.Fatalf("RequestEmailChange: %v", err)
	}
	msgs := profileOutbox(t, s, testNow)
	if len(msgs) != 1 {
		t.Fatalf("outbox holds %d messages, want only the existing-account notice", len(msgs))
	}
	m := msgs[0]
	if m.To != "bia@example.com" {
		t.Errorf("message To = %q, want the occupied address", m.To)
	}
	for _, want := range []string{owner.Name, signupOrigin + "/entrar", signupOrigin + "/senha/esqueci"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("existing-account text lacks %q:\n%s", want, m.Text)
		}
	}
	if strings.Contains(m.Text, "/confirmar-email") || strings.Contains(m.HTML, "/confirmar-email") {
		t.Errorf("occupied-address email carries a confirmation link")
	}
	if got, err := s.Repos().Users.ByID(ctx, actor.ID); err != nil || got.Email != actor.Email {
		t.Errorf("requester = email %q, err %v; want it unchanged", got.Email, err)
	}
	profileTokensGone(t, s)
}

func TestRequestEmailChangeMailsBothAddressesAndCreatesToken(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	actor := insertLoginUser(t, s)

	if err := RequestEmailChange(ctx, s, signupOrigin, actor, "  NOVA@Example.com ", testNow); err != nil {
		t.Fatalf("RequestEmailChange: %v", err)
	}
	msgs := profileOutbox(t, s, testNow)
	if len(msgs) != 2 {
		t.Fatalf("outbox holds %d messages, want one to each address: %+v", len(msgs), msgs)
	}
	byTo := make(map[string]domain.OutboxMessage, len(msgs))
	for _, m := range msgs {
		byTo[m.To] = m
	}
	newMsg, ok := byTo[profileNewEmail]
	if !ok {
		t.Fatalf("no message to the new address: %+v", byTo)
	}
	oldMsg, ok := byTo[actor.Email]
	if !ok {
		t.Fatalf("no message to the old address: %+v", byTo)
	}
	if !strings.Contains(newMsg.Text, actor.Name) || !strings.Contains(newMsg.Subject, "novo e-mail") {
		t.Errorf("confirmation to the new address = subject %q text %q", newMsg.Subject, newMsg.Text)
	}
	raw := profileLinkToken(t, newMsg.Text)
	if !strings.Contains(newMsg.HTML, signupOrigin+"/confirmar-email?token="+raw) {
		t.Errorf("the HTML part does not carry the same confirmation link")
	}
	if !strings.Contains(oldMsg.Text, actor.Name) || !strings.Contains(oldMsg.Text, profileNewEmail) ||
		!strings.Contains(oldMsg.Subject, "pendente") {
		t.Errorf("notice to the old address = subject %q text %q", oldMsg.Subject, oldMsg.Text)
	}
	if strings.Contains(oldMsg.Text, "/confirmar-email?token=") {
		t.Errorf("the notice to the old address carries the link")
	}
	for _, m := range msgs {
		if !m.NextAttemptAt.Equal(testNow) || !m.CreatedAt.Equal(testNow) {
			t.Errorf("message to %s: next %v created %v, want %v", m.To, m.NextAttemptAt, m.CreatedAt, testNow)
		}
	}

	hash, err := auth.HashToken(raw)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}
	tok, err := s.Repos().Tokens.Consume(ctx, domain.TokenPurposeChangeEmail, hash, profileConfirmAt)
	if err != nil {
		t.Fatalf("Consume the link token: %v", err)
	}
	if tok.UserID != actor.ID || tok.NewEmail != profileNewEmail {
		t.Errorf("token = user %q new email %q, want %s and %s", tok.UserID, tok.NewEmail, actor.ID, profileNewEmail)
	}
	if !tok.CreatedAt.Equal(testNow) || !tok.ExpiresAt.Equal(testNow.Add(ChangeEmailTokenTTL)) {
		t.Errorf("token times = created %v expires %v, want %v and %v", tok.CreatedAt, tok.ExpiresAt, testNow, testNow.Add(ChangeEmailTokenTTL))
	}
	if got, err := s.Repos().Users.ByEmail(ctx, profileNewEmail); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the email changed before confirmation: user %+v err %v", got, err)
	}
}

func TestRequestEmailChangeRetiresTheOlderToken(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	actor := insertActiveUser(t, s, "ana@example.com")
	later := testNow.Add(time.Hour)

	if err := RequestEmailChange(ctx, s, signupOrigin, actor, profileNewEmail, testNow); err != nil {
		t.Fatalf("first RequestEmailChange: %v", err)
	}
	if err := RequestEmailChange(ctx, s, signupOrigin, actor, profileNewEmail, later); err != nil {
		t.Fatalf("second RequestEmailChange: %v", err)
	}
	var first, second string
	for _, m := range profileOutbox(t, s, later) {
		if m.To != profileNewEmail || !strings.Contains(m.Text, "/confirmar-email?token=") {
			continue
		}
		switch {
		case m.CreatedAt.Equal(testNow):
			first = profileLinkToken(t, m.Text)
		case m.CreatedAt.Equal(later):
			second = profileLinkToken(t, m.Text)
		}
	}
	if first == "" || second == "" || first == second {
		t.Fatalf("confirmation tokens = %q and %q, want two different ones", first, second)
	}
	// The older link is tried first: the new one would have retired it.
	if _, err := ConfirmEmailChange(ctx, s, first, profileConfirmAt); !errors.Is(err, ErrEmailChangeInvalid) {
		t.Errorf("old link: err = %v, want ErrEmailChangeInvalid", err)
	}
	if _, err := ConfirmEmailChange(ctx, s, second, profileConfirmAt); err != nil {
		t.Fatalf("new link: %v", err)
	}
	if got, err := s.Repos().Users.ByID(ctx, actor.ID); err != nil || got.Email != profileNewEmail {
		t.Errorf("user = email %q, err %v; want %s", got.Email, err, profileNewEmail)
	}
}

func TestConfirmEmailChangeSwapsTheEmailAndKeepsSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	u := insertLoginUser(t, s)
	session := profileSession(t, s, u.ID, 1)
	raw := profileChangeToken(t, s, u.ID, profileNewEmail, testNow)

	got, err := ConfirmEmailChange(ctx, s, raw, profileConfirmAt)
	if err != nil {
		t.Fatalf("ConfirmEmailChange: %v", err)
	}
	if got.ID != u.ID || got.Email != profileNewEmail {
		t.Errorf("returned user = id %q email %q, want %s and %s", got.ID, got.Email, u.ID, profileNewEmail)
	}
	if !got.UpdatedAt.Truncate(time.Millisecond).Equal(profileConfirmAt) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, profileConfirmAt)
	}
	if logged, err := Login(ctx, s, profileNewEmail, loginPassword, profileConfirmAt); err != nil || logged.ID != u.ID {
		t.Errorf("login with the new email = %+v, %v; want the same account", logged, err)
	}
	if _, err := s.Repos().Users.ByEmail(ctx, loginEmail); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("the old email still finds an account: %v", err)
	}
	profileSessionAlive(t, s, session)
	if msgs := profileOutbox(t, s, signupFarFuture); len(msgs) != 0 {
		t.Errorf("outbox holds %d messages, want none", len(msgs))
	}
}

func TestConfirmEmailChangeRefusesBadLinks(t *testing.T) {
	t.Run("malformed token never reaches the store", func(t *testing.T) {
		good, _, err := auth.NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		for _, raw := range []string{"", "abc", "not a token!", good + "x", strings.ToUpper(good) + "\n"} {
			if _, err := ConfirmEmailChange(t.Context(), nil, raw, profileConfirmAt); !errors.Is(err, ErrEmailChangeInvalid) {
				t.Errorf("token %q: err = %v, want ErrEmailChangeInvalid", raw, err)
			}
		}
	})

	t.Run("unknown token", func(t *testing.T) {
		s := newTestStore(t)
		u := insertActiveUser(t, s, "ana@example.com")
		raw, _, err := auth.NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if _, err := ConfirmEmailChange(t.Context(), s, raw, profileConfirmAt); !errors.Is(err, ErrEmailChangeInvalid) {
			t.Errorf("err = %v, want ErrEmailChangeInvalid", err)
		}
		if got, err := s.Repos().Users.ByID(t.Context(), u.ID); err != nil || got.Email != u.Email {
			t.Errorf("user = email %q, err %v; want it unchanged", got.Email, err)
		}
	})

	t.Run("expired token", func(t *testing.T) {
		s := newTestStore(t)
		ctx := t.Context()
		u := insertActiveUser(t, s, "ana@example.com")
		raw := profileChangeToken(t, s, u.ID, profileNewEmail, testNow)
		if _, err := ConfirmEmailChange(ctx, s, raw, testNow.Add(ChangeEmailTokenTTL)); !errors.Is(err, ErrEmailChangeInvalid) {
			t.Errorf("at the expiry instant: err = %v, want ErrEmailChangeInvalid", err)
		}
		hash, err := auth.HashToken(raw)
		if err != nil {
			t.Fatalf("HashToken: %v", err)
		}
		if _, err := s.Repos().Tokens.Consume(ctx, domain.TokenPurposeChangeEmail, hash, profileConfirmAt); err != nil {
			t.Errorf("the refused link spent the token: %v", err)
		}
		if got, err := s.Repos().Users.ByID(ctx, u.ID); err != nil || got.Email != u.Email {
			t.Errorf("user = email %q, err %v; want it unchanged", got.Email, err)
		}
	})

	t.Run("used token", func(t *testing.T) {
		s := newTestStore(t)
		ctx := t.Context()
		u := insertActiveUser(t, s, "ana@example.com")
		raw := profileChangeToken(t, s, u.ID, profileNewEmail, testNow)
		if _, err := ConfirmEmailChange(ctx, s, raw, profileConfirmAt); err != nil {
			t.Fatalf("first use: %v", err)
		}
		if _, err := ConfirmEmailChange(ctx, s, raw, profileConfirmAt); !errors.Is(err, ErrEmailChangeInvalid) {
			t.Errorf("second use: err = %v, want ErrEmailChangeInvalid", err)
		}
		if got, err := s.Repos().Users.ByID(ctx, u.ID); err != nil || got.Email != profileNewEmail {
			t.Errorf("user = email %q, err %v; want %s", got.Email, err, profileNewEmail)
		}
	})
}

func TestPurgeUnconfirmedSignups(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	// Ages are measured from the clock the caller passes, eight days after testNow.
	purgeNow := testNow.Add(8 * 24 * time.Hour)

	old := insertActiveUser(t, s, "old@example.com", func(u *domain.User) {
		u.Name = "Velho"
		u.Status = domain.UserStatusPendingEmail
		u.EmailVerifiedAt = nil
		u.CreatedAt = testNow
		u.UpdatedAt = testNow
	})
	young := insertActiveUser(t, s, "young@example.com", func(u *domain.User) {
		u.Status = domain.UserStatusPendingEmail
		u.EmailVerifiedAt = nil
		u.CreatedAt = purgeNow.Add(-6 * 24 * time.Hour) // six days before purgeNow
		u.UpdatedAt = u.CreatedAt
	})
	waiting := insertActiveUser(t, s, "waiting@example.com", func(u *domain.User) {
		u.Status = domain.UserStatusPendingApproval
		u.EmailVerifiedAt = nil
		u.CreatedAt = testNow
		u.UpdatedAt = testNow
	})
	oldCollective := profileInsertCollective(t, s, "Noite Velha", old, testNow)
	profileMembership(t, s, old, oldCollective, testNow)
	youngCollective := profileInsertCollective(t, s, "Casa Nova", young, young.CreatedAt)
	profileMembership(t, s, young, youngCollective, young.CreatedAt)

	purged, err := PurgeUnconfirmedSignups(ctx, s, purgeNow)
	if err != nil {
		t.Fatalf("PurgeUnconfirmedSignups: %v", err)
	}
	if purged != 1 {
		t.Errorf("purged = %d, want 1", purged)
	}
	if _, err := s.Repos().Users.ByID(ctx, old.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("old account still exists: %v", err)
	}
	if _, err := s.Repos().Collectives.ByID(ctx, oldCollective.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("old account's pending collective still exists: %v", err)
	}
	if got, err := s.Repos().Users.ByID(ctx, young.ID); err != nil || got.Email != young.Email {
		t.Errorf("six-day-old account = %+v, err %v; want it to survive", got, err)
	}
	if got, err := s.Repos().Collectives.ByID(ctx, youngCollective.ID); err != nil || got.CreatedBy != young.ID {
		t.Errorf("six-day-old collective = %+v, err %v; want it to survive", got, err)
	}
	if got, err := s.Repos().Users.ByID(ctx, waiting.ID); err != nil || got.Status != domain.UserStatusPendingApproval {
		t.Errorf("pending-approval account = %+v, err %v; want it to survive", got, err)
	}
	entries, err := s.Repos().AuditEntries.List(ctx, domain.ListLimitMax)
	if err != nil || len(entries) != 0 {
		t.Errorf("audit entries = %+v, err %v; want none", entries, err)
	}
	if msgs := profileOutbox(t, s, signupFarFuture); len(msgs) != 0 {
		t.Errorf("outbox holds %d messages, want none", len(msgs))
	}

	// Nothing is left to purge: the same call is a no-op.
	purged, err = PurgeUnconfirmedSignups(ctx, s, purgeNow)
	if err != nil || purged != 0 {
		t.Errorf("second purge = %d, %v; want 0, nil", purged, err)
	}
}
