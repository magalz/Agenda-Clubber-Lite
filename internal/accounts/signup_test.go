package accounts

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const signupOrigin = "https://agenda.example"

// signupFarFuture is later than every token expiry, so Tokens.DeleteExpired(...,
// signupFarFuture) removes and counts every token in the test database.
var signupFarFuture = testNow.Add(10 * 365 * 24 * time.Hour)

// signupInput is a valid signup that creates a new collective as its owner.
func signupInput() SignupInput {
	return SignupInput{
		Name:            "Ana Souza",
		Email:           "ana@example.com",
		Phone:           "(81) 98765-4321",
		PhoneIsWhatsApp: true,
		Password:        "Senha#Forte1",
		PasswordConfirm: "Senha#Forte1",
		CollectiveKind:  domain.CollectiveKindColetivo,
		CollectiveName:  "Noite Verde",
		Role:            domain.RoleOwner,
	}
}

// signupInsertCollective stores a collective in the given state and returns it.
func signupInsertCollective(t *testing.T, s domain.Store, name string, kind domain.CollectiveKind, status domain.CollectiveStatus) domain.Collective {
	t.Helper()
	c := domain.Collective{
		Name:      name,
		NameKey:   domain.NameKey(name),
		Kind:      kind,
		Status:    status,
		CreatedAt: testNow,
		UpdatedAt: testNow,
	}
	if err := s.Repos().Collectives.Create(t.Context(), &c); err != nil {
		t.Fatalf("insert collective %q: %v", name, err)
	}
	return c
}

// signupOutbox claims the messages due at testNow. It leases them, so call it
// once per test.
func signupOutbox(t *testing.T, s domain.Store) []domain.OutboxMessage {
	t.Helper()
	msgs, err := s.Repos().Outbox.Claim(t.Context(), testNow, time.Minute, 10)
	if err != nil {
		t.Fatalf("claim outbox: %v", err)
	}
	return msgs
}

// signupFieldErrors asserts that err is a domain.FieldErrors and returns it.
func signupFieldErrors(t *testing.T, err error) domain.FieldErrors {
	t.Helper()
	var fe domain.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want domain.FieldErrors", err)
	}
	return fe
}

// signupAssertKeys asserts the exact set of failing fields.
func signupAssertKeys(t *testing.T, fe domain.FieldErrors, want ...string) {
	t.Helper()
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(fe)); !slices.Equal(got, want) {
		t.Errorf("field errors %v, want keys %v", fe, want)
	}
}

// signupAssertNothingWritten asserts that a failed signup left no user for
// email, no collective for typedName (when not empty), no token and no message.
// It removes the tokens and leases the messages it counts, so it comes last.
func signupAssertNothingWritten(t *testing.T, s domain.Store, email, typedName string) {
	t.Helper()
	ctx := t.Context()
	r := s.Repos()
	if u, err := r.Users.ByEmail(ctx, email); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("user %s after the failed signup: id %q, err %v; want ErrNotFound", email, u.ID, err)
	}
	if typedName != "" {
		if c, err := r.Collectives.ByNameKey(ctx, domain.NameKey(typedName)); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("collective %q after the failed signup: id %q, err %v; want ErrNotFound", typedName, c.ID, err)
		}
	}
	if n, err := r.Tokens.DeleteExpired(ctx, signupFarFuture); err != nil || n != 0 {
		t.Errorf("tokens after the failed signup: %d, err %v; want none", n, err)
	}
	if msgs := signupOutbox(t, s); len(msgs) != 0 {
		t.Errorf("outbox after the failed signup holds %d messages, want none", len(msgs))
	}
}

// signupLinkToken returns the token in the confirmation link of an email text.
func signupLinkToken(t *testing.T, text string) string {
	t.Helper()
	prefix := signupOrigin + "/confirmar?token="
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

func TestSignupNewCollectiveAsOwner(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	in := signupInput()
	in.Name = "  Ana Souza  "
	in.Email = " Ana@Example.com "
	in.CollectiveName = "  Noite Verde "

	if err := Signup(ctx, s, signupOrigin, testNow, in); err != nil {
		t.Fatalf("Signup: %v", err)
	}
	r := s.Repos()

	u, err := r.Users.ByEmail(ctx, "ana@example.com")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if u.Name != "Ana Souza" || u.Email != "ana@example.com" || u.Phone != "81987654321" || !u.PhoneIsWhatsApp {
		t.Errorf("user = name %q email %q phone %q whatsapp %v", u.Name, u.Email, u.Phone, u.PhoneIsWhatsApp)
	}
	if u.Status != domain.UserStatusPendingEmail || u.IsAdmin || u.EmailVerifiedAt != nil || u.ApprovedAt != nil {
		t.Errorf("user status %q admin %v verified %v approved %v, want pending_email and nothing granted", u.Status, u.IsAdmin, u.EmailVerifiedAt, u.ApprovedAt)
	}
	if ok, err := auth.VerifyPassword(u.PasswordHash, in.Password); err != nil || !ok {
		t.Errorf("stored hash does not verify the password: ok %v, err %v", ok, err)
	}
	if !u.PasswordChangedAt.Equal(testNow) || !u.CreatedAt.Equal(testNow) || !u.UpdatedAt.Equal(testNow) {
		t.Errorf("user times %v %v %v, want %v", u.PasswordChangedAt, u.CreatedAt, u.UpdatedAt, testNow)
	}

	c, err := r.Collectives.ByNameKey(ctx, domain.NameKey("Noite Verde"))
	if err != nil {
		t.Fatalf("collective: %v", err)
	}
	if c.Name != "Noite Verde" || c.Kind != domain.CollectiveKindColetivo || c.Status != domain.CollectiveStatusPending ||
		c.CreatedBy != u.ID || c.ApprovedBy != "" || c.ApprovedAt != nil {
		t.Errorf("collective = %+v, want a pending coletivo created by %s", c, u.ID)
	}
	if !c.CreatedAt.Equal(testNow) || !c.UpdatedAt.Equal(testNow) {
		t.Errorf("collective times %v %v, want %v", c.CreatedAt, c.UpdatedAt, testNow)
	}

	ms, err := r.Memberships.ListByUser(ctx, u.ID)
	if err != nil {
		t.Fatalf("memberships: %v", err)
	}
	if len(ms) != 1 {
		t.Fatalf("memberships = %+v, want one", ms)
	}
	m := ms[0]
	if m.CollectiveID != c.ID || m.Role != domain.RoleOwner || m.Status != domain.MembershipStatusPending {
		t.Errorf("membership = %+v, want owner/pending in %s", m.Membership, c.ID)
	}
	if m.OwnerApprovedBy != u.ID || m.OwnerApprovedAt == nil || !m.OwnerApprovedAt.Equal(testNow) {
		t.Errorf("owner side = by %q at %v, want %s at %v", m.OwnerApprovedBy, m.OwnerApprovedAt, u.ID, testNow)
	}
	if m.AdminApprovedBy != "" || m.AdminApprovedAt != nil || m.DecidedAt != nil || !m.RequestedAt.Equal(testNow) {
		t.Errorf("admin side = by %q at %v decided %v requested %v, want pending", m.AdminApprovedBy, m.AdminApprovedAt, m.DecidedAt, m.RequestedAt)
	}

	msgs := signupOutbox(t, s)
	if len(msgs) != 1 {
		t.Fatalf("outbox holds %d messages, want one", len(msgs))
	}
	msg := msgs[0]
	if msg.To != "ana@example.com" || !msg.NextAttemptAt.Equal(testNow) || !msg.CreatedAt.Equal(testNow) {
		t.Errorf("message to %q next %v created %v", msg.To, msg.NextAttemptAt, msg.CreatedAt)
	}
	raw := signupLinkToken(t, msg.Text)
	if !strings.Contains(msg.HTML, signupOrigin+"/confirmar?token="+raw) {
		t.Errorf("the HTML part does not carry the same link")
	}
	if strings.Contains(msg.Text, in.Password) || strings.Contains(msg.HTML, in.Password) || strings.Contains(msg.Text, u.PasswordHash) {
		t.Errorf("the email carries the password or its hash")
	}
	hash, err := auth.HashToken(raw)
	if err != nil {
		t.Fatalf("the link token is not a token: %v", err)
	}
	tok, err := r.Tokens.Consume(ctx, domain.TokenPurposeVerifyEmail, hash, testNow)
	if err != nil {
		t.Fatalf("Consume the link token: %v", err)
	}
	if tok.UserID != u.ID || !tok.ExpiresAt.Equal(testNow.Add(SignupTokenTTL)) || !tok.CreatedAt.Equal(testNow) {
		t.Errorf("token = user %q expires %v created %v, want %s, %v, %v", tok.UserID, tok.ExpiresAt, tok.CreatedAt, u.ID, testNow.Add(SignupTokenTTL), testNow)
	}
}

func TestSignupJoinsApprovedCollectiveByID(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	c := signupInsertCollective(t, s, "Casa Aurora", domain.CollectiveKindProdutora, domain.CollectiveStatusApproved)
	in := signupInput()
	in.CollectiveKind = domain.CollectiveKindProdutora
	in.CollectiveID = c.ID
	in.CollectiveName = "Outro Nome" // ignored: the picked collective wins
	in.Role = domain.RoleMember

	if err := Signup(ctx, s, signupOrigin, testNow, in); err != nil {
		t.Fatalf("Signup: %v", err)
	}
	r := s.Repos()
	u, err := r.Users.ByEmail(ctx, in.Email)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	ms, err := r.Memberships.ListByUser(ctx, u.ID)
	if err != nil || len(ms) != 1 {
		t.Fatalf("memberships = %+v, err %v; want one", ms, err)
	}
	m := ms[0]
	if m.CollectiveID != c.ID || m.Role != domain.RoleMember || m.Status != domain.MembershipStatusPending {
		t.Errorf("membership = %+v, want member/pending in %s", m.Membership, c.ID)
	}
	if m.OwnerApprovedBy != "" || m.OwnerApprovedAt != nil || m.AdminApprovedBy != "" || m.AdminApprovedAt != nil || !m.RequestedAt.Equal(testNow) {
		t.Errorf("membership approvals = %+v, want none and RequestedAt %v", m.Membership, testNow)
	}
	if got, err := r.Collectives.ByID(ctx, c.ID); err != nil || got.Status != domain.CollectiveStatusApproved {
		t.Errorf("collective after the signup = %+v, err %v; want it unchanged", got, err)
	}
	if _, err := r.Collectives.ByNameKey(ctx, domain.NameKey("Outro Nome")); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("typed name created a collective: %v", err)
	}
	if msgs := signupOutbox(t, s); len(msgs) != 1 || !strings.Contains(msgs[0].Text, "/confirmar?token=") {
		t.Errorf("outbox = %+v, want one verification email", msgs)
	}
}

func TestSignupTypedNameJoinsApprovedCollective(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	c := signupInsertCollective(t, s, "Coletivo Ação", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
	in := signupInput()
	in.CollectiveName = "  coletivo   ACAO "
	in.Role = domain.RoleOwner

	if err := Signup(ctx, s, signupOrigin, testNow, in); err != nil {
		t.Fatalf("Signup: %v", err)
	}
	r := s.Repos()
	u, err := r.Users.ByEmail(ctx, in.Email)
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	ms, err := r.Memberships.ListByUser(ctx, u.ID)
	if err != nil || len(ms) != 1 {
		t.Fatalf("memberships = %+v, err %v; want one", ms, err)
	}
	m := ms[0]
	if m.CollectiveID != c.ID || m.Role != domain.RoleOwner || m.Status != domain.MembershipStatusPending {
		t.Errorf("membership = %+v, want owner/pending in the existing %s", m.Membership, c.ID)
	}
	// Joining an approved collective is not creating one: the owner side still
	// waits for an owner, and the admin side for the admin.
	if m.OwnerApprovedBy != "" || m.OwnerApprovedAt != nil {
		t.Errorf("owner side approved on a join: %+v", m.Membership)
	}
	if got, err := r.Collectives.ByID(ctx, c.ID); err != nil || got.Name != "Coletivo Ação" {
		t.Errorf("collective after the signup = %+v, err %v; want it unchanged", got, err)
	}
}

func TestSignupCollectiveErrorsWriteNothing(t *testing.T) {
	s := newTestStore(t)
	pending := signupInsertCollective(t, s, "Fila Nova", domain.CollectiveKindColetivo, domain.CollectiveStatusPending)
	rejected := signupInsertCollective(t, s, "Rejeitado Club", domain.CollectiveKindColetivo, domain.CollectiveStatusRejected)
	produtora := signupInsertCollective(t, s, "Casa Aurora", domain.CollectiveKindProdutora, domain.CollectiveStatusApproved)
	signupInsertCollective(t, s, "Noite Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)

	tests := []struct {
		name    string
		mutate  func(*SignupInput)
		key     string
		msgPart string
	}{
		{"typed name of a pending collective", func(in *SignupInput) { in.CollectiveName = "fila nova" }, "collective", "aprovação"},
		{"typed name of a rejected collective", func(in *SignupInput) { in.CollectiveName = "Rejeitado Club" }, "collective", "em uso"},
		{"typed name of an approved collective of the other kind", func(in *SignupInput) { in.CollectiveName = "casa aurora" }, "collective", "em uso"},
		{"picked collective that is pending", func(in *SignupInput) { in.CollectiveID = pending.ID }, "collective", "escolha um coletivo da lista"},
		{"picked collective that is rejected", func(in *SignupInput) { in.CollectiveID = rejected.ID }, "collective", "escolha um coletivo da lista"},
		{"picked collective of the other kind", func(in *SignupInput) { in.CollectiveID = produtora.ID }, "collective", "escolha um coletivo da lista"},
		{"picked collective that does not exist", func(in *SignupInput) { in.CollectiveID = "no-such-collective" }, "collective", "escolha um coletivo da lista"},
		{"member of a new collective", func(in *SignupInput) { in.CollectiveName = "Totalmente Nova"; in.Role = domain.RoleMember }, "role", "quem cria um coletivo novo entra como dono"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := signupInput()
			tt.mutate(&in)
			fe := signupFieldErrors(t, Signup(t.Context(), s, signupOrigin, testNow, in))
			signupAssertKeys(t, fe, tt.key)
			if !strings.Contains(fe[tt.key], tt.msgPart) {
				t.Errorf("message %q does not mention %q", fe[tt.key], tt.msgPart)
			}
			if u, err := s.Repos().Users.ByEmail(t.Context(), in.Email); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("user after the failed signup: id %q, err %v; want ErrNotFound", u.ID, err)
			}
		})
	}
	// The cases share one store and none may write: no new collective, token or message.
	signupAssertNothingWritten(t, s, "ana@example.com", "Totalmente Nova")
}

// The collective rules run whether or not the email has an account, so a
// field error never tells a stranger that an address is registered.
func TestSignupCollectiveErrorsDoNotDependOnTheEmail(t *testing.T) {
	s := newTestStore(t)
	insertActiveUser(t, s, "ana@example.com")
	signupInsertCollective(t, s, "Fila Nova", domain.CollectiveKindColetivo, domain.CollectiveStatusPending)

	tests := []struct {
		name   string
		mutate func(*SignupInput)
		key    string
	}{
		{"name of a pending collective", func(in *SignupInput) { in.CollectiveName = "Fila Nova" }, "collective"},
		{"member of a new collective", func(in *SignupInput) { in.CollectiveName = "Totalmente Nova"; in.Role = domain.RoleMember }, "role"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := map[string]domain.FieldErrors{}
			for _, email := range []string{"ana@example.com", "nova@example.com"} {
				in := signupInput()
				in.Email = email
				tt.mutate(&in)
				fe := signupFieldErrors(t, Signup(t.Context(), s, signupOrigin, testNow, in))
				signupAssertKeys(t, fe, tt.key)
				got[email] = fe
			}
			if !maps.Equal(got["ana@example.com"], got["nova@example.com"]) {
				t.Errorf("registered email got %v, new email got %v; want the same", got["ana@example.com"], got["nova@example.com"])
			}
		})
	}
	signupAssertNothingWritten(t, s, "nova@example.com", "Totalmente Nova")
}

func TestSignupExistingEmail(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	existing := insertActiveUser(t, s, "ana@example.com")
	in := signupInput()
	in.Email = "  ANA@Example.com "

	// The same nil as a signup that worked: the caller shows one success page.
	if err := Signup(ctx, s, signupOrigin, testNow, in); err != nil {
		t.Fatalf("Signup with a registered email: %v, want nil", err)
	}
	r := s.Repos()

	msgs := signupOutbox(t, s)
	if len(msgs) != 1 {
		t.Fatalf("outbox holds %d messages, want only the existing-account one", len(msgs))
	}
	m := msgs[0]
	if m.To != "ana@example.com" {
		t.Errorf("message To = %q, want the registered address", m.To)
	}
	for _, want := range []string{existing.Name, signupOrigin + "/entrar", signupOrigin + "/senha/esqueci"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("existing-account text lacks %q:\n%s", want, m.Text)
		}
	}
	if strings.Contains(m.Text, "/confirmar") || strings.Contains(m.HTML, "/confirmar") {
		t.Errorf("existing-account email carries a confirmation link")
	}

	got, err := r.Users.ByEmail(ctx, "ana@example.com")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	if got.ID != existing.ID || got.Name != existing.Name || got.Status != domain.UserStatusActive || got.PasswordHash != existing.PasswordHash {
		t.Errorf("the registered account changed: %+v", got)
	}
	if ms, err := r.Memberships.ListByUser(ctx, existing.ID); err != nil || len(ms) != 0 {
		t.Errorf("memberships = %+v, err %v; want none", ms, err)
	}
	if c, err := r.Collectives.ByNameKey(ctx, domain.NameKey(in.CollectiveName)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("collective %q created: id %q, err %v", in.CollectiveName, c.ID, err)
	}
	if n, err := r.Tokens.DeleteExpired(ctx, signupFarFuture); err != nil || n != 0 {
		t.Errorf("tokens = %d, err %v; want none", n, err)
	}
}

func TestSignupFieldValidation(t *testing.T) {
	s := newTestStore(t)
	longName := strings.Repeat("ã", domain.UserNameMax+1)
	longCollective := strings.Repeat("c", domain.CollectiveNameMax+1)

	tests := []struct {
		name    string
		mutate  func(*SignupInput)
		keys    []string
		msgPart map[string]string // substring each message must hold
	}{
		{"empty name", func(in *SignupInput) { in.Name = "" }, []string{"name"}, nil},
		{"blank name", func(in *SignupInput) { in.Name = " \t " }, []string{"name"}, nil},
		{"name over the limit", func(in *SignupInput) { in.Name = longName }, []string{"name"}, nil},
		{"name that is not UTF-8", func(in *SignupInput) { in.Name = "Ana\xff" }, []string{"name"}, nil},
		{"empty email", func(in *SignupInput) { in.Email = "" }, []string{"email"}, nil},
		{"email without a domain", func(in *SignupInput) { in.Email = "not-an-email" }, []string{"email"}, nil},
		{"email with a display name", func(in *SignupInput) { in.Email = "Ana <ana@example.com>" }, []string{"email"}, nil},
		{"empty phone", func(in *SignupInput) { in.Phone = "" }, []string{"phone"}, nil},
		{"phone with 9 digits", func(in *SignupInput) { in.Phone = "81 9876-543" }, []string{"phone"}, nil},
		{"phone with 12 digits", func(in *SignupInput) { in.Phone = "819876543210" }, []string{"phone"}, nil},
		{"phone without digits", func(in *SignupInput) { in.Phone = "abc" }, []string{"phone"}, nil},
		{"empty password", func(in *SignupInput) { in.Password, in.PasswordConfirm = "", "" }, []string{"password"}, nil},
		{"weak password", func(in *SignupInput) { in.Password, in.PasswordConfirm = "abc", "abc" }, []string{"password"},
			map[string]string{"password": auth.RuleLabels[auth.RuleMinLength]}},
		{"password without a digit", func(in *SignupInput) { in.Password, in.PasswordConfirm = "Senha#Forte", "Senha#Forte" }, []string{"password"},
			map[string]string{"password": auth.RuleLabels[auth.RuleDigit]}},
		{"password equal to the email", func(in *SignupInput) {
			in.Email = " Ana1@Example.com "
			in.Password, in.PasswordConfirm = "Ana1@Example.com", "Ana1@Example.com"
		}, []string{"password"}, map[string]string{"password": auth.RuleLabels[auth.RuleNotEmail]}},
		{"password that is not UTF-8", func(in *SignupInput) { in.Password, in.PasswordConfirm = "Senha#Forte1\xff", "Senha#Forte1\xff" }, []string{"password"}, nil},
		{"confirmation differs", func(in *SignupInput) { in.PasswordConfirm = "Senha#Forte2" }, []string{"password_confirm"},
			map[string]string{"password_confirm": "as senhas não conferem"}},
		{"empty kind", func(in *SignupInput) { in.CollectiveKind = "" }, []string{"collective_kind"}, nil},
		{"unknown kind", func(in *SignupInput) { in.CollectiveKind = "banda" }, []string{"collective_kind"}, nil},
		{"unknown kind with a picked collective", func(in *SignupInput) { in.CollectiveKind = "banda"; in.CollectiveID = "x" }, []string{"collective_kind"}, nil},
		{"empty role", func(in *SignupInput) { in.Role = "" }, []string{"role"}, nil},
		{"unknown role", func(in *SignupInput) { in.Role = "admin" }, []string{"role"}, nil},
		{"no collective", func(in *SignupInput) { in.CollectiveName = "" }, []string{"collective"}, nil},
		{"blank collective name", func(in *SignupInput) { in.CollectiveName = "   " }, []string{"collective"}, nil},
		{"collective name of one rune", func(in *SignupInput) { in.CollectiveName = " a " }, []string{"collective"}, nil},
		{"collective name over the limit", func(in *SignupInput) { in.CollectiveName = longCollective }, []string{"collective"}, nil},
		{"collective name that is not UTF-8", func(in *SignupInput) { in.CollectiveName = "Noite\xff Verde" }, []string{"collective"}, nil},
		{"collective name without a comparable key", func(in *SignupInput) { in.CollectiveName = "\u0301\u0301" }, []string{"collective"}, nil},
		{"every field at once", func(in *SignupInput) {
			*in = SignupInput{Email: "x", Phone: "1", Password: "a", PasswordConfirm: "b"}
		}, []string{"name", "email", "phone", "password", "password_confirm", "collective_kind", "collective", "role"}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := signupInput()
			tt.mutate(&in)
			fe := signupFieldErrors(t, Signup(t.Context(), s, signupOrigin, testNow, in))
			signupAssertKeys(t, fe, tt.keys...)
			for key, part := range tt.msgPart {
				if !strings.Contains(fe[key], part) {
					t.Errorf("%s message %q does not mention %q", key, fe[key], part)
				}
			}
			for key, msg := range fe {
				if strings.TrimSpace(msg) == "" {
					t.Errorf("%s has an empty message", key)
				}
			}
		})
	}
	// Validation stops before the transaction: nothing was written.
	signupAssertNothingWritten(t, s, "ana@example.com", "Noite Verde")
}

// signupHookStore wraps a Store and lets a test replace the repositories each
// WithTx call hands to fn. run counts the WithTx calls from 1.
type signupHookStore struct {
	domain.Store
	runs int
	hook func(run int, r domain.Repos) domain.Repos
}

func (s *signupHookStore) WithTx(ctx context.Context, fn func(ctx context.Context, r domain.Repos) error) error {
	s.runs++
	run := s.runs
	return s.Store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		return fn(ctx, s.hook(run, r))
	})
}

// signupFailingOutbox fails every Enqueue.
type signupFailingOutbox struct {
	domain.Outbox
	err error
}

func (o signupFailingOutbox) Enqueue(context.Context, *domain.OutboxMessage) error { return o.err }

// signupBlindUsers does not see any account by email, as a signup that
// another request has not committed yet.
type signupBlindUsers struct{ domain.Users }

func (signupBlindUsers) ByEmail(context.Context, string) (domain.User, error) {
	return domain.User{}, domain.ErrNotFound
}

// signupBlindCollectives does not see any collective by name key.
type signupBlindCollectives struct{ domain.Collectives }

func (signupBlindCollectives) ByNameKey(context.Context, string) (domain.Collective, error) {
	return domain.Collective{}, domain.ErrNotFound
}

func TestSignupRollsBackWhenAStepFails(t *testing.T) {
	s := newTestStore(t)
	boom := errors.New("outbox is down")
	hs := &signupHookStore{Store: s, hook: func(_ int, r domain.Repos) domain.Repos {
		r.Outbox = signupFailingOutbox{Outbox: r.Outbox, err: boom}
		return r
	}}
	in := signupInput()

	err := Signup(t.Context(), hs, signupOrigin, testNow, in)
	if !errors.Is(err, boom) {
		t.Fatalf("Signup = %v, want the Enqueue error", err)
	}
	var fe domain.FieldErrors
	if errors.As(err, &fe) {
		t.Errorf("a server error came back as field errors: %v", fe)
	}
	// The user, the collective, the membership and the token were written before
	// the failing step; none of them may be left behind.
	signupAssertNothingWritten(t, s, in.Email, in.CollectiveName)
}

func TestSignupEmailTakenRace(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	existing := insertActiveUser(t, s, "ana@example.com")
	// The first transaction misses the account (the other signup is not visible
	// yet), then Users.Create finds it taken.
	hs := &signupHookStore{Store: s, hook: func(run int, r domain.Repos) domain.Repos {
		if run == 1 {
			r.Users = signupBlindUsers{Users: r.Users}
		}
		return r
	}}
	in := signupInput()

	if err := Signup(ctx, hs, signupOrigin, testNow, in); err != nil {
		t.Fatalf("Signup: %v, want nil", err)
	}
	if hs.runs != 2 {
		t.Errorf("transactions = %d, want the signup and the existing-account retry", hs.runs)
	}
	r := s.Repos()
	msgs := signupOutbox(t, s)
	if len(msgs) != 1 || msgs[0].To != "ana@example.com" || !strings.Contains(msgs[0].Text, signupOrigin+"/entrar") {
		t.Fatalf("outbox = %+v, want one existing-account email", msgs)
	}
	if ms, err := r.Memberships.ListByUser(ctx, existing.ID); err != nil || len(ms) != 0 {
		t.Errorf("memberships = %+v, err %v; want none", ms, err)
	}
	if c, err := r.Collectives.ByNameKey(ctx, domain.NameKey(in.CollectiveName)); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("collective created by the losing signup: id %q, err %v", c.ID, err)
	}
	if n, err := r.Tokens.DeleteExpired(ctx, signupFarFuture); err != nil || n != 0 {
		t.Errorf("tokens = %d, err %v; want none", n, err)
	}
}

func TestSignupNameTakenRace(t *testing.T) {
	s := newTestStore(t)
	signupInsertCollective(t, s, "Noite Verde", domain.CollectiveKindColetivo, domain.CollectiveStatusPending)
	// The signup does not see the collective by name, so it tries to create one
	// and the store reports the name as taken.
	hs := &signupHookStore{Store: s, hook: func(_ int, r domain.Repos) domain.Repos {
		r.Collectives = signupBlindCollectives{Collectives: r.Collectives}
		return r
	}}
	in := signupInput()

	fe := signupFieldErrors(t, Signup(t.Context(), hs, signupOrigin, testNow, in))
	signupAssertKeys(t, fe, "collective")
	if hs.runs != 1 {
		t.Errorf("transactions = %d, want 1", hs.runs)
	}
	// The user written before the failing Create is rolled back with it.
	signupAssertNothingWritten(t, s, in.Email, "")
}
