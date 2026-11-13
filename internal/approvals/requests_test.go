package approvals

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// requestsFixture is a store with two site admins, an approved collective Alfa
// with two Administradores (master, its creator, and owner) and one Membro, and
// a person with an active account who belongs to nothing.
type requestsFixture struct {
	s      domain.Store
	admin  domain.User
	admin2 domain.User
	master domain.User
	owner  domain.User
	member domain.User
	person domain.User
	c      domain.Collective
}

// newRequestsFixture builds the store; mutate adjusts Alfa before it is stored.
func newRequestsFixture(t *testing.T, mutate ...func(*domain.Collective)) requestsFixture {
	t.Helper()
	s := newTestStore(t)
	f := requestsFixture{
		s:      s,
		admin:  insertAdmin(t, s, "ada@agenda.test"),
		admin2: insertAdmin(t, s, "ayla@agenda.test"),
		master: insertUser(t, s, "ana@agenda.test"),
		owner:  insertUser(t, s, "olga@agenda.test"),
		member: insertUser(t, s, "mia@agenda.test"),
		person: insertUser(t, s, "bia@agenda.test"),
	}
	f.c = requestsApprovedCollective(t, s, "Alfa", f, mutate...)
	for _, m := range []struct {
		user domain.User
		role domain.Role
	}{{f.master, domain.RoleOwner}, {f.owner, domain.RoleOwner}, {f.member, domain.RoleMember}} {
		insertMembership(t, s, requestsMembership(f.c.ID, m.user.ID, m.role, domain.MembershipStatusApproved, f))
	}
	return f
}

// requestsApprovedCollective stores an approved collective of kind coletivo,
// approved by the fixture's first admin.
func requestsApprovedCollective(t *testing.T, s domain.Store, name string, f requestsFixture, mutate ...func(*domain.Collective)) domain.Collective {
	t.Helper()
	at := testNow
	return insertCollective(t, s, name, f.master.ID, append([]func(*domain.Collective){func(c *domain.Collective) {
		c.Status, c.ApprovedBy, c.ApprovedAt = domain.CollectiveStatusApproved, f.admin.ID, &at
	}}, mutate...)...)
}

// requestsMembership is a decided membership: with status approved both sides
// are set, otherwise it is as a join request leaves it.
func requestsMembership(collectiveID, userID string, role domain.Role, status domain.MembershipStatus, f requestsFixture) domain.Membership {
	m := domain.Membership{UserID: userID, CollectiveID: collectiveID, Role: role, Status: status, RequestedAt: testNow}
	if status == domain.MembershipStatusPending {
		return m
	}
	at := testNow
	m.DecidedAt = &at
	if status == domain.MembershipStatusApproved {
		m.AdminApprovedBy, m.AdminApprovedAt = f.admin.ID, &at
		m.OwnerApprovedBy, m.OwnerApprovedAt = f.master.ID, &at
	}
	return m
}

// requestsByRecipient maps the queued emails by recipient and fails when one
// person got two.
func requestsByRecipient(t *testing.T, msgs []domain.OutboxMessage) map[string]domain.OutboxMessage {
	t.Helper()
	by := map[string]domain.OutboxMessage{}
	for _, m := range msgs {
		if _, dup := by[m.To]; dup {
			t.Errorf("%s got two emails: %+v", m.To, msgs)
		}
		by[m.To] = m
	}
	return by
}

// requestsRequireEmail fails the test unless the recipient got an email with this
// subject whose text holds every fragment.
func requestsRequireEmail(t *testing.T, by map[string]domain.OutboxMessage, to domain.User, subject string, fragments ...string) {
	t.Helper()
	msg, ok := by[to.Email]
	if !ok {
		t.Errorf("%s got no email", to.Email)
		return
	}
	if msg.Subject != subject {
		t.Errorf("email to %s has the subject %q, want %q", to.Email, msg.Subject, subject)
	}
	for _, want := range fragments {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("email to %s lacks %q:\n%s", to.Email, want, msg.Text)
		}
	}
}

func requestsFields(t *testing.T, err error) domain.FieldErrors {
	t.Helper()
	var fe domain.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want a domain.FieldErrors", err)
	}
	return fe
}

// requestsRequireFields fails the test unless err is exactly these field errors.
func requestsRequireFields(t *testing.T, err error, want domain.FieldErrors) {
	t.Helper()
	if got := requestsFields(t, err); !maps.Equal(got, want) {
		t.Fatalf("field errors %v, want %v", got, want)
	}
}

// requestsRequireKeys fails the test unless err fails exactly these fields, each
// with a message.
func requestsRequireKeys(t *testing.T, err error, keys ...string) {
	t.Helper()
	got := requestsFields(t, err)
	if len(got) != len(keys) {
		t.Fatalf("field errors %v, want only the keys %v", got, keys)
	}
	for _, k := range keys {
		if got[k] == "" {
			t.Fatalf("field errors %v lack a message for %q", got, k)
		}
	}
}

// requestsRequireNoMembership fails the test when the user holds any membership.
func requestsRequireNoMembership(t *testing.T, s domain.Store, u domain.User) {
	t.Helper()
	views, err := s.Repos().Memberships.ListByUser(t.Context(), u.ID)
	if err != nil || len(views) != 0 {
		t.Errorf("memberships of %s = %+v, %v, want none", u.Name, views, err)
	}
}

const (
	requestsMsgNotFound   = "coletivo não encontrado"
	requestsMsgAlready    = "você já faz parte deste coletivo ou já pediu para entrar"
	requestsMsgExists     = "já existe; peça para entrar"
	requestsMsgNameInUse  = "nome já em uso; escolha outro"
	requestsLongName      = "ééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééé" // 81 runes
	requestsMaxRunesName  = "éééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééééé"  // 80 runes
	requestsCombiningOnly = "\u0301\u0301"
)

func TestRequestJoin(t *testing.T) {
	for _, tc := range []struct {
		role  domain.Role
		label string
	}{
		{domain.RoleMember, "Membro"},
		{domain.RoleOwner, "Administrador"},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			f := newRequestsFixture(t)
			now := testNow.Add(time.Hour)

			if err := RequestJoin(t.Context(), f.s, testOrigin, f.person, f.c.ID, tc.role, now); err != nil {
				t.Fatalf("RequestJoin: %v", err)
			}

			// A request that awaits both sides: nothing is approved or decided.
			m := membershipOf(t, f.s, f.person.ID, f.c.ID)
			if m.Role != tc.role || m.Status != domain.MembershipStatusPending || !m.RequestedAt.Truncate(time.Millisecond).Equal(now) {
				t.Errorf("membership = role %q, status %q, requested at %v; want %q, pending, %v", m.Role, m.Status, m.RequestedAt, tc.role, now)
			}
			if m.AdminApprovedBy != "" || m.AdminApprovedAt != nil || m.OwnerApprovedBy != "" || m.OwnerApprovedAt != nil || m.DecidedAt != nil {
				t.Errorf("a new request holds a decision: %+v", m)
			}
			// The collective is written back (the serialization rule) and is the same
			// otherwise.
			want := f.c
			want.UpdatedAt = now
			if got := collectiveOf(t, f.s, f.c.ID); !sameCollective(got, want) {
				t.Errorf("collective = %+v, want %+v", got, want)
			}

			// The Administradores of Alfa and the site admins are told, each once; the
			// Membro is not.
			by := requestsByRecipient(t, outboxMessages(t, f.s))
			if len(by) != 4 {
				t.Errorf("%d people got an email, want the two Administradores and the two admins: %+v", len(by), by)
			}
			for _, u := range []domain.User{f.master, f.owner} {
				requestsRequireEmail(t, by, u, "Novo pedido para entrar em Alfa",
					f.person.Name+" pediu para entrar em Alfa como "+tc.label, testOrigin+"/coletivo/"+f.c.ID)
			}
			for _, u := range []domain.User{f.admin, f.admin2} {
				requestsRequireEmail(t, by, u, "Pedido de entrada: "+f.person.Name+" em Alfa",
					f.person.Name+" pediu para entrar em Alfa como "+tc.label, testOrigin+"/admin")
			}

			wantAudit := domain.AuditEntry{
				At: now, Action: domain.AuditJoinRequested,
				ActorID: f.person.ID, ActorName: f.person.Name,
				UserID: f.person.ID, UserName: f.person.Name,
				CollectiveID: f.c.ID, CollectiveName: "Alfa",
				Detail: tc.label,
			}
			if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, wantAudit) {
				t.Errorf("audit entry = %+v, want %+v", got, wantAudit)
			}
		})
	}
}

func TestRequestJoinRefused(t *testing.T) {
	f := newRequestsFixture(t)
	locked := requestsApprovedCollective(t, f.s, "Locked", f, func(c *domain.Collective) { c.Locked = true })
	pending := insertCollective(t, f.s, "Pending", f.master.ID)
	rejected := insertCollective(t, f.s, "Rejected", f.master.ID, func(c *domain.Collective) { c.Status = domain.CollectiveStatusRejected })

	waiting := insertUser(t, f.s, "wanda@agenda.test")
	insertMembership(t, f.s, requestsMembership(f.c.ID, waiting.ID, domain.RoleMember, domain.MembershipStatusPending, f))
	refused := insertUser(t, f.s, "rita@agenda.test")
	insertMembership(t, f.s, requestsMembership(f.c.ID, refused.ID, domain.RoleMember, domain.MembershipStatusRejected, f))

	withStatus := func(u domain.User, s domain.UserStatus) domain.User { u.Status = s; return u }

	for _, tc := range []struct {
		name         string
		actor        domain.User
		collectiveID string
		role         domain.Role
		err          error              // matched with errors.Is when set
		fields       domain.FieldErrors // exact field errors when set
	}{
		{name: "zero actor", actor: domain.User{}, collectiveID: f.c.ID, role: domain.RoleMember, err: ErrNotAllowed},
		{name: "account awaiting approval", actor: withStatus(f.person, domain.UserStatusPendingApproval), collectiveID: f.c.ID, role: domain.RoleMember, err: ErrNotAllowed},
		{name: "disabled account", actor: withStatus(f.person, domain.UserStatusDisabled), collectiveID: f.c.ID, role: domain.RoleMember, err: ErrNotAllowed},
		{name: "empty role", actor: f.person, collectiveID: f.c.ID, role: "", fields: nil},
		{name: "unknown role", actor: f.person, collectiveID: f.c.ID, role: "master", fields: nil},
		{name: "unknown collective", actor: f.person, collectiveID: "no-such-collective", role: domain.RoleMember, fields: domain.FieldErrors{"collective": requestsMsgNotFound}},
		{name: "empty collective ID", actor: f.person, collectiveID: "", role: domain.RoleMember, fields: domain.FieldErrors{"collective": requestsMsgNotFound}},
		{name: "pending collective", actor: f.person, collectiveID: pending.ID, role: domain.RoleMember, fields: domain.FieldErrors{"collective": requestsMsgNotFound}},
		{name: "rejected collective", actor: f.person, collectiveID: rejected.ID, role: domain.RoleMember, fields: domain.FieldErrors{"collective": requestsMsgNotFound}},
		{name: "locked collective", actor: f.person, collectiveID: locked.ID, role: domain.RoleMember, err: ErrLocked},
		{name: "already an approved member", actor: f.member, collectiveID: f.c.ID, role: domain.RoleOwner, fields: domain.FieldErrors{"collective": requestsMsgAlready}},
		{name: "the master", actor: f.master, collectiveID: f.c.ID, role: domain.RoleOwner, fields: domain.FieldErrors{"collective": requestsMsgAlready}},
		{name: "already asked", actor: waiting, collectiveID: f.c.ID, role: domain.RoleMember, fields: domain.FieldErrors{"collective": requestsMsgAlready}},
		{name: "asked before and was refused", actor: refused, collectiveID: f.c.ID, role: domain.RoleMember, fields: domain.FieldErrors{"collective": requestsMsgAlready}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RequestJoin(t.Context(), f.s, testOrigin, tc.actor, tc.collectiveID, tc.role, testNow.Add(time.Hour))
			switch {
			case tc.err != nil:
				requireErrorIs(t, err, tc.err)
			case tc.fields != nil:
				requestsRequireFields(t, err, tc.fields)
			default:
				requestsRequireKeys(t, err, "role")
			}
		})
	}

	// None of the refusals wrote anything: no membership, no email, no audit entry,
	// and Alfa was not written back.
	requestsRequireNoMembership(t, f.s, f.person)
	if got := collectiveOf(t, f.s, f.c.ID); !sameCollective(got, f.c) {
		t.Errorf("Alfa = %+v, want it unchanged: %+v", got, f.c)
	}
	requireNoSideEffects(t, f.s)
}

// The same person pressing the button several times at once leaves one request
// and one round of notices.
func TestRequestJoinConcurrentSamePerson(t *testing.T) {
	f := newRequestsFixture(t)
	errs := make([]error, 4)

	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() {
			errs[i] = RequestJoin(t.Context(), f.s, testOrigin, f.person, f.c.ID, domain.RoleMember, testNow.Add(time.Hour))
		})
	}
	wg.Wait()

	var done int
	for _, err := range errs {
		if err == nil {
			done++
			continue
		}
		requestsRequireFields(t, err, domain.FieldErrors{"collective": requestsMsgAlready})
	}
	if done != 1 {
		t.Errorf("%d calls created the request, want exactly 1 (errors: %v)", done, errs)
	}
	if n := len(outboxMessages(t, f.s)); n != 4 {
		t.Errorf("%d emails queued, want 4 (two Administradores and two admins, once)", n)
	}
	onlyAudit(t, auditEntries(t, f.s))
}

// Different people asking at once all get in line: the collective is written
// back by each, so the transactions retry on one another and none is lost.
func TestRequestJoinConcurrentPeople(t *testing.T) {
	f := newRequestsFixture(t)
	people := []domain.User{
		insertUser(t, f.s, "p1@agenda.test"), insertUser(t, f.s, "p2@agenda.test"), insertUser(t, f.s, "p3@agenda.test"),
	}
	errs := make([]error, len(people))

	var wg sync.WaitGroup
	for i, p := range people {
		wg.Go(func() {
			errs[i] = RequestJoin(t.Context(), f.s, testOrigin, p, f.c.ID, domain.RoleMember, testNow.Add(time.Hour))
		})
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("request of %s: %v", people[i].Name, err)
		}
		if m := membershipOf(t, f.s, people[i].ID, f.c.ID); m.Status != domain.MembershipStatusPending {
			t.Errorf("membership of %s = %+v, want pending", people[i].Name, m)
		}
	}
	if n := len(outboxMessages(t, f.s)); n != 4*len(people) {
		t.Errorf("%d emails queued, want %d", n, 4*len(people))
	}
	if n := len(auditEntries(t, f.s)); n != len(people) {
		t.Errorf("%d audit entries, want %d", n, len(people))
	}
}

func TestRequestCollective(t *testing.T) {
	for _, kind := range []domain.CollectiveKind{domain.CollectiveKindColetivo, domain.CollectiveKindProdutora} {
		t.Run(string(kind), func(t *testing.T) {
			f := newRequestsFixture(t)
			now := testNow.Add(time.Hour)

			// The name is trimmed; what is inside it stays as typed.
			if err := RequestCollective(t.Context(), f.s, testOrigin, f.person, "  Nova  Casa  ", kind, now); err != nil {
				t.Fatalf("RequestCollective: %v", err)
			}

			c, err := f.s.Repos().Collectives.ByNameKey(t.Context(), domain.NameKey("Nova Casa"))
			if err != nil {
				t.Fatalf("the new collective: %v", err)
			}
			if c.Name != "Nova  Casa" || c.NameKey != "nova casa" || c.Kind != kind || c.Status != domain.CollectiveStatusPending {
				t.Errorf("collective = %q (key %q), kind %q, status %q; want \"Nova  Casa\", \"nova casa\", %q, pending", c.Name, c.NameKey, c.Kind, c.Status, kind)
			}
			if c.CreatedBy != f.person.ID || c.ApprovedBy != "" || c.ApprovedAt != nil || c.EventsBlocked || c.Locked {
				t.Errorf("collective = %+v, want created by the person and nothing decided or blocked", c)
			}
			if !c.CreatedAt.Truncate(time.Millisecond).Equal(now) || !c.UpdatedAt.Truncate(time.Millisecond).Equal(now) {
				t.Errorf("CreatedAt = %v, UpdatedAt = %v, want both %v", c.CreatedAt, c.UpdatedAt, now)
			}

			// The person is its Administrador already (the owner side), waiting for the
			// admin's side, as a signup that creates a collective leaves it.
			m := membershipOf(t, f.s, f.person.ID, c.ID)
			if m.Role != domain.RoleOwner || m.Status != domain.MembershipStatusPending || !m.RequestedAt.Truncate(time.Millisecond).Equal(now) {
				t.Errorf("membership = role %q, status %q, requested at %v; want owner, pending, %v", m.Role, m.Status, m.RequestedAt, now)
			}
			if m.OwnerApprovedBy != f.person.ID || !sameTime(m.OwnerApprovedAt, now) {
				t.Errorf("owner side = %q at %v, want the person's own at %v", m.OwnerApprovedBy, m.OwnerApprovedAt, now)
			}
			if m.AdminApprovedBy != "" || m.AdminApprovedAt != nil || m.DecidedAt != nil {
				t.Errorf("the admin side is already decided: %+v", m)
			}
			if ok, err := f.s.Repos().Memberships.UserCanCreateEvents(t.Context(), f.person.ID); err != nil || ok {
				t.Errorf("UserCanCreateEvents = %v, %v, want false until the admin approves", ok, err)
			}

			by := requestsByRecipient(t, outboxMessages(t, f.s))
			if len(by) != 2 {
				t.Errorf("%d people got an email, want the two admins: %+v", len(by), by)
			}
			for _, u := range []domain.User{f.admin, f.admin2} {
				requestsRequireEmail(t, by, u, "Coletivo novo para aprovar: Nova  Casa",
					f.person.Name+" pediu para criar Nova  Casa", testOrigin+"/admin")
			}

			want := domain.AuditEntry{
				At: now, Action: domain.AuditCollectiveRequested,
				ActorID: f.person.ID, ActorName: f.person.Name,
				UserID: f.person.ID, UserName: f.person.Name,
				CollectiveID: c.ID, CollectiveName: "Nova  Casa",
			}
			if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, want) {
				t.Errorf("audit entry = %+v, want %+v", got, want)
			}
		})
	}
}

func TestRequestCollectiveBadInput(t *testing.T) {
	f := newRequestsFixture(t)
	for _, tc := range []struct {
		name       string
		collective string
		kind       domain.CollectiveKind
		keys       []string
	}{
		{"empty name", "", domain.CollectiveKindColetivo, []string{"name"}},
		{"blank name", " \t ", domain.CollectiveKindColetivo, []string{"name"}},
		{"one letter", "A", domain.CollectiveKindColetivo, []string{"name"}},
		{"too long", requestsLongName, domain.CollectiveKindColetivo, []string{"name"}},
		{"line break", "Nova\nCasa", domain.CollectiveKindColetivo, []string{"name"}},
		{"control character", "Nova\x00Casa", domain.CollectiveKindColetivo, []string{"name"}},
		{"invalid UTF-8", "Nova\xffCasa", domain.CollectiveKindColetivo, []string{"name"}},
		{"only combining marks", requestsCombiningOnly, domain.CollectiveKindColetivo, []string{"name"}},
		{"empty kind", "Nova Casa", "", []string{"kind"}},
		{"unknown kind", "Nova Casa", "festa", []string{"kind"}},
		{"name and kind", "", "festa", []string{"name", "kind"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RequestCollective(t.Context(), f.s, testOrigin, f.person, tc.collective, tc.kind, testNow.Add(time.Hour))
			requestsRequireKeys(t, err, tc.keys...)
		})
	}

	// Nothing was created or queued by any of them.
	requestsRequireNoMembership(t, f.s, f.person)
	if cs, err := f.s.Repos().Collectives.ListByStatus(t.Context(), domain.CollectiveStatusPending, 10); err != nil || len(cs) != 0 {
		t.Errorf("pending collectives = %+v, %v, want none", cs, err)
	}
	requireNoSideEffects(t, f.s)
}

// The limits are in runes, not bytes, and are inclusive.
func TestRequestCollectiveNameLength(t *testing.T) {
	f := newRequestsFixture(t)
	for _, name := range []string{"Ab", requestsMaxRunesName} {
		if err := RequestCollective(t.Context(), f.s, testOrigin, f.person, name, domain.CollectiveKindColetivo, testNow.Add(time.Hour)); err != nil {
			t.Errorf("RequestCollective(%q): %v", name, err)
		}
	}
}

func TestRequestCollectiveRefusedActor(t *testing.T) {
	f := newRequestsFixture(t)
	withStatus := func(u domain.User, s domain.UserStatus) domain.User { u.Status = s; return u }
	for _, tc := range []struct {
		name  string
		actor domain.User
		kind  domain.CollectiveKind
		coll  string
	}{
		{"zero actor", domain.User{}, domain.CollectiveKindColetivo, "Nova Casa"},
		{"account awaiting approval", withStatus(f.person, domain.UserStatusPendingApproval), domain.CollectiveKindColetivo, "Nova Casa"},
		{"disabled account", withStatus(f.person, domain.UserStatusDisabled), domain.CollectiveKindColetivo, "Nova Casa"},
		// The actor comes first: a refused account learns nothing about the input.
		{"refused account and bad input", withStatus(f.person, domain.UserStatusDisabled), "festa", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requireErrorIs(t, RequestCollective(t.Context(), f.s, testOrigin, tc.actor, tc.coll, tc.kind, testNow.Add(time.Hour)), ErrNotAllowed)
		})
	}
	requestsRequireNoMembership(t, f.s, f.person)
	requireNoSideEffects(t, f.s)
}

func TestRequestCollectiveNameTaken(t *testing.T) {
	f := newRequestsFixture(t)
	insertCollective(t, f.s, "Beta", f.master.ID)
	insertCollective(t, f.s, "Gama", f.master.ID, func(c *domain.Collective) { c.Status = domain.CollectiveStatusRejected })
	requestsApprovedCollective(t, f.s, "Delta", f, func(c *domain.Collective) { c.Kind = domain.CollectiveKindProdutora })

	for _, tc := range []struct {
		name string
		kind domain.CollectiveKind
		want string
	}{
		// An approved collective can be joined, so the person is sent there, whatever
		// the kind and however the name is written.
		{"Alfa", domain.CollectiveKindColetivo, requestsMsgExists},
		{"  ALFA ", domain.CollectiveKindProdutora, requestsMsgExists},
		{"Álfa", domain.CollectiveKindColetivo, requestsMsgExists},
		{"Delta", domain.CollectiveKindColetivo, requestsMsgExists},
		// One that is not approved cannot, so the name is simply taken.
		{"Beta", domain.CollectiveKindColetivo, requestsMsgNameInUse},
		{"beta", domain.CollectiveKindProdutora, requestsMsgNameInUse},
		{"Gama", domain.CollectiveKindColetivo, requestsMsgNameInUse},
	} {
		t.Run(tc.name+"/"+string(tc.kind), func(t *testing.T) {
			err := RequestCollective(t.Context(), f.s, testOrigin, f.person, tc.name, tc.kind, testNow.Add(time.Hour))
			requestsRequireFields(t, err, domain.FieldErrors{"name": tc.want})
		})
	}

	requestsRequireNoMembership(t, f.s, f.person)
	requireNoSideEffects(t, f.s)
}

// Several people asking for the same new name at once: one gets it, the others are
// told the name is taken (the unique name key catches the ones that read before
// the winner wrote).
func TestRequestCollectiveConcurrentSameName(t *testing.T) {
	f := newRequestsFixture(t)
	people := []domain.User{
		insertUser(t, f.s, "p1@agenda.test"), insertUser(t, f.s, "p2@agenda.test"),
		insertUser(t, f.s, "p3@agenda.test"), insertUser(t, f.s, "p4@agenda.test"),
	}
	errs := make([]error, len(people))

	var wg sync.WaitGroup
	for i, p := range people {
		wg.Go(func() {
			errs[i] = RequestCollective(t.Context(), f.s, testOrigin, p, "Nova Casa", domain.CollectiveKindColetivo, testNow.Add(time.Hour))
		})
	}
	wg.Wait()

	var done int
	for _, err := range errs {
		if err == nil {
			done++
			continue
		}
		requestsRequireFields(t, err, domain.FieldErrors{"name": requestsMsgNameInUse})
	}
	if done != 1 {
		t.Errorf("%d calls created the collective, want exactly 1 (errors: %v)", done, errs)
	}
	if n := len(outboxMessages(t, f.s)); n != 2 {
		t.Errorf("%d emails queued, want 2 (the two admins, once)", n)
	}
	onlyAudit(t, auditEntries(t, f.s))
}

func TestRenameCollective(t *testing.T) {
	for _, who := range []string{"master", "owner"} {
		t.Run(who, func(t *testing.T) {
			f := newRequestsFixture(t, func(c *domain.Collective) { c.EventsBlocked = true })
			actor := map[string]domain.User{"master": f.master, "owner": f.owner}[who]
			now := testNow.Add(time.Hour)

			if err := RenameCollective(t.Context(), f.s, testOrigin, actor, f.c.ID, "  Alfa Prime ", now); err != nil {
				t.Fatalf("RenameCollective: %v", err)
			}

			// Applied at once, and nothing else about the collective changed.
			want := f.c
			want.Name, want.NameKey, want.UpdatedAt = "Alfa Prime", "alfa prime", now
			if got := collectiveOf(t, f.s, f.c.ID); !sameCollective(got, want) {
				t.Errorf("collective = %+v, want %+v", got, want)
			}

			// The site admins are told, with both names; the people of Alfa are not.
			by := requestsByRecipient(t, outboxMessages(t, f.s))
			if len(by) != 2 {
				t.Errorf("%d people got an email, want the two admins: %+v", len(by), by)
			}
			for _, u := range []domain.User{f.admin, f.admin2} {
				requestsRequireEmail(t, by, u, "Coletivo renomeado: Alfa → Alfa Prime",
					actor.Name+" mudou o nome de Alfa para Alfa Prime", testOrigin+"/admin/coletivos")
			}

			// The snapshot of the collective is the new name; the detail holds both.
			wantAudit := domain.AuditEntry{
				At: now, Action: domain.AuditCollectiveRenamed,
				ActorID: actor.ID, ActorName: actor.Name,
				CollectiveID: f.c.ID, CollectiveName: "Alfa Prime",
				Detail: "Alfa → Alfa Prime",
			}
			if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, wantAudit) {
				t.Errorf("audit entry = %+v, want %+v", got, wantAudit)
			}
		})
	}
}

// A change of case or spacing keeps the name key, so it needs no free name, and
// it is still a rename: stored, mailed and audited.
func TestRenameCollectiveSameKey(t *testing.T) {
	f := newRequestsFixture(t)
	now := testNow.Add(time.Hour)

	if err := RenameCollective(t.Context(), f.s, testOrigin, f.master, f.c.ID, "ALFA", now); err != nil {
		t.Fatalf("RenameCollective: %v", err)
	}

	c := collectiveOf(t, f.s, f.c.ID)
	if c.Name != "ALFA" || c.NameKey != "alfa" || !c.UpdatedAt.Truncate(time.Millisecond).Equal(now) {
		t.Errorf("collective = %q (key %q) updated at %v, want \"ALFA\", \"alfa\", %v", c.Name, c.NameKey, c.UpdatedAt, now)
	}
	if n := len(outboxMessages(t, f.s)); n != 2 {
		t.Errorf("%d emails queued, want 2", n)
	}
	if got := onlyAudit(t, auditEntries(t, f.s)); got.Detail != "Alfa → ALFA" {
		t.Errorf("audit detail = %q, want %q", got.Detail, "Alfa → ALFA")
	}
}

// Asking for the name the collective already has (spaces around it aside) does
// nothing: no write, no email, no audit entry.
func TestRenameCollectiveNoChange(t *testing.T) {
	f := newRequestsFixture(t)
	for _, name := range []string{"Alfa", "  Alfa  "} {
		if err := RenameCollective(t.Context(), f.s, testOrigin, f.master, f.c.ID, name, testNow.Add(time.Hour)); err != nil {
			t.Errorf("RenameCollective(%q): %v", name, err)
		}
	}
	if got := collectiveOf(t, f.s, f.c.ID); !sameCollective(got, f.c) {
		t.Errorf("collective = %+v, want it unchanged: %+v", got, f.c)
	}
	requireNoSideEffects(t, f.s)
}

func TestRenameCollectiveRefused(t *testing.T) {
	f := newRequestsFixture(t)
	insertCollective(t, f.s, "Beta", f.master.ID)
	locked := requestsApprovedCollective(t, f.s, "Locked", f, func(c *domain.Collective) { c.Locked = true })
	insertMembership(t, f.s, requestsMembership(locked.ID, f.master.ID, domain.RoleOwner, domain.MembershipStatusApproved, f))
	// An approved Administrador of a collective that is not approved (not what the
	// flows leave, but the status gate is the rename's own).
	pending := insertCollective(t, f.s, "Pending", f.master.ID)
	insertMembership(t, f.s, requestsMembership(pending.ID, f.master.ID, domain.RoleOwner, domain.MembershipStatusApproved, f))

	waiting := insertUser(t, f.s, "wanda@agenda.test")
	insertMembership(t, f.s, requestsMembership(f.c.ID, waiting.ID, domain.RoleOwner, domain.MembershipStatusPending, f))
	refused := insertUser(t, f.s, "rita@agenda.test")
	insertMembership(t, f.s, requestsMembership(f.c.ID, refused.ID, domain.RoleOwner, domain.MembershipStatusRejected, f))

	withStatus := func(u domain.User, s domain.UserStatus) domain.User { u.Status = s; return u }

	for _, tc := range []struct {
		name         string
		actor        domain.User
		collectiveID string
		newName      string
		err          error // matched with errors.Is when set
		keys         []string
		fields       domain.FieldErrors
	}{
		{name: "zero actor", actor: domain.User{}, collectiveID: f.c.ID, newName: "Alfa Prime", err: ErrNotAllowed},
		{name: "disabled Administrador", actor: withStatus(f.master, domain.UserStatusDisabled), collectiveID: f.c.ID, newName: "Alfa Prime", err: ErrNotAllowed},
		{name: "not a member", actor: f.person, collectiveID: f.c.ID, newName: "Alfa Prime", err: ErrNotAllowed},
		{name: "a Membro", actor: f.member, collectiveID: f.c.ID, newName: "Alfa Prime", err: ErrNotAllowed},
		{name: "site admin who is not an Administrador of it", actor: f.admin, collectiveID: f.c.ID, newName: "Alfa Prime", err: ErrNotAllowed},
		{name: "Administrador still awaiting approval", actor: waiting, collectiveID: f.c.ID, newName: "Alfa Prime", err: ErrNotAllowed},
		{name: "Administrador whose request was refused", actor: refused, collectiveID: f.c.ID, newName: "Alfa Prime", err: ErrNotAllowed},
		// The caller learns no more than "not yours": an unknown collective is the same answer.
		{name: "unknown collective", actor: f.master, collectiveID: "no-such-collective", newName: "Alfa Prime", err: ErrNotAllowed},
		{name: "collective not approved", actor: f.master, collectiveID: pending.ID, newName: "Pending Prime", err: ErrNotAllowed},
		{name: "locked collective", actor: f.master, collectiveID: locked.ID, newName: "Locked Prime", err: ErrLocked},
		{name: "name taken by another collective", actor: f.master, collectiveID: f.c.ID, newName: "beta", fields: domain.FieldErrors{"name": requestsMsgNameInUse}},
		{name: "empty name", actor: f.master, collectiveID: f.c.ID, newName: " ", keys: []string{"name"}},
		{name: "one letter", actor: f.master, collectiveID: f.c.ID, newName: "A", keys: []string{"name"}},
		{name: "too long", actor: f.master, collectiveID: f.c.ID, newName: requestsLongName, keys: []string{"name"}},
		{name: "line break", actor: f.master, collectiveID: f.c.ID, newName: "Alfa\nPrime", keys: []string{"name"}},
		{name: "only combining marks", actor: f.master, collectiveID: f.c.ID, newName: requestsCombiningOnly, keys: []string{"name"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := RenameCollective(t.Context(), f.s, testOrigin, tc.actor, tc.collectiveID, tc.newName, testNow.Add(time.Hour))
			switch {
			case tc.err != nil:
				requireErrorIs(t, err, tc.err)
				if !isNoneOf(err, domain.ErrNotFound) {
					t.Errorf("err = %v, want it not to match ErrNotFound", err)
				}
			case tc.fields != nil:
				requestsRequireFields(t, err, tc.fields)
			default:
				requestsRequireKeys(t, err, tc.keys...)
			}
		})
	}

	// Every collective is as it was, and nothing was mailed or audited.
	for _, c := range []domain.Collective{f.c, locked, pending} {
		if got := collectiveOf(t, f.s, c.ID); !sameCollective(got, c) {
			t.Errorf("%s = %+v, want it unchanged: %+v", c.Name, got, c)
		}
	}
	requireNoSideEffects(t, f.s)
}

// Two Administradores renaming at once: both renames apply, one after the other,
// and the second reads the name the first left, so the audit log chains.
func TestRenameCollectiveConcurrent(t *testing.T) {
	f := newRequestsFixture(t)
	errs := make([]error, 2)

	var wg sync.WaitGroup
	for i, tc := range []struct {
		actor domain.User
		name  string
	}{{f.master, "Alfa Um"}, {f.owner, "Alfa Dois"}} {
		wg.Go(func() {
			errs[i] = RenameCollective(t.Context(), f.s, testOrigin, tc.actor, f.c.ID, tc.name, testNow.Add(time.Hour))
		})
	}
	wg.Wait()

	for _, err := range errs {
		if err != nil {
			t.Errorf("a rename failed: %v", err)
		}
	}
	var details []string
	for _, e := range auditEntries(t, f.s) {
		details = append(details, e.Detail)
	}
	slices.Sort(details)
	oneThenTwo := []string{"Alfa Um → Alfa Dois", "Alfa → Alfa Um"}
	twoThenOne := []string{"Alfa Dois → Alfa Um", "Alfa → Alfa Dois"}
	slices.Sort(oneThenTwo)
	slices.Sort(twoThenOne)
	var wantName string
	switch {
	case slices.Equal(details, oneThenTwo):
		wantName = "Alfa Dois"
	case slices.Equal(details, twoThenOne):
		wantName = "Alfa Um"
	default:
		t.Fatalf("audit details = %q, want the two renames in sequence", details)
	}
	if got := collectiveOf(t, f.s, f.c.ID).Name; got != wantName {
		t.Errorf("name = %q, want %q, the last rename", got, wantName)
	}
}
