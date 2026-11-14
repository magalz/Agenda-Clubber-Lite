package approvals

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

func TestSentinelErrorsAreDistinct(t *testing.T) {
	all := []error{ErrNotAllowed, ErrDecided, ErrLocked, ErrMaster, ErrLastOwner, ErrSelf}
	for i, a := range all {
		if a == nil {
			t.Fatalf("sentinel %d is nil", i)
		}
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel %d (%v) matches sentinel %d (%v)", i, a, j, b)
			}
		}
	}
}

func TestRoleLabel(t *testing.T) {
	tests := []struct {
		role domain.Role
		want string
	}{
		{domain.RoleOwner, "Administrador"},
		{domain.RoleMember, "Membro"},
		// An unknown role is a bug in the caller; the label shows the stored value
		// instead of passing for a role the person does not have.
		{domain.Role("master"), "master"},
		{domain.Role(""), ""},
	}
	for _, tt := range tests {
		if got := RoleLabel(tt.role); got != tt.want {
			t.Errorf("RoleLabel(%q) = %q, want %q", tt.role, got, tt.want)
		}
	}
}

func TestCheckReason(t *testing.T) {
	runes := func(n int) string { return strings.Repeat("é", n) }
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"empty is allowed", "", "", false},
		{"only space is empty", " \t\n ", "", false},
		{"trimmed", "  motivo  \n", "motivo", false},
		{"CRLF becomes LF", "a\r\nb", "a\nb", false},
		{"lone CR becomes LF", "a\rb", "a\nb", false},
		{"LF stays", "a\nb", "a\nb", false},
		{"tab is allowed inside", "a\tb", "a\tb", false},
		{"accents and emoji are text", "não pode 🎉", "não pode 🎉", false},
		{"limit in runes is allowed", runes(domain.AuditTextMax), runes(domain.AuditTextMax), false},
		{"limit is counted after trimming", "  " + runes(domain.AuditTextMax) + "\n", runes(domain.AuditTextMax), false},
		{"one rune over the limit", runes(domain.AuditTextMax + 1), "", true},
		{"NUL", "a\x00b", "", true},
		{"BEL", "a\x07b", "", true},
		{"ESC", "a\x1bb", "", true},
		{"DEL", "a\x7fb", "", true},
		{"C1 control", "a\u0085b", "", true},
		{"invalid UTF-8", "a\xffb", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CheckReason(tt.in)
			if tt.wantErr {
				requireReasonError(t, err)
				if got != "" {
					t.Errorf("reason = %q with an error, want empty", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if got != tt.want {
				t.Errorf("reason = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCheckReasonMessages(t *testing.T) {
	_, err := CheckReason(strings.Repeat("a", domain.AuditTextMax+1))
	var fe domain.FieldErrors
	if !errors.As(err, &fe) || fe["reason"] != "use no máximo 500 caracteres" {
		t.Errorf("too long: err = %v, want reason: use no máximo 500 caracteres", err)
	}
	_, err = CheckReason("a\x00b")
	if !errors.As(err, &fe) || fe["reason"] == "" || fe["reason"] == "use no máximo 500 caracteres" {
		t.Errorf("control character: err = %v, want its own reason message", err)
	}
}

func TestRequireAdmin(t *testing.T) {
	tests := []struct {
		name  string
		actor domain.User
		want  error
	}{
		{"active admin", domain.User{ID: "u1", IsAdmin: true, Status: domain.UserStatusActive}, nil},
		{"active member", domain.User{ID: "u1", Status: domain.UserStatusActive}, ErrNotAllowed},
		{"disabled admin", domain.User{ID: "u1", IsAdmin: true, Status: domain.UserStatusDisabled}, ErrNotAllowed},
		{"admin awaiting approval", domain.User{ID: "u1", IsAdmin: true, Status: domain.UserStatusPendingApproval}, ErrNotAllowed},
		{"admin awaiting email", domain.User{ID: "u1", IsAdmin: true, Status: domain.UserStatusPendingEmail}, ErrNotAllowed},
		{"nobody", domain.User{}, ErrNotAllowed},
		{"admin without an ID", domain.User{IsAdmin: true, Status: domain.UserStatusActive}, ErrNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireAdmin(tt.actor)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				return
			}
			requireErrorIs(t, err, tt.want)
		})
	}
}

func TestRequireOwner(t *testing.T) {
	s := newTestStore(t)
	creator := insertUser(t, s, "master@agenda.test")
	c := insertCollective(t, s, "Coletivo Dono", creator.ID, func(c *domain.Collective) { c.Status = domain.CollectiveStatusApproved })
	member := func(email string, role domain.Role, status domain.MembershipStatus, mutate ...func(*domain.User)) domain.User {
		u := insertUser(t, s, email, mutate...)
		insertMembership(t, s, domain.Membership{UserID: u.ID, CollectiveID: c.ID, Role: role, Status: status, RequestedAt: testNow})
		return u
	}
	owner := member("adm@agenda.test", domain.RoleOwner, domain.MembershipStatusApproved)
	plain := member("membro@agenda.test", domain.RoleMember, domain.MembershipStatusApproved)
	waiting := member("pedido@agenda.test", domain.RoleOwner, domain.MembershipStatusPending)
	blocked := member("bloqueado@agenda.test", domain.RoleOwner, domain.MembershipStatusApproved,
		func(u *domain.User) { u.Status = domain.UserStatusDisabled })
	outsider := insertUser(t, s, "fora@agenda.test")

	m, err := requireOwner(t.Context(), s.Repos(), owner, c.ID)
	if err != nil {
		t.Fatalf("approved Administrador: err = %v, want nil", err)
	}
	if m.UserID != owner.ID || m.CollectiveID != c.ID || m.Role != domain.RoleOwner {
		t.Errorf("approved Administrador: membership = %+v, want the actor's owner membership", m)
	}

	tests := []struct {
		name  string
		actor domain.User
		id    string
	}{
		{"approved member", plain, c.ID},
		{"pending Administrador", waiting, c.ID},
		{"disabled Administrador", blocked, c.ID},
		{"no membership", outsider, c.ID},
		{"unknown collective", owner, "nope"},
		{"nobody", domain.User{}, c.ID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := requireOwner(t.Context(), s.Repos(), tt.actor, tt.id)
			requireErrorIs(t, err, ErrNotAllowed)
		})
	}
}

func TestWrap(t *testing.T) {
	err := wrap("approve collective: read collective", domain.ErrNotFound)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("wrap hides the cause: %v", err)
	}
	if want := "approvals: approve collective: read collective: "; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("err = %v, want the prefix %q", err, want)
	}

	// A FieldErrors from the store means the flow built an entity the store
	// refuses: a bug, not something the person fixes, so it must not reach the
	// web layer as a form error.
	err = wrap("write audit entry", domain.FieldErrors{"detail": domain.MsgInvalid})
	var fe domain.FieldErrors
	if err == nil || errors.As(err, &fe) {
		t.Fatalf("wrap kept the FieldErrors: %v", err)
	}
	if !strings.Contains(err.Error(), "detail: "+domain.MsgInvalid) {
		t.Errorf("err = %v, want the field text in the message", err)
	}
}

func TestAuditBy(t *testing.T) {
	actor := domain.User{ID: "u1", Name: "Ada Admin", Email: "ada@agenda.test", IsAdmin: true}
	got := auditBy(domain.AuditCollectiveApproved, actor, testNow)
	want := domain.AuditEntry{At: testNow, Action: domain.AuditCollectiveApproved, ActorID: "u1", ActorName: "Ada Admin"}
	if got != want {
		t.Errorf("auditBy = %+v, want %+v", got, want)
	}
}

func TestAudit(t *testing.T) {
	s := newTestStore(t)
	admin := insertAdmin(t, s, "ada@agenda.test")

	e := auditBy(domain.AuditCollectiveApproved, admin, testNow)
	e.CollectiveID, e.CollectiveName, e.Reason = "c1", "Alfa", "motivo"
	if err := audit(t.Context(), s.Repos(), e); err != nil {
		t.Fatalf("audit: %v", err)
	}
	got := onlyAudit(t, auditEntries(t, s))
	if got.Action != e.Action || got.ActorID != admin.ID || got.CollectiveName != "Alfa" || got.Reason != "motivo" || got.ID == "" {
		t.Errorf("stored entry = %+v", got)
	}

	// An entry the store refuses (no actor) is a bug of the flow: an error, and
	// not a FieldErrors the web layer would show as a form error.
	err := audit(t.Context(), s.Repos(), domain.AuditEntry{At: testNow, Action: domain.AuditCollectiveApproved})
	var fe domain.FieldErrors
	if err == nil || errors.As(err, &fe) {
		t.Errorf("audit of an invalid entry: err = %v, want a plain error", err)
	}
}

func TestEnqueue(t *testing.T) {
	s := newTestStore(t)
	data := mail.OwnerJoinRequestData{CollectiveName: "Alfa", UserName: "Bia Souza", Role: "Membro", Link: testOrigin + "/coletivos"}
	now := testNow.Add(time.Hour)

	if err := enqueue(t.Context(), s.Repos(), "ana@agenda.test", mail.TemplateOwnerJoinRequest, data, now); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// A template that takes other data is refused by Render: an error, and nothing
	// is queued for it.
	if err := enqueue(t.Context(), s.Repos(), "ana@agenda.test", mail.TemplateDecision, data, now); err == nil {
		t.Error("enqueue with the wrong data type returned nil")
	}

	m := onlyMessage(t, outboxMessages(t, s))
	if m.To != "ana@agenda.test" || m.Subject == "" || m.HTML == "" {
		t.Errorf("message = %+v", m)
	}
	for _, want := range []string{"Bia Souza", "Alfa", "Membro", data.Link} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, m.Text)
		}
	}
	if !m.NextAttemptAt.Truncate(time.Millisecond).Equal(now) || !m.CreatedAt.Truncate(time.Millisecond).Equal(now) {
		t.Errorf("NextAttemptAt = %v, CreatedAt = %v, want both %v", m.NextAttemptAt, m.CreatedAt, now)
	}
}

func TestDecisionSend(t *testing.T) {
	link := testOrigin + "/coletivos"

	t.Run("one email lists every item", func(t *testing.T) {
		s := newTestStore(t)
		d := newDecision("ana@agenda.test", "Ana Lima")
		d.add("o coletivo Alfa", mail.DecisionApproved, "")
		d.add("seu pedido para entrar em Beta como Membro", mail.DecisionRejected, "já temos membros demais")
		d.add("seu pedido para entrar em Gama como Administrador", mail.DecisionWaiting, "")
		if err := d.send(t.Context(), s.Repos(), link, testNow); err != nil {
			t.Fatalf("send: %v", err)
		}
		m := onlyMessage(t, outboxMessages(t, s))
		if m.To != "ana@agenda.test" {
			t.Errorf("To = %q", m.To)
		}
		for _, want := range []string{
			"Ana Lima",
			"o coletivo Alfa: aprovado",
			"seu pedido para entrar em Beta como Membro: recusado",
			"motivo: já temos membros demais",
			"seu pedido para entrar em Gama como Administrador: aprovado pelo admin; falta um administrador do coletivo",
			link,
		} {
			if !strings.Contains(m.Text, want) {
				t.Errorf("text lacks %q:\n%s", want, m.Text)
			}
		}
		if strings.Count(m.Text, "motivo:") != 1 {
			t.Errorf("only the rejected item has a reason:\n%s", m.Text)
		}
	})

	t.Run("no items queues nothing", func(t *testing.T) {
		s := newTestStore(t)
		d := newDecision("ana@agenda.test", "Ana Lima")
		if err := d.send(t.Context(), s.Repos(), link, testNow); err != nil {
			t.Fatalf("send: %v", err)
		}
		if msgs := outboxMessages(t, s); len(msgs) != 0 {
			t.Errorf("%d emails queued for an empty decision: %+v", len(msgs), msgs)
		}
	})

	t.Run("no recipient queues nothing", func(t *testing.T) {
		// The person was deleted, so there is nobody to tell.
		s := newTestStore(t)
		d := newDecision("", "")
		d.add("o coletivo Alfa", mail.DecisionRejected, "")
		if err := d.send(t.Context(), s.Repos(), link, testNow); err != nil {
			t.Fatalf("send: %v", err)
		}
		if msgs := outboxMessages(t, s); len(msgs) != 0 {
			t.Errorf("%d emails queued without a recipient: %+v", len(msgs), msgs)
		}
	})

	t.Run("an outcome the template does not know is an error", func(t *testing.T) {
		s := newTestStore(t)
		d := newDecision("ana@agenda.test", "Ana Lima")
		d.add("o coletivo Alfa", "maybe", "")
		if err := d.send(t.Context(), s.Repos(), link, testNow); err == nil {
			t.Error("send with an unknown outcome returned nil")
		}
		if msgs := outboxMessages(t, s); len(msgs) != 0 {
			t.Errorf("%d emails queued: %+v", len(msgs), msgs)
		}
	})
}

func TestNotifyAdmins(t *testing.T) {
	s := newTestStore(t)
	insertAdmin(t, s, "ada@agenda.test")
	insertAdmin(t, s, "bob@agenda.test")
	insertUser(t, s, "off@agenda.test", func(u *domain.User) { u.IsAdmin, u.Status = true, domain.UserStatusDisabled })
	insertUser(t, s, "membro@agenda.test")

	data := mail.AdminRequestData{Kind: mail.AdminRequestNewCollective, UserName: "Bia Souza", CollectiveName: "Alfa", Link: testOrigin + "/admin"}
	if err := notifyAdmins(t.Context(), s.Repos(), data, testNow); err != nil {
		t.Fatalf("notifyAdmins: %v", err)
	}
	msgs := outboxMessages(t, s)
	var to []string
	for _, m := range msgs {
		to = append(to, m.To)
		if !strings.Contains(m.Text, "Bia Souza") || !strings.Contains(m.Text, data.Link) {
			t.Errorf("text for %s lacks the request:\n%s", m.To, m.Text)
		}
	}
	slices.Sort(to)
	if want := []string{"ada@agenda.test", "bob@agenda.test"}; !slices.Equal(to, want) {
		t.Errorf("recipients = %v, want %v", to, want)
	}
}

func TestNotifyAdminsWithoutAdmins(t *testing.T) {
	s := newTestStore(t)
	insertUser(t, s, "membro@agenda.test")
	data := mail.AdminRequestData{Kind: mail.AdminRequestNewCollective, UserName: "Bia Souza", CollectiveName: "Alfa", Link: testOrigin + "/admin"}
	if err := notifyAdmins(t.Context(), s.Repos(), data, testNow); err != nil {
		t.Fatalf("notifyAdmins: %v", err)
	}
	if msgs := outboxMessages(t, s); len(msgs) != 0 {
		t.Errorf("%d emails queued with no admin: %+v", len(msgs), msgs)
	}
}

func TestNotifyOwners(t *testing.T) {
	s := newTestStore(t)
	boss := insertUser(t, s, "chefe@agenda.test")
	second := insertUser(t, s, "segunda@agenda.test")
	member := insertUser(t, s, "membro@agenda.test")
	waiting := insertUser(t, s, "espera@agenda.test")
	away := insertUser(t, s, "fora@agenda.test")
	alfa := insertCollective(t, s, "Alfa", boss.ID, func(c *domain.Collective) { c.Status = domain.CollectiveStatusApproved })
	beta := insertCollective(t, s, "Beta", away.ID, func(c *domain.Collective) { c.Status = domain.CollectiveStatusApproved })
	approved := func(u domain.User, c domain.Collective, role domain.Role, status domain.MembershipStatus) {
		insertMembership(t, s, domain.Membership{UserID: u.ID, CollectiveID: c.ID, Role: role, Status: status, RequestedAt: testNow})
	}
	approved(boss, alfa, domain.RoleOwner, domain.MembershipStatusApproved)
	approved(second, alfa, domain.RoleOwner, domain.MembershipStatusApproved)
	approved(member, alfa, domain.RoleMember, domain.MembershipStatusApproved)
	approved(waiting, alfa, domain.RoleOwner, domain.MembershipStatusPending)
	approved(away, beta, domain.RoleOwner, domain.MembershipStatusApproved)

	data := mail.OwnerJoinRequestData{CollectiveName: "Alfa", UserName: "Bia Souza", Role: "Membro", Link: testOrigin + "/coletivos"}
	if err := notifyOwners(t.Context(), s.Repos(), alfa.ID, data, testNow); err != nil {
		t.Fatalf("notifyOwners: %v", err)
	}
	var to []string
	for _, m := range outboxMessages(t, s) {
		to = append(to, m.To)
	}
	slices.Sort(to)
	if want := []string{"chefe@agenda.test", "segunda@agenda.test"}; !slices.Equal(to, want) {
		t.Errorf("recipients = %v, want the approved owners of Alfa only: %v", to, want)
	}
}

func TestWhatStrings(t *testing.T) {
	if whatAccount != "sua conta" {
		t.Errorf("whatAccount = %q", whatAccount)
	}
	for _, tc := range []struct {
		kind domain.CollectiveKind
		want string
	}{
		{domain.CollectiveKindColetivo, "o coletivo Alfa"},
		{domain.CollectiveKindProdutora, "a produtora Alfa"},
	} {
		if got := whatCollective("Alfa", tc.kind); got != tc.want {
			t.Errorf("whatCollective(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
	if got, want := whatJoin("Beta", domain.RoleMember), "seu pedido para entrar em Beta como Membro"; got != want {
		t.Errorf("whatJoin member = %q, want %q", got, want)
	}
	if got, want := whatJoin("Beta", domain.RoleOwner), "seu pedido para entrar em Beta como Administrador"; got != want {
		t.Errorf("whatJoin owner = %q, want %q", got, want)
	}
}
