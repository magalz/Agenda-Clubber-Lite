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
//   - for events, also the catalog of styles and of event types, the rule that a
//     new event is not in the past and who may write one: the event service.
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

func TestTextProblem(t *testing.T) {
	const (
		control   = "não use quebras de linha nem caracteres de controle"
		textOnly  = "use só texto, sem caracteres de controle"
		atMost5   = "use no máximo 5 caracteres"
		between25 = "use de 2 a 5 caracteres"
	)
	for _, tc := range []struct {
		name      string
		s         string
		min, max  int
		multiline bool
		want      string
	}{
		{"single line in range", "abc", 1, 5, false, ""},
		{"single line of exactly max runes", rep("ç", 5), 1, 5, false, ""},
		{"single line of max plus one runes", rep("ç", 6), 1, 5, false, atMost5},
		{"single line empty and required", "", 1, 5, false, MsgRequired},
		{"single line of spaces and required", "   ", 1, 5, false, MsgRequired},
		{"single line empty and optional", "", 0, 5, false, ""},
		{"single line of spaces and optional", "   ", 0, 5, false, ""},
		{"single line optional but too long", "abcdef", 0, 5, false, atMost5},
		{"single line counted after trimming", "  abcde  ", 1, 5, false, ""},
		{"single line below a minimum of 2", "a", 2, 5, false, between25},
		{"single line above a minimum of 2", "abcdef", 2, 5, false, between25},
		{"single line at a minimum of 2", "ab", 2, 5, false, ""},

		{"single line with a line break inside", "a\nb", 1, 5, false, control},
		{"single line with a trailing line break", "abc\n", 1, 5, false, control},
		{"single line with a carriage return", "a\rb", 1, 5, false, control},
		{"single line with a tab", "a\tb", 1, 5, false, control},
		{"single line with a NUL", "a\x00b", 1, 5, false, control},
		{"single line with a DEL", "a\x7fb", 1, 5, false, control},
		{"single line with a C1 control", "a\u0085b", 1, 5, false, control},
		{"single line that is only a line break", "\n", 0, 5, false, control},
		{"single line with a control character and too long", "a\nbcdefg", 1, 5, false, control},
		{"single line that is not UTF-8", "a\xffb", 1, 5, false, MsgInvalid},
		{"single line that is not UTF-8 and has a line break", "\xff\n", 1, 5, false, MsgInvalid},
		{"single line with non-ASCII letters and a space", "são josé", 1, 20, false, ""},

		{"multi-line in range", "abc", 1, 5, true, ""},
		{"multi-line with a line break and a tab", "a\tb\nc", 1, 20, true, ""},
		{"multi-line with a trailing line break", "abc\n", 1, 3, true, ""},
		{"multi-line empty and optional", "", 0, 5, true, ""},
		{"multi-line empty and required", "", 1, 5, true, MsgRequired},
		{"multi-line of only line breaks and required", "\n\n \t", 1, 5, true, MsgRequired},
		{"multi-line of max runes", rep("ç", 5), 1, 5, true, ""},
		{"multi-line of max plus one runes", rep("ç", 6), 1, 5, true, atMost5},
		{"multi-line below a minimum of 2", "a", 2, 5, true, between25},
		{"multi-line with a carriage return", "a\r\nb", 1, 20, true, textOnly},
		{"multi-line with a lone carriage return", "a\rb", 1, 20, true, textOnly},
		{"multi-line with a NUL", "a\x00b", 1, 20, true, textOnly},
		{"multi-line with a DEL", "a\x7fb", 1, 20, true, textOnly},
		{"multi-line with a vertical tab", "a\vb", 1, 20, true, textOnly},
		{"multi-line with a C1 control", "a\u0085b", 1, 20, true, textOnly},
		{"multi-line with a control character and too long", "a\x00bcdefg", 1, 5, true, textOnly},
		{"multi-line that is not UTF-8", "a\xffb", 1, 20, true, MsgInvalid},
		{"multi-line that is not UTF-8 and has a NUL", "\xff\x00", 1, 20, true, MsgInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TextProblem(tc.s, tc.min, tc.max, tc.multiline); got != tc.want {
				t.Errorf("TextProblem(%q, %d, %d, %v) = %q, want %q", tc.s, tc.min, tc.max, tc.multiline, got, tc.want)
			}
		})
	}
}

func baseEvent() Event {
	return Event{
		ID: "e1", CollectiveID: "c1", Name: "Noite Verde", Type: EventTypeFesta,
		StartsAt: t0.Add(48 * time.Hour), DurationMin: 240, PriceCents: 3000,
		TicketURL: "https://example.com/ingressos", Style: "Techno", Substyle: "Melodic",
		State: "CE", City: "Fortaleza", Venue: "Casa Azul", Artists: "DJ Ana\nDJ Bia",
		Description: "Uma noite de techno.\n\nPortão às 22h.",
		CreatedBy:   "u1", UpdatedBy: "u1", CreatedAt: t0, UpdatedAt: t0, Version: 1,
	}
}

func TestEventValidate(t *testing.T) {
	const (
		maxPrice = 9_999_999
		urlBase  = "https://example.com/"
	)
	cases := []check[Event]{
		c[Event]("valid event", nil),
		c("entity ID is assigned by the backend", func(e *Event) { e.ID = "" }),
		c("only the required fields", func(e *Event) {
			*e = Event{
				CollectiveID: "c1", Name: "Rave", Type: EventTypeRave, StartsAt: t0, DurationMin: 15, Free: true,
				Style: "Techno", State: "PE", City: "Recife", CreatedAt: t0,
			}
		}),
		c("references that the store checks are not checked", func(e *Event) { e.CollectiveID, e.CreatedBy = "gone-1", "gone-2" }),
		c("a creator and an editor that were deleted", func(e *Event) { e.CreatedBy, e.UpdatedBy = "", "" }),
		c("version and update time are not checked", func(e *Event) { e.Version, e.UpdatedAt = 0, time.Time{} }),

		c("missing collective", func(e *Event) { e.CollectiveID = "" }, "collective_id"),
		c("zero start time", func(e *Event) { e.StartsAt = time.Time{} }, "starts_at"),
		c("zero creation time", func(e *Event) { e.CreatedAt = time.Time{} }, "created_at"),

		c("empty name", func(e *Event) { e.Name = "" }, "name"),
		c("name of only spaces", func(e *Event) { e.Name = "   \t" }, "name"),
		c("name of 1 rune", func(e *Event) { e.Name = "x" }, "name"),
		c("name of 2 runes", func(e *Event) { e.Name = "xy" }),
		c("name of 120 runes", func(e *Event) { e.Name = rep("ç", 120) }),
		c("name of 121 runes", func(e *Event) { e.Name = rep("ç", 121) }, "name"),
		c("name counted after trimming", func(e *Event) { e.Name = "  " + rep("x", 120) + "  " }),
		c("name with a line break", func(e *Event) { e.Name = "Noite\nVerde" }, "name"),
		c("name with a tab", func(e *Event) { e.Name = "Noite\tVerde" }, "name"),
		c("name that is not UTF-8", func(e *Event) { e.Name = "Noite\xff" }, "name"),

		c("empty type", func(e *Event) { e.Type = "" }, "type"),
		c("type unknown", func(e *Event) { e.Type = "show" }, "type"),
		c("type in the wrong case", func(e *Event) { e.Type = "Festa" }, "type"),
		c("type outros with its name", func(e *Event) { e.Type, e.TypeOther = EventTypeOutros, "Sarau" }),
		c("type outros without its name", func(e *Event) { e.Type, e.TypeOther = EventTypeOutros, "" }, "type_other"),
		c("type outros with a name of only spaces", func(e *Event) { e.Type, e.TypeOther = EventTypeOutros, "  " }, "type_other"),
		c("type_other of 40 runes", func(e *Event) { e.Type, e.TypeOther = EventTypeOutros, rep("ç", 40) }),
		c("type_other of 41 runes", func(e *Event) { e.Type, e.TypeOther = EventTypeOutros, rep("ç", 41) }, "type_other"),
		c("type_other with a line break", func(e *Event) { e.Type, e.TypeOther = EventTypeOutros, "Sa\nrau" }, "type_other"),
		c("type_other with another type", func(e *Event) { e.TypeOther = "Sarau" }, "type_other"),
		c("type_other of spaces with another type", func(e *Event) { e.TypeOther = " " }, "type_other"),

		c("duration of 14 minutes", func(e *Event) { e.DurationMin = 14 }, "duration_min"),
		c("duration of 15 minutes", func(e *Event) { e.DurationMin = 15 }),
		c("duration of 1439 minutes", func(e *Event) { e.DurationMin = 1439 }),
		c("duration of 1440 minutes", func(e *Event) { e.DurationMin = 1440 }, "duration_min"),
		c("duration of 0 minutes", func(e *Event) { e.DurationMin = 0 }, "duration_min"),
		c("negative duration", func(e *Event) { e.DurationMin = -60 }, "duration_min"),

		c("free with no price", func(e *Event) { e.Free, e.PriceCents = true, 0 }),
		c("free with a price", func(e *Event) { e.Free, e.PriceCents = true, 1 }, "price_cents"),
		c("free with a negative price", func(e *Event) { e.Free, e.PriceCents = true, -1 }, "price_cents"),
		c("paid with no price", func(e *Event) { e.Free, e.PriceCents = false, 0 }, "price_cents"),
		c("paid with a price of 1 cent", func(e *Event) { e.PriceCents = 1 }),
		c("paid with the greatest price", func(e *Event) { e.PriceCents = maxPrice }),
		c("paid with a price above the greatest", func(e *Event) { e.PriceCents = maxPrice + 1 }, "price_cents"),
		c("paid with a negative price", func(e *Event) { e.PriceCents = -100 }, "price_cents"),

		c("no ticket URL", func(e *Event) { e.TicketURL = "" }),
		c("ticket URL over http", func(e *Event) { e.TicketURL = "http://example.com/x?a=1#b" }),
		c("ticket URL with a port", func(e *Event) { e.TicketURL = "https://example.com:8443/x" }),
		c("ticket URL with an uppercase scheme", func(e *Event) { e.TicketURL = "HTTPS://example.com" }),
		c("ticket URL with a javascript scheme", func(e *Event) { e.TicketURL = "javascript:alert(1)" }, "ticket_url"),
		c("ticket URL with a data scheme", func(e *Event) { e.TicketURL = "data:text/html,x" }, "ticket_url"),
		c("ticket URL with an ftp scheme", func(e *Event) { e.TicketURL = "ftp://example.com/x" }, "ticket_url"),
		c("ticket URL with a mailto scheme", func(e *Event) { e.TicketURL = "mailto:a@example.com" }, "ticket_url"),
		c("ticket URL without a scheme", func(e *Event) { e.TicketURL = "example.com/x" }, "ticket_url"),
		c("ticket URL relative to the scheme", func(e *Event) { e.TicketURL = "//example.com/x" }, "ticket_url"),
		c("ticket URL that is a path", func(e *Event) { e.TicketURL = "/ingressos" }, "ticket_url"),
		c("ticket URL without a host", func(e *Event) { e.TicketURL = "http://" }, "ticket_url"),
		c("ticket URL with an empty host and a path", func(e *Event) { e.TicketURL = "https:///x" }, "ticket_url"),
		c("ticket URL with only a port", func(e *Event) { e.TicketURL = "https://:8443/x" }, "ticket_url"),
		c("ticket URL with a space in the host", func(e *Event) { e.TicketURL = "http://exa mple.com" }, "ticket_url"),
		c("ticket URL with a leading space", func(e *Event) { e.TicketURL = " https://example.com" }, "ticket_url"),
		c("ticket URL with a trailing line break", func(e *Event) { e.TicketURL = "https://example.com/\n" }, "ticket_url"),
		c("ticket URL with a space in the path", func(e *Event) { e.TicketURL = "https://a.example/x y" }, "ticket_url"),
		c("ticket URL with a tab in the path", func(e *Event) { e.TicketURL = "https://a.example/x\ty" }, "ticket_url"),
		c("ticket URL with a no-break space in the path", func(e *Event) { e.TicketURL = "https://a.example/x y" }, "ticket_url"),
		c("ticket URL with an ideographic space in the query", func(e *Event) { e.TicketURL = "https://a.example/?q=a　b" }, "ticket_url"),
		c("ticket URL that does not parse", func(e *Event) { e.TicketURL = "https://exa%zzmple.com" }, "ticket_url"),
		c("ticket URL of 2048 characters", func(e *Event) { e.TicketURL = urlBase + rep("a", 2048-len(urlBase)) }),
		c("ticket URL of 2049 characters", func(e *Event) { e.TicketURL = urlBase + rep("a", 2049-len(urlBase)) }, "ticket_url"),

		c("empty style", func(e *Event) { e.Style = "" }, "style"),
		c("style of only spaces", func(e *Event) { e.Style = " " }, "style"),
		c("style of 60 runes", func(e *Event) { e.Style = rep("ç", 60) }),
		c("style of 61 runes", func(e *Event) { e.Style = rep("ç", 61) }, "style"),
		c("style with a line break", func(e *Event) { e.Style = "Tech\nno" }, "style"),
		c("style Outros with a capital is an ordinary style", func(e *Event) { e.Style, e.Substyle = "Outros", "" }),

		c("style outros with its name", func(e *Event) { e.Style, e.Substyle, e.StyleOther = StyleOther, "", "Brega funk" }),
		c("style outros without its name", func(e *Event) { e.Style, e.Substyle = StyleOther, "" }, "style_other"),
		c("style outros with a name of only spaces", func(e *Event) { e.Style, e.Substyle, e.StyleOther = StyleOther, "", "  " }, "style_other"),
		c("style outros with a substyle", func(e *Event) { e.Style, e.StyleOther = StyleOther, "Brega funk" }, "substyle"),
		c("style_other of 40 runes", func(e *Event) { e.Style, e.Substyle, e.StyleOther = StyleOther, "", rep("ç", 40) }),
		c("style_other of 41 runes", func(e *Event) { e.Style, e.Substyle, e.StyleOther = StyleOther, "", rep("ç", 41) }, "style_other"),
		c("style_other with a line break", func(e *Event) { e.Style, e.Substyle, e.StyleOther = StyleOther, "", "Brega\nfunk" }, "style_other"),
		c("style_other with another style", func(e *Event) { e.StyleOther = "Brega funk" }, "style_other"),
		c("style_other of spaces with another style", func(e *Event) { e.StyleOther = " " }, "style_other"),

		c("no substyle", func(e *Event) { e.Substyle = "" }),
		c("substyle of 60 runes", func(e *Event) { e.Substyle = rep("ç", 60) }),
		c("substyle of 61 runes", func(e *Event) { e.Substyle = rep("ç", 61) }, "substyle"),
		c("substyle with a line break", func(e *Event) { e.Substyle = "Melodic\nHouse" }, "substyle"),

		c("empty state", func(e *Event) { e.State = "" }, "state"),
		c("state of one letter", func(e *Event) { e.State = "C" }, "state"),
		c("state of three letters", func(e *Event) { e.State = "CEE" }, "state"),
		c("state in lowercase", func(e *Event) { e.State = "ce" }, "state"),
		c("state in mixed case", func(e *Event) { e.State = "Ce" }, "state"),
		c("state with a digit", func(e *Event) { e.State = "C1" }, "state"),
		c("state with a space", func(e *Event) { e.State = "C " }, "state"),
		c("state with a non-ASCII uppercase letter", func(e *Event) { e.State = "ÇE" }, "state"),
		c("state with a trailing line break", func(e *Event) { e.State = "CE\n" }, "state"),
		c("state PE", func(e *Event) { e.State = "PE" }),

		c("empty city", func(e *Event) { e.City = "" }, "city"),
		c("city of only spaces", func(e *Event) { e.City = "  " }, "city"),
		c("city of 60 runes", func(e *Event) { e.City = rep("ã", 60) }),
		c("city of 61 runes", func(e *Event) { e.City = rep("ã", 61) }, "city"),
		c("city with a line break", func(e *Event) { e.City = "São\nJosé" }, "city"),
		c("city with spaces and accents", func(e *Event) { e.City = "São José do Egito" }),

		c("no venue", func(e *Event) { e.Venue = "" }),
		c("venue of 120 runes", func(e *Event) { e.Venue = rep("ç", 120) }),
		c("venue of 121 runes", func(e *Event) { e.Venue = rep("ç", 121) }, "venue"),
		c("venue with a line break", func(e *Event) { e.Venue = "Casa\nAzul" }, "venue"),

		c("no artists", func(e *Event) { e.Artists = "" }),
		c("artists of 2000 runes", func(e *Event) { e.Artists = rep("ç", 2000) }),
		c("artists of 2001 runes", func(e *Event) { e.Artists = rep("ç", 2001) }, "artists"),
		c("artists with line breaks and tabs", func(e *Event) { e.Artists = "DJ Ana\n\tDJ Bia\n" }),
		c("artists with a carriage return", func(e *Event) { e.Artists = "DJ Ana\r\nDJ Bia" }, "artists"),
		c("artists with a NUL", func(e *Event) { e.Artists = "DJ\x00Ana" }, "artists"),
		c("artists that are not UTF-8", func(e *Event) { e.Artists = "DJ \xffAna" }, "artists"),

		c("no description", func(e *Event) { e.Description = "" }),
		c("description of 4000 runes", func(e *Event) { e.Description = rep("ç", 4000) }),
		c("description of 4001 runes", func(e *Event) { e.Description = rep("ç", 4001) }, "description"),
		c("description with paragraphs", func(e *Event) { e.Description = "Primeira linha.\n\nSegunda\tlinha." }),
		c("description with a bell", func(e *Event) { e.Description = "ding\x07" }, "description"),

		c("every failing field is reported", func(e *Event) {
			*e = Event{
				Name: "x", Type: "?", DurationMin: 0, PriceCents: -1, TicketURL: "javascript:x",
				Style: "", State: "c", City: "", Venue: rep("x", 121), Artists: rep("x", 2001),
				Description: rep("x", 4001),
			}
		}, "collective_id", "name", "type", "starts_at", "created_at", "duration_min", "price_cents", "ticket_url",
			"style", "state", "city", "venue", "artists", "description"),
	}
	for _, typ := range []EventType{EventTypeFesta, EventTypeRave, EventTypeFestival, EventTypeWorkshop, EventTypeFeira} {
		cases = append(cases, c("type "+string(typ), func(e *Event) { e.Type = typ }))
	}
	runChecks(t, baseEvent, Event.Validate, cases)
}

// TestTicketURLProblem pins the rule the form and the store share, and its
// message: any whitespace anywhere in the link is refused with the same text as
// a link that is not an http or https address.
func TestTicketURLProblem(t *testing.T) {
	const msg = "informe um endereço que comece com http:// ou https://"
	tests := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"https", "https://a.example/x?a=1#b", ""},
		{"http with a port", "http://localhost:8080/x", ""},
		{"percent-encoded space", "https://a.example/x%20y", ""},
		{"space in the path", "https://a.example/x y", msg},
		{"space in the host", "http://exa mple.com", msg},
		{"no-break space in the path", "https://a.example/x y", msg},
		{"ideographic space in the query", "https://a.example/?q=a　b", msg},
		{"leading space", " https://a.example", msg},
		{"no scheme", "a.example/x", msg},
		{"javascript scheme", "javascript:alert(1)", msg},
		{"no host", "https:///x", msg},
		{"does not parse", "https://exa%zzmple.com", msg},
		{"tab in the path", "https://a.example/x\ty", MsgControl},
		{"line break", "https://a.example/\n", MsgControl},
		{"not UTF-8", "https://a.example/\xff", MsgInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TicketURLProblem(tc.in); got != tc.want {
				t.Errorf("TicketURLProblem(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
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
