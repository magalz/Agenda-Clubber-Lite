package domain

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

// The Validate tests cover the field rules of each entity. What a field rule
// cannot check is left to other tests:
//
//   - uniqueness (an email that differs only in case, a collective name that is
//     taken), references (an unknown approver, user, collective or creator) and
//     cascades: the backend contract suite, internal/store/storetest;
//   - events: phase 4.
//
// An email that is not in normalized form is a field error here, a rule of its
// own.

var t0 = time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)

func rep(s string, n int) string { return strings.Repeat(s, n) }

func hash32() []byte { return []byte(rep("\xAB", 32)) }

// check is one Validate case: mutate edits a valid baseline and want lists the
// field keys that must fail; no keys means the entity must be valid.
type check[T any] struct {
	name   string
	mutate func(*T)
	want   []string
}

func c[T any](name string, mutate func(*T), want ...string) check[T] {
	return check[T]{name, mutate, want}
}

func runChecks[T any](t *testing.T, baseline func() T, validate func(T) error, cases []check[T]) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := baseline()
			if tc.mutate != nil {
				tc.mutate(&v)
			}
			assertFields(t, validate(v), tc.want...)
		})
	}
}

// assertFields checks that err is nil when want is empty, and otherwise a
// FieldErrors whose keys are exactly want, each with a message.
func assertFields(t *testing.T, err error, want ...string) {
	t.Helper()
	if len(want) == 0 {
		if err != nil {
			t.Errorf("Validate() = %v, want nil", err)
		}
		return
	}
	var fe FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("Validate() = %v (%T), want a FieldErrors with keys %v", err, err, want)
	}
	if got := slices.Sorted(maps.Keys(fe)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Errorf("failing fields = %v, want %v (%v)", got, want, err)
	}
	for field, msg := range fe {
		if msg == "" {
			t.Errorf("field %q has an empty message", field)
		}
	}
}

func baseUser() User {
	return User{
		ID: "u1", Name: "Bia", Email: "bia@example.com", Phone: "85988887777",
		PasswordHash: "hash", PasswordChangedAt: t0, Status: UserStatusPendingEmail,
		CreatedAt: t0, UpdatedAt: t0,
	}
}

func TestUserValidate(t *testing.T) {
	runChecks(t, baseUser, User.Validate, []check[User]{
		c[User]("valid user", nil),
		c("entity ID is assigned by the backend", func(u *User) { u.ID = "" }),
		c("user status disabled", func(u *User) { u.Status = UserStatusDisabled }),
		c("user status banned", func(u *User) { u.Status = "banned" }, "status"),
		c("empty status", func(u *User) { u.Status = "" }, "status"),

		c("phone with a letter", func(u *User) { u.Phone = "8599999888a" }, "phone"),
		c("phone with a space", func(u *User) { u.Phone = "85 99998888" }, "phone"),
		c("phone with a plus", func(u *User) { u.Phone = "+5585999998888" }, "phone"),
		c("phone with a non-ASCII digit", func(u *User) { u.Phone = "8599999888٣" }, "phone"),
		c("empty phone", func(u *User) { u.Phone = "" }, "phone"),
		c("phone with 9 digits", func(u *User) { u.Phone = "859999988" }, "phone"),
		c("phone with 10 digits", func(u *User) { u.Phone = "8532221111" }),
		c("phone with 12 digits", func(u *User) { u.Phone = "859999988887" }, "phone"),

		c("empty name", func(u *User) { u.Name = "" }, "name"),
		c("name of only spaces", func(u *User) { u.Name = "   \t" }, "name"),
		c("name of 120 characters", func(u *User) { u.Name = rep("x", 120) }),
		c("name of 121 characters", func(u *User) { u.Name = rep("x", 121) }, "name"),
		c("name of 120 runes", func(u *User) { u.Name = rep("ç", 120) }),
		c("name of 121 runes", func(u *User) { u.Name = rep("ç", 121) }, "name"),
		c("name counted after trimming", func(u *User) { u.Name = "  " + rep("x", 120) + "  " }),

		c("email of 2 characters", func(u *User) { u.Email = "a@" }, "email"),
		c("empty email", func(u *User) { u.Email = "" }, "email"),
		c("email of 254 characters", func(u *User) { u.Email = rep("a", 254-len("@example.com")) + "@example.com" }),
		c("email of 255 characters", func(u *User) { u.Email = rep("a", 255-len("@example.com")) + "@example.com" }, "email"),
		c("email without a dot in the domain", func(u *User) { u.Email = "bia@localhost" }, "email"),
		c("email with a display name", func(u *User) { u.Email = "Bia <bia@example.com>" }, "email"),
		c("email not lowercase", func(u *User) { u.Email = "Bia@Example.COM" }, "email"),
		c("email with surrounding spaces", func(u *User) { u.Email = " bia@example.com " }, "email"),

		c("negative failed_logins", func(u *User) { u.FailedLogins = -1 }, "failed_logins"),
		c("failed_logins 0", func(u *User) { u.FailedLogins = 0 }),
		c("failed_logins 5", func(u *User) { u.FailedLogins = 5 }),
		c("missing password hash", func(u *User) { u.PasswordHash = "" }, "password_hash"),

		c("every failing field is reported", func(u *User) {
			u.Name, u.Email, u.Phone, u.Status, u.FailedLogins = "", "x", "1", "?", -2
		}, "name", "email", "phone", "status", "failed_logins"),
	})
}

func baseCollective() Collective {
	return Collective{
		ID: "c1", Name: "Produtora Dois", NameKey: "produtora dois",
		Kind: CollectiveKindProdutora, Status: CollectiveStatusPending,
		CreatedAt: t0, UpdatedAt: t0,
	}
}

// named gives a collective the name and the key a service derives from it.
func named(name string) func(*Collective) {
	return func(col *Collective) { col.Name, col.NameKey = name, NameKey(name) }
}

func TestCollectiveValidate(t *testing.T) {
	runChecks(t, baseCollective, Collective.Validate, []check[Collective]{
		c[Collective]("valid collective", nil),
		c("entity ID is assigned by the backend", func(col *Collective) { col.ID = "" }),
		c("collective kind unknown", func(col *Collective) { col.Kind = "banda" }, "kind"),
		c("empty kind", func(col *Collective) { col.Kind = "" }, "kind"),
		c("collective status unknown", func(col *Collective) { col.Status = "open" }, "status"),
		c("empty status", func(col *Collective) { col.Status = "" }, "status"),

		c("collective name of 1 character", named("X"), "name"),
		c("collective name of 2 characters", named("Xy")),
		c("collective name of 80 characters", named(rep("x", 80))),
		c("collective name of 81 characters", named(rep("x", 81)), "name"),
		c("collective name of 80 runes", named(rep("ç", 80))),
		c("collective name of 81 runes", named(rep("ç", 81)), "name"),
		c("collective name counted after trimming", named("  X  "), "name"),
		c("empty collective name", named(""), "name", "name_key"),
		c("collective name of only spaces", named("  \t "), "name", "name_key"),

		c("accented name with its folded key", func(col *Collective) {
			col.Name, col.NameKey = "  Coletivo   Ação ", "coletivo acao"
		}),
		c("empty name_key", func(col *Collective) { col.NameKey = "" }, "name_key"),
		c("name_key of another name", func(col *Collective) { col.NameKey = "coletivo um" }, "name_key"),
		c("name_key not folded", func(col *Collective) {
			col.Name, col.NameKey = "Coletivo Ação", "coletivo ação"
		}, "name_key"),
		c("name_key not lowercase", func(col *Collective) { col.NameKey = "Produtora Dois" }, "name_key"),
	})
}

func baseMembership() Membership {
	return Membership{UserID: "u1", CollectiveID: "c1", Role: RoleMember, Status: MembershipStatusPending, RequestedAt: t0}
}

func TestMembershipValidate(t *testing.T) {
	runChecks(t, baseMembership, Membership.Validate, []check[Membership]{
		c[Membership]("valid membership", nil),
		c("owner role", func(m *Membership) { m.Role = RoleOwner }),
		c("membership role unknown", func(m *Membership) { m.Role = "admin" }, "role"),
		c("empty role", func(m *Membership) { m.Role = "" }, "role"),
		c("membership status unknown", func(m *Membership) { m.Status = "open" }, "status"),
		c("empty status", func(m *Membership) { m.Status = "" }, "status"),
		c("missing user", func(m *Membership) { m.UserID = "" }, "user_id"),
		c("missing collective", func(m *Membership) { m.CollectiveID = "" }, "collective_id"),
		c("missing both references", func(m *Membership) { m.UserID, m.CollectiveID = "", "" }, "user_id", "collective_id"),
	})
}

func baseToken() Token {
	return Token{
		ID: "t1", UserID: "u1", Purpose: TokenPurposeVerifyEmail, Hash: hash32(),
		ExpiresAt: t0.Add(time.Hour), CreatedAt: t0,
	}
}

func TestTokenValidate(t *testing.T) {
	runChecks(t, baseToken, Token.Validate, []check[Token]{
		c[Token]("valid token", nil),
		c("entity ID is assigned by the backend", func(tok *Token) { tok.ID = "" }),
		c("missing user", func(tok *Token) { tok.UserID = "" }, "user_id"),
		c("token purpose unknown", func(tok *Token) { tok.Purpose = "login" }, "purpose"),
		c("empty purpose", func(tok *Token) { tok.Purpose = "" }, "purpose"),
		c("reset_password", func(tok *Token) { tok.Purpose = TokenPurposeResetPassword }),

		c("token hash of 31 bytes", func(tok *Token) { tok.Hash = hash32()[:31] }, "hash"),
		c("token hash of 33 bytes", func(tok *Token) { tok.Hash = append(hash32(), 0) }, "hash"),
		c("missing token hash", func(tok *Token) { tok.Hash = nil }, "hash"),

		c("change_email without new_email", func(tok *Token) { tok.Purpose = TokenPurposeChangeEmail }, "new_email"),
		c("change_email with new_email", func(tok *Token) {
			tok.Purpose, tok.NewEmail = TokenPurposeChangeEmail, "novo@example.com"
		}),
		c("change_email with an email not normalized", func(tok *Token) {
			tok.Purpose, tok.NewEmail = TokenPurposeChangeEmail, "Novo@Example.com"
		}, "new_email"),
		c("change_email with an invalid email", func(tok *Token) {
			tok.Purpose, tok.NewEmail = TokenPurposeChangeEmail, "novo"
		}, "new_email"),
		c("verify_email with new_email", func(tok *Token) { tok.NewEmail = "novo@example.com" }, "new_email"),
		c("reset_password with new_email", func(tok *Token) {
			tok.Purpose, tok.NewEmail = TokenPurposeResetPassword, "novo@example.com"
		}, "new_email"),
		c("unknown purpose with new_email", func(tok *Token) {
			tok.Purpose, tok.NewEmail = "login", "novo@example.com"
		}, "purpose", "new_email"),

		c("token expiring before it was created", func(tok *Token) { tok.ExpiresAt = t0.Add(-time.Second) }, "expires_at"),
		c("token expiring when it was created", func(tok *Token) { tok.ExpiresAt = t0 }, "expires_at"),
		c("token without an expiry", func(tok *Token) { tok.ExpiresAt = time.Time{} }, "expires_at"),
		c("token already used", func(tok *Token) { used := t0.Add(time.Minute); tok.UsedAt = &used }),
	})
}

func baseSession() Session {
	return Session{
		Hash: hash32(), UserID: "u1", CreatedAt: t0, LastSeenAt: t0,
		ExpiresAt: t0.Add(24 * time.Hour), UserAgent: "Mozilla/5.0",
	}
}

func TestSessionValidate(t *testing.T) {
	runChecks(t, baseSession, Session.Validate, []check[Session]{
		c[Session]("valid session", nil),
		c("session without a user agent", func(s *Session) { s.UserAgent = "" }),
		c("missing user", func(s *Session) { s.UserID = "" }, "user_id"),
		c("session hash of 31 bytes", func(s *Session) { s.Hash = hash32()[:31] }, "hash"),
		c("session hash of 33 bytes", func(s *Session) { s.Hash = append(hash32(), 0) }, "hash"),
		c("missing session hash", func(s *Session) { s.Hash = nil }, "hash"),
		c("session user agent of 300 characters", func(s *Session) { s.UserAgent = rep("x", 300) }),
		c("session user agent of 301 characters", func(s *Session) { s.UserAgent = rep("x", 301) }, "user_agent"),
		c("session user agent of 300 runes", func(s *Session) { s.UserAgent = rep("ç", 300) }),
		c("session user agent of 301 runes", func(s *Session) { s.UserAgent = rep("ç", 301) }, "user_agent"),
		c("session expiring before it was created", func(s *Session) { s.ExpiresAt = t0.Add(-time.Second) }, "expires_at"),
		c("session expiring when it was created", func(s *Session) { s.ExpiresAt = t0 }, "expires_at"),
		c("session without an expiry", func(s *Session) { s.ExpiresAt = time.Time{} }, "expires_at"),
	})
}

func baseOutbox() OutboxMessage {
	return OutboxMessage{To: "ana@example.com", Subject: "Oi", Text: "Oi", NextAttemptAt: t0, CreatedAt: t0}
}

func TestOutboxMessageValidate(t *testing.T) {
	runChecks(t, baseOutbox, OutboxMessage.Validate, []check[OutboxMessage]{
		c[OutboxMessage]("valid outbox message", nil),
		c("entity ID is assigned by the backend", func(m *OutboxMessage) { m.ID = "" }),
		c("message with an HTML part", func(m *OutboxMessage) { m.HTML = "<p>Oi</p>" }),
		c("outbox message without a recipient", func(m *OutboxMessage) { m.To = "" }, "to"),
		c("recipient that is not an email", func(m *OutboxMessage) { m.To = "ana" }, "to"),
		c("recipient with a display name", func(m *OutboxMessage) { m.To = "Ana <ana@example.com>" }, "to"),
		c("recipient with a trailing newline", func(m *OutboxMessage) { m.To = "ana@example.com\n" }, "to"),
		c("recipient in mixed case", func(m *OutboxMessage) { m.To = "Ana@Example.com" }),
		c("empty subject", func(m *OutboxMessage) { m.Subject = "" }, "subject"),
		c("subject of only spaces", func(m *OutboxMessage) { m.Subject = "  " }, "subject"),
		c("subject of 200 characters", func(m *OutboxMessage) { m.Subject = rep("x", 200) }),
		c("subject of 201 characters", func(m *OutboxMessage) { m.Subject = rep("x", 201) }, "subject"),
		c("subject of 200 runes", func(m *OutboxMessage) { m.Subject = rep("ç", 200) }),
		c("subject of 201 runes", func(m *OutboxMessage) { m.Subject = rep("ç", 201) }, "subject"),
		c("empty text", func(m *OutboxMessage) { m.Text = "" }, "text"),
		c("text of only spaces", func(m *OutboxMessage) { m.Text = " \n" }, "text"),
		c("negative attempts", func(m *OutboxMessage) { m.Attempts = -1 }, "attempts"),
		c("attempts 3", func(m *OutboxMessage) { m.Attempts = 3 }),
		c("message that gave up", func(m *OutboxMessage) { m.GaveUp, m.LastError = true, "smtp: 550" }),
	})
}

func baseAuditEntry() AuditEntry {
	return AuditEntry{
		At: t0, Action: AuditCollectiveRenamed, ActorID: "u1", ActorName: "Ana",
		UserID: "u2", UserName: "Bia", CollectiveID: "c1", CollectiveName: "Produtora Dois",
		Detail: "Produtora Um -> Produtora Dois", Reason: "nome oficial",
	}
}

func TestAuditEntryValidate(t *testing.T) {
	cases := []check[AuditEntry]{
		c[AuditEntry]("valid entry", nil),
		c("entity ID is assigned by the backend", func(e *AuditEntry) { e.ID = "" }),
		c("only the required fields", func(e *AuditEntry) {
			*e = AuditEntry{At: t0, Action: AuditAccountApproved, ActorID: "u1"}
		}),
		c("an action about nobody and no collective", func(e *AuditEntry) {
			e.UserID, e.UserName, e.CollectiveID, e.CollectiveName = "", "", "", ""
		}),
		c("snapshot IDs of deleted rows are not checked", func(e *AuditEntry) {
			e.ActorID, e.UserID, e.CollectiveID = "gone-1", "gone-2", "gone-3"
		}),

		c("audit action unknown", func(e *AuditEntry) { e.Action = "login" }, "action"),
		c("empty action", func(e *AuditEntry) { e.Action = "" }, "action"),
		c("action in the wrong case", func(e *AuditEntry) { e.Action = "Account_Approved" }, "action"),

		c("zero time", func(e *AuditEntry) { e.At = time.Time{} }, "at"),
		c("missing actor", func(e *AuditEntry) { e.ActorID = "" }, "actor_id"),

		c("actor name of 120 runes", func(e *AuditEntry) { e.ActorName = rep("ç", 120) }),
		c("actor name of 121 runes", func(e *AuditEntry) { e.ActorName = rep("ç", 121) }, "actor_name"),
		c("user name of 120 runes", func(e *AuditEntry) { e.UserName = rep("ç", 120) }),
		c("user name of 121 runes", func(e *AuditEntry) { e.UserName = rep("ç", 121) }, "user_name"),
		c("collective name of 80 runes", func(e *AuditEntry) { e.CollectiveName = rep("ç", 80) }),
		c("collective name of 81 runes", func(e *AuditEntry) { e.CollectiveName = rep("ç", 81) }, "collective_name"),
		c("detail of 500 runes", func(e *AuditEntry) { e.Detail = rep("ç", 500) }),
		c("detail of 501 runes", func(e *AuditEntry) { e.Detail = rep("ç", 501) }, "detail"),
		c("reason of 500 runes", func(e *AuditEntry) { e.Reason = rep("ç", 500) }),
		c("reason of 501 runes", func(e *AuditEntry) { e.Reason = rep("ç", 501) }, "reason"),
		c("detail of 500 characters", func(e *AuditEntry) { e.Detail = rep("x", 500) }),
		c("reason of 501 characters", func(e *AuditEntry) { e.Reason = rep("x", 501) }, "reason"),

		c("every failing field is reported", func(e *AuditEntry) {
			e.Action, e.At, e.ActorID = "?", time.Time{}, ""
			e.ActorName, e.UserName, e.CollectiveName = rep("x", 121), rep("x", 121), rep("x", 81)
			e.Detail, e.Reason = rep("x", 501), rep("x", 501)
		}, "action", "at", "actor_id", "actor_name", "user_name", "collective_name", "detail", "reason"),
	}
	for _, a := range allAuditActions {
		cases = append(cases, c("action "+string(a), func(e *AuditEntry) { e.Action = a }))
	}
	runChecks(t, baseAuditEntry, AuditEntry.Validate, cases)
}

func TestFieldErrors(t *testing.T) {
	var _ error = FieldErrors(nil)

	t.Run("Err is nil when there are no errors", func(t *testing.T) {
		for name, fe := range map[string]FieldErrors{"nil map": nil, "empty map": {}} {
			if err := fe.Err(); err != nil {
				t.Errorf("%s: Err() = %v (%T), want a nil error", name, err, err)
			}
		}
	})

	t.Run("Add starts from the zero value", func(t *testing.T) {
		var fe FieldErrors
		fe.Add("name", "obrigatório")
		fe.Add("email", "informe um e-mail válido")
		err := fe.Err()
		if err == nil {
			t.Fatal("Err() = nil after Add")
		}
		var got FieldErrors
		if !errors.As(fmt.Errorf("signup: %w", err), &got) {
			t.Fatalf("errors.As(%v) did not find a FieldErrors", err)
		}
		if got["name"] != "obrigatório" || got["email"] != "informe um e-mail válido" || len(got) != 2 {
			t.Errorf("fields = %v", got)
		}
	})

	t.Run("Error is sorted by field and repeatable", func(t *testing.T) {
		fe := FieldErrors{"phone": "informe de 10 a 11 dígitos", "name": "obrigatório", "email": "inválido"}
		const want = "email: inválido; name: obrigatório; phone: informe de 10 a 11 dígitos"
		for range 50 { // map iteration order is random
			if got := fe.Error(); got != want {
				t.Fatalf("Error() = %q, want %q", got, want)
			}
		}
	})

	t.Run("Error of one field", func(t *testing.T) {
		if got, want := (FieldErrors{"name": "obrigatório"}).Error(), "name: obrigatório"; got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})
}

func TestNormalizeEmail(t *testing.T) {
	longLocal := func(total int) string { return rep("a", total-len("@example.com")) + "@example.com" }

	valid := []struct{ name, in, want string }{
		{"already normalized", "ana@example.com", "ana@example.com"},
		{"case folded", "Ana@Example.COM", "ana@example.com"},
		{"surrounding spaces", "  ana@example.com  ", "ana@example.com"},
		{"surrounding tab and newline", "\tana@example.com\n", "ana@example.com"},
		{"plus tag and subdomains", "Ana.Silva+Tag@Mail.Example.com.br", "ana.silva+tag@mail.example.com.br"},
		{"254 characters", longLocal(254), longLocal(254)},
	}
	for _, tc := range valid {
		t.Run("valid "+tc.name, func(t *testing.T) {
			got, err := NormalizeEmail(tc.in)
			if err != nil || got != tc.want {
				t.Errorf("NormalizeEmail(%q) = %q, %v; want %q, nil", tc.in, got, err, tc.want)
			}
			if again, err := NormalizeEmail(got); err != nil || again != got {
				t.Errorf("not idempotent: NormalizeEmail(%q) = %q, %v", got, again, err)
			}
		})
	}

	invalid := []struct{ name, in string }{
		{"empty", ""},
		{"only spaces", "   "},
		{"display name", "Ana <ana@example.com>"},
		{"angle brackets", "<ana@example.com>"},
		{"quoted display name", `"Ana" <ana@example.com>`},
		{"trailing comment", "ana@example.com (Ana)"},
		{"quoted local part", `"ana"@example.com`},
		{"inner space before the at", "ana @example.com"},
		{"inner space in the domain", "ana@exa mple.com"},
		{"inner no-break space", "ana silva@example.com"},
		{"two addresses", "ana@example.com ana@example.org"},
		{"two addresses with a comma", "ana@example.com,bia@example.com"},
		{"no dot in the domain", "ana@localhost"},
		{"domain literal", "ana@[127.0.0.1]"},
		{"no domain", "ana@"},
		{"no local part", "@example.com"},
		{"no at", "ana"},
		{"domain ending with a dot", "ana@example."},
		{"domain starting with a dot", "ana@.example.com"},
		{"255 characters", longLocal(255)},
	}
	for _, tc := range invalid {
		t.Run("invalid "+tc.name, func(t *testing.T) {
			got, err := NormalizeEmail(tc.in)
			if err == nil {
				t.Fatalf("NormalizeEmail(%q) = %q, nil; want an error", tc.in, got)
			}
			if got != "" {
				t.Errorf("NormalizeEmail(%q) returned %q with an error, want \"\"", tc.in, got)
			}
			var fe FieldErrors
			if !errors.As(err, &fe) || fe["email"] == "" || len(fe) != 1 {
				t.Errorf("error = %v (%T), want a FieldErrors with only the email key", err, err)
			}
		})
	}

	t.Run("the length message is what rejects 255 characters", func(t *testing.T) {
		_, err := NormalizeEmail(longLocal(255))
		if err == nil || !strings.Contains(err.Error(), "254") {
			t.Errorf("error = %v, want a message that names the 254-character limit", err)
		}
	})
}

func TestNameKey(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"spec example", "  Coletivo   Ação  ", "coletivo acao"},
		{"empty", "", ""},
		{"only spaces", " \t\n ", ""},
		{"lowercase a", "áàâãä", "aaaaa"},
		{"uppercase a", "ÁÀÂÃÄ", "aaaaa"},
		{"lowercase e", "éèêë", "eeee"},
		{"uppercase e", "ÉÈÊË", "eeee"},
		{"lowercase i", "íìîï", "iiii"},
		{"uppercase i", "ÍÌÎÏ", "iiii"},
		{"lowercase o", "óòôõö", "ooooo"},
		{"uppercase o", "ÓÒÔÕÖ", "ooooo"},
		{"lowercase u", "úùûü", "uuuu"},
		{"uppercase u", "ÚÙÛÜ", "uuuu"},
		{"cedilla", "çÇ", "cc"},
		{"tilde n", "ñÑ", "nn"},
		{"tabs and newlines", "a\tb\nc\r\nd  e", "a b c d e"},
		{"unicode spaces", "a b c　d", "a b c d"},
		{"decomposed accents", "Ação", "acao"},
		{"punctuation and digits kept", "Zé & Cia. #1", "ze & cia. #1"},
		{"plain name", "Produtora Dois", "produtora dois"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := NameKey(tc.in)
			if got != tc.want {
				t.Errorf("NameKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := NameKey(got); again != got {
				t.Errorf("not idempotent: NameKey(%q) = %q", got, again)
			}
		})
	}
}

func TestCleanCollectiveName(t *testing.T) {
	const (
		required = "campo obrigatório"
		invalid  = "valor inválido"
		control  = "não use quebras de linha nem caracteres de controle"
		length   = "use de 2 a 80 caracteres"
	)
	if MsgControl != control {
		t.Errorf("MsgControl = %q, want %q", MsgControl, control)
	}

	for _, tc := range []struct {
		name, in string
		want     string // the cleaned name, "" when there is a problem
		problem  string
	}{
		{"plain name", "Coletivo Ação", "Coletivo Ação", ""},
		{"trimmed", " \t Casa Dois \n", "Casa Dois", ""},
		{"inside kept as typed", "  Coletivo   AÇÃO  ", "Coletivo   AÇÃO", ""},
		{"decomposed accents kept", "Ação", "Ação", ""},
		{"exactly 2 runes", "ab", "ab", ""},
		{"exactly 2 runes with accents", "éé", "éé", ""},
		{"exactly 80 runes", rep("a", 80), rep("a", 80), ""},
		{"80 multibyte runes count as 80", rep("ç", 80), rep("ç", 80), ""},

		{"empty", "", "", required},
		{"only space", " \t\n ", "", required},
		{"1 rune", "a", "", length},
		{"1 multibyte rune", "é", "", length},
		{"1 rune after trimming", "  a  ", "", length},
		{"81 runes", rep("a", 81), "", length},
		{"81 multibyte runes", rep("ç", 81), "", length},

		{"invalid UTF-8", "ab\xffcd", "", invalid},
		{"invalid UTF-8 wins over control", "\xff\x00", "", invalid},
		{"only combining marks", "̃̃", "", invalid},

		{"newline inside", "linha\noutra", "", control},
		{"carriage return inside", "a\rb", "", control},
		{"tab inside", "a\tb", "", control},
		{"NUL inside", "ab\x00cd", "", control},
		{"DEL inside", "ab\x7fcd", "", control},
		{"C1 control inside", "ab\u0085cd", "", control},
		{"control wins over length", "a\x00", "", control},
		{"control wins over empty key", "̃\x00", "", control},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, problem := CleanCollectiveName(tc.in)
			if got != tc.want || problem != tc.problem {
				t.Errorf("CleanCollectiveName(%q) = (%q, %q), want (%q, %q)", tc.in, got, problem, tc.want, tc.problem)
			}
			if problem == "" && NameKey(got) == "" {
				t.Errorf("accepted %q, which has no comparison key", tc.in)
			}
		})
	}
}
