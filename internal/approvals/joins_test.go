package approvals

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// joinFixture is a store with one cast of people and four collectives. The cast:
// the site admin, the Master of every collective (Ana, creator and Administrador),
// a second Administrador, a Membro, and five people whose request to join waits
// in a state of its own. The collectives: Alfa (approved, the one most tests
// use), Gama (pending, yet with an approved Administrador, which a real flow never
// leaves but which makes the "collective is not approved" checks reachable),
// Lambda (approved and locked) and Beta (approved, with a single Administrador who
// belongs to none of the others). Alfa, Gama and Lambda hold the same memberships.
type joinFixture struct {
	s     domain.Store
	admin domain.User

	master, coAdmin, member domain.User // approved: Administrador, Administrador, Membro

	requester domain.User // pending Membro request, nothing decided
	adminDone domain.User // pending Membro request, the admin side decided
	ownerDone domain.User // pending Membro request, the owner side decided
	hopeful   domain.User // pending Administrador request, nothing decided
	inactive  domain.User // pending Membro request of an account that awaits approval

	stranger domain.User // the Administrador of Beta only
	outsider domain.User // belongs to no collective

	c, pending, locked, beta domain.Collective
	gone                     string // the ID of a collective that existed and was deleted
}

func newJoinFixture(t *testing.T) joinFixture {
	t.Helper()
	s := newTestStore(t)
	f := joinFixture{s: s}
	f.admin = insertAdmin(t, s, "ada@agenda.test")
	f.master = insertUser(t, s, "ana@agenda.test")
	f.coAdmin = insertUser(t, s, "cora@agenda.test")
	f.member = insertUser(t, s, "mia@agenda.test")
	f.requester = insertUser(t, s, "rita@agenda.test")
	f.adminDone = insertUser(t, s, "ari@agenda.test")
	f.ownerDone = insertUser(t, s, "omar@agenda.test")
	f.hopeful = insertUser(t, s, "hugo@agenda.test")
	f.inactive = insertUser(t, s, "ian@agenda.test", func(u *domain.User) { u.Status = domain.UserStatusPendingApproval })
	f.stranger = insertUser(t, s, "sam@agenda.test")
	f.outsider = insertUser(t, s, "oto@agenda.test")

	f.c = f.stock(t, "Alfa")
	f.pending = f.stock(t, "Gama", func(c *domain.Collective) {
		c.Status, c.ApprovedBy, c.ApprovedAt = domain.CollectiveStatusPending, "", nil
	})
	f.locked = f.stock(t, "Lambda", func(c *domain.Collective) { c.Locked = true })
	f.beta = insertCollective(t, s, "Beta", f.stranger.ID, joinApproved(f.admin))
	insertMembership(t, s, joinMembership(f.stranger, f.beta, domain.RoleOwner, f.admin, f.stranger))

	gone := insertCollective(t, s, "Delta", f.master.ID)
	if err := s.Repos().Collectives.Delete(t.Context(), gone.ID); err != nil {
		t.Fatalf("delete collective: %v", err)
	}
	f.gone = gone.ID
	return f
}

// stock stores an approved collective created by the Master, adjusted by mutate,
// and the memberships of the cast in it.
func (f joinFixture) stock(t *testing.T, name string, mutate ...func(*domain.Collective)) domain.Collective {
	t.Helper()
	c := insertCollective(t, f.s, name, f.master.ID, append([]func(*domain.Collective){joinApproved(f.admin)}, mutate...)...)
	none := domain.User{}
	for _, m := range []domain.Membership{
		joinMembership(f.master, c, domain.RoleOwner, f.admin, f.master),
		joinMembership(f.coAdmin, c, domain.RoleOwner, f.admin, f.master),
		joinMembership(f.member, c, domain.RoleMember, f.admin, f.master),
		joinMembership(f.requester, c, domain.RoleMember, none, none),
		joinMembership(f.adminDone, c, domain.RoleMember, f.admin, none),
		joinMembership(f.ownerDone, c, domain.RoleMember, none, f.master),
		joinMembership(f.hopeful, c, domain.RoleOwner, none, none),
		joinMembership(f.inactive, c, domain.RoleMember, none, none),
	} {
		insertMembership(t, f.s, m)
	}
	return c
}

// snapshot describes every collective of the fixture and every one of their
// memberships, so a test can tell that a refused call changed nothing.
func (f joinFixture) snapshot(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, id := range []string{f.c.ID, f.pending.ID, f.locked.ID, f.beta.ID} {
		c := collectiveOf(t, f.s, id)
		fmt.Fprintf(&b, "collective %s %q %s locked=%v events_blocked=%v updated=%d\n", c.ID, c.Name, c.Status, c.Locked, c.EventsBlocked, c.UpdatedAt.UnixMilli())
		views, err := f.s.Repos().Memberships.ListByCollective(t.Context(), id)
		if err != nil {
			t.Fatalf("list members of %s: %v", id, err)
		}
		for _, v := range views {
			fmt.Fprintf(&b, "  %s %s %s admin=%s@%d owner=%s@%d decided=%d\n", v.UserID, v.Role, v.Status,
				v.AdminApprovedBy, joinMillis(v.AdminApprovedAt), v.OwnerApprovedBy, joinMillis(v.OwnerApprovedAt), joinMillis(v.DecidedAt))
		}
	}
	return b.String()
}

func joinMillis(at *time.Time) int64 {
	if at == nil {
		return -1
	}
	return at.UnixMilli()
}

func joinTime(at *time.Time) time.Time {
	if at == nil {
		return time.Time{}
	}
	return *at
}

// joinApproved makes the collective an approved one, approved by approver.
func joinApproved(approver domain.User) func(*domain.Collective) {
	return func(c *domain.Collective) {
		at := testNow
		c.Status, c.ApprovedBy, c.ApprovedAt = domain.CollectiveStatusApproved, approver.ID, &at
	}
}

// joinMembership returns the membership of u in c with the admin side decided by
// admin and the owner side decided by owner; a zero User leaves that side open.
// With both sides decided it is approved.
func joinMembership(u domain.User, c domain.Collective, role domain.Role, admin, owner domain.User) domain.Membership {
	m := domain.Membership{UserID: u.ID, CollectiveID: c.ID, Role: role, Status: domain.MembershipStatusPending, RequestedAt: testNow}
	at := testNow
	if admin.ID != "" {
		m.AdminApprovedBy, m.AdminApprovedAt = admin.ID, &at
	}
	if owner.ID != "" {
		m.OwnerApprovedBy, m.OwnerApprovedAt = owner.ID, &at
	}
	if admin.ID != "" && owner.ID != "" {
		m.Status, m.DecidedAt = domain.MembershipStatusApproved, &at
	}
	return m
}

// joinWant is what a test expects a membership to hold; a zero time means unset.
type joinWant struct {
	role                        domain.Role
	status                      domain.MembershipStatus
	admin, owner                string // who decided each side
	adminAt, ownerAt, decidedAt time.Time
}

func joinWantOf(m domain.Membership) joinWant {
	return joinWant{m.Role, m.Status, m.AdminApprovedBy, m.OwnerApprovedBy, joinTime(m.AdminApprovedAt), joinTime(m.OwnerApprovedAt), joinTime(m.DecidedAt)}
}

func joinSameTime(got *time.Time, want time.Time) bool {
	if want.IsZero() {
		return got == nil
	}
	return sameTime(got, want)
}

func joinRequireMembership(t *testing.T, got domain.Membership, w joinWant) {
	t.Helper()
	if got.Role != w.role || got.Status != w.status {
		t.Errorf("role %q, status %q; want %q, %q", got.Role, got.Status, w.role, w.status)
	}
	if got.AdminApprovedBy != w.admin || !joinSameTime(got.AdminApprovedAt, w.adminAt) {
		t.Errorf("admin side = %q at %v; want %q at %v", got.AdminApprovedBy, got.AdminApprovedAt, w.admin, w.adminAt)
	}
	if got.OwnerApprovedBy != w.owner || !joinSameTime(got.OwnerApprovedAt, w.ownerAt) {
		t.Errorf("owner side = %q at %v; want %q at %v", got.OwnerApprovedBy, got.OwnerApprovedAt, w.owner, w.ownerAt)
	}
	if !joinSameTime(got.DecidedAt, w.decidedAt) {
		t.Errorf("DecidedAt = %v, want %v", got.DecidedAt, w.decidedAt)
	}
	if !got.RequestedAt.Truncate(time.Millisecond).Equal(testNow) {
		t.Errorf("RequestedAt = %v, want %v: it never changes", got.RequestedAt, testNow)
	}
}

// joinRequireTouched fails the test unless the collective is what it was except
// for UpdatedAt, which every write path moves to now (it is what makes two
// transactions on one collective conflict).
func joinRequireTouched(t *testing.T, s domain.Store, before domain.Collective, now time.Time) {
	t.Helper()
	want := before
	want.UpdatedAt = now
	if got := collectiveOf(t, s, before.ID); !sameCollective(got, want) {
		t.Errorf("collective = %+v, want %+v: only UpdatedAt moves, to %v", got, want, now)
	}
}

// joinRequireMembers fails the test unless got lists exactly want, in order.
func joinRequireMembers(t *testing.T, what string, got []domain.MemberView, want ...domain.User) {
	t.Helper()
	var gotIDs, wantIDs, gotNames, wantNames []string
	for _, v := range got {
		gotIDs, gotNames = append(gotIDs, v.UserID), append(gotNames, v.UserName)
	}
	for _, u := range want {
		wantIDs, wantNames = append(wantIDs, u.ID), append(wantNames, u.Name)
	}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Errorf("%s = %q, want %q", what, gotNames, wantNames)
	}
}

// joinEmailKinds counts the queued emails that report the request described by
// what as approved and as waiting for an Administrador. The waiting text starts
// like the approval, so the two are told apart by what follows.
func joinEmailKinds(msgs []domain.OutboxMessage, what string) (approved, waiting int) {
	for _, m := range msgs {
		switch {
		case strings.Contains(m.Text, what+": aprovado pelo admin; falta um administrador do coletivo"):
			waiting++
		case strings.Contains(m.Text, what+": aprovado"):
			approved++
		}
	}
	return approved, waiting
}

// joinActiveAdministradores counts the approved Administradores of the collective
// whose account is active, the people who can still run it.
func joinActiveAdministradores(t *testing.T, s domain.Store, collectiveID string) int {
	t.Helper()
	views, err := s.Repos().Memberships.ListByCollective(t.Context(), collectiveID)
	if err != nil {
		t.Fatalf("list members: %v", err)
	}
	n := 0
	for _, v := range views {
		if v.Role == domain.RoleOwner && v.Status == domain.MembershipStatusApproved && v.UserStatus == domain.UserStatusActive {
			n++
		}
	}
	return n
}

// joinRefusal is a call that must fail with want and change nothing.
type joinRefusal struct {
	name string
	call func() error
	want error
}

// joinRequireRefused runs each call against the fixture and checks the error, that
// the fixture is as it was, and, once all ran, that no email was queued and no
// audit entry written.
func joinRequireRefused(t *testing.T, f joinFixture, tests []joinRefusal) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := f.snapshot(t)
			requireErrorIs(t, tt.call(), tt.want)
			if after := f.snapshot(t); after != before {
				t.Errorf("a refused call changed the fixture:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
	requireNoSideEffects(t, f.s)
}

func joinDisabled(u domain.User) domain.User {
	u.Status = domain.UserStatusDisabled
	return u
}

func TestAdminApproveJoin(t *testing.T) {
	now := testNow.Add(time.Hour)
	tests := []struct {
		name  string
		user  func(f joinFixture) domain.User
		label string // the role as the person reads it
		done  bool   // the owner side was decided, so this completes the approval
	}{
		{"Membro, owner side open", func(f joinFixture) domain.User { return f.requester }, "Membro", false},
		{"Membro, owner side decided", func(f joinFixture) domain.User { return f.ownerDone }, "Membro", true},
		{"Administrador, owner side open", func(f joinFixture) domain.User { return f.hopeful }, "Administrador", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newJoinFixture(t)
			u := tt.user(f)
			before := membershipOf(t, f.s, u.ID, f.c.ID)

			if err := AdminApproveJoin(t.Context(), f.s, testOrigin, f.admin, u.ID, f.c.ID, now); err != nil {
				t.Fatalf("AdminApproveJoin: %v", err)
			}

			w := joinWantOf(before)
			w.admin, w.adminAt = f.admin.ID, now
			if tt.done {
				w.status, w.decidedAt = domain.MembershipStatusApproved, now
			}
			joinRequireMembership(t, membershipOf(t, f.s, u.ID, f.c.ID), w)
			joinRequireTouched(t, f.s, f.c, now)

			msg := onlyMessage(t, outboxMessages(t, f.s))
			what := "seu pedido para entrar em Alfa como " + tt.label
			if msg.To != u.Email {
				t.Errorf("email to %q, want %q", msg.To, u.Email)
			}
			for _, want := range []string{u.Name, what + ": aprovado", testOrigin + "/coletivos"} {
				if !strings.Contains(msg.Text, want) {
					t.Errorf("email lacks %q:\n%s", want, msg.Text)
				}
			}
			if got := strings.Contains(msg.Text, "falta um administrador do coletivo"); got == tt.done {
				t.Errorf("email says waiting for an Administrador = %v, want %v:\n%s", got, !tt.done, msg.Text)
			}
			if strings.Contains(msg.Text, "motivo:") {
				t.Errorf("an approval has no reason:\n%s", msg.Text)
			}

			want := domain.AuditEntry{
				At: now, Action: domain.AuditJoinAdminApproved,
				ActorID: f.admin.ID, ActorName: f.admin.Name,
				UserID: u.ID, UserName: u.Name,
				CollectiveID: f.c.ID, CollectiveName: "Alfa", Detail: tt.label,
			}
			if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, want) {
				t.Errorf("audit entry = %+v, want %+v", got, want)
			}
		})
	}
}

func TestOwnerApproveJoin(t *testing.T) {
	now := testNow.Add(time.Hour)
	tests := []struct {
		name  string
		user  func(f joinFixture) domain.User
		label string
		done  bool // the admin side was decided, so this completes the approval
	}{
		{"Membro, admin side open", func(f joinFixture) domain.User { return f.requester }, "Membro", false},
		{"Membro, admin side decided", func(f joinFixture) domain.User { return f.adminDone }, "Membro", true},
		{"Administrador, admin side open", func(f joinFixture) domain.User { return f.hopeful }, "Administrador", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newJoinFixture(t)
			u := tt.user(f)
			before := membershipOf(t, f.s, u.ID, f.c.ID)

			if err := OwnerApproveJoin(t.Context(), f.s, testOrigin, f.master, u.ID, f.c.ID, now); err != nil {
				t.Fatalf("OwnerApproveJoin: %v", err)
			}

			w := joinWantOf(before)
			w.owner, w.ownerAt = f.master.ID, now
			if tt.done {
				w.status, w.decidedAt = domain.MembershipStatusApproved, now
			}
			joinRequireMembership(t, membershipOf(t, f.s, u.ID, f.c.ID), w)
			joinRequireTouched(t, f.s, f.c, now)

			// The person hears about it only when it is settled; an Administrador's
			// approval that still waits for the admin tells them nothing yet.
			msgs := outboxMessages(t, f.s)
			if !tt.done {
				if len(msgs) != 0 {
					t.Errorf("%d emails queued, want none: %+v", len(msgs), msgs)
				}
			} else {
				msg := onlyMessage(t, msgs)
				what := "seu pedido para entrar em Alfa como " + tt.label
				if msg.To != u.Email {
					t.Errorf("email to %q, want %q", msg.To, u.Email)
				}
				for _, want := range []string{u.Name, what + ": aprovado", testOrigin + "/coletivos"} {
					if !strings.Contains(msg.Text, want) {
						t.Errorf("email lacks %q:\n%s", want, msg.Text)
					}
				}
				if strings.Contains(msg.Text, "falta um administrador") || strings.Contains(msg.Text, "motivo:") {
					t.Errorf("a settled approval has no waiting note and no reason:\n%s", msg.Text)
				}
			}

			want := domain.AuditEntry{
				At: now, Action: domain.AuditJoinOwnerApproved,
				ActorID: f.master.ID, ActorName: f.master.Name,
				UserID: u.ID, UserName: u.Name,
				CollectiveID: f.c.ID, CollectiveName: "Alfa", Detail: tt.label,
			}
			if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, want) {
				t.Errorf("audit entry = %+v, want %+v", got, want)
			}
		})
	}
}

// Whichever side decides first, the request ends approved, DecidedAt is the time
// of the second decision, and the person gets exactly one approval email (the
// second decision sends it; an admin who decides first only sends a waiting note).
func TestJoinDecisionsInEitherOrder(t *testing.T) {
	first, second := testNow.Add(time.Hour), testNow.Add(2*time.Hour)
	for _, adminFirst := range []bool{true, false} {
		name := "Administrador then admin"
		if adminFirst {
			name = "admin then Administrador"
		}
		t.Run(name, func(t *testing.T) {
			f := newJoinFixture(t)
			u := f.hopeful
			adminStep := func(now time.Time) error {
				return AdminApproveJoin(t.Context(), f.s, testOrigin, f.admin, u.ID, f.c.ID, now)
			}
			ownerStep := func(now time.Time) error {
				return OwnerApproveJoin(t.Context(), f.s, testOrigin, f.coAdmin, u.ID, f.c.ID, now)
			}
			steps := []func(time.Time) error{ownerStep, adminStep}
			if adminFirst {
				steps = []func(time.Time) error{adminStep, ownerStep}
			}

			if err := steps[0](first); err != nil {
				t.Fatalf("first decision: %v", err)
			}
			if m := membershipOf(t, f.s, u.ID, f.c.ID); m.Status != domain.MembershipStatusPending || m.DecidedAt != nil {
				t.Fatalf("after one side: status %q, DecidedAt %v, want pending and unset", m.Status, m.DecidedAt)
			}
			if err := steps[1](second); err != nil {
				t.Fatalf("second decision: %v", err)
			}

			m := membershipOf(t, f.s, u.ID, f.c.ID)
			w := joinWant{
				role: domain.RoleOwner, status: domain.MembershipStatusApproved,
				admin: f.admin.ID, owner: f.coAdmin.ID, decidedAt: second,
				adminAt: first, ownerAt: second,
			}
			if !adminFirst {
				w.adminAt, w.ownerAt = second, first
			}
			joinRequireMembership(t, m, w)
			if ok, err := f.s.Repos().Memberships.UserCanCreateEvents(t.Context(), u.ID); err != nil || !ok {
				t.Errorf("UserCanCreateEvents = %v, %v, want true: an approved Administrador may publish events", ok, err)
			}

			msgs := outboxMessages(t, f.s)
			approved, waiting := joinEmailKinds(msgs, "seu pedido para entrar em Alfa como Administrador")
			wantWaiting := 0
			if adminFirst {
				wantWaiting = 1
			}
			if approved != 1 || waiting != wantWaiting || len(msgs) != approved+waiting {
				t.Errorf("%d emails: %d approvals and %d waiting notes, want 1 and %d:\n%+v", len(msgs), approved, waiting, wantWaiting, msgs)
			}

			var actions []string
			for _, e := range auditEntries(t, f.s) {
				actions = append(actions, string(e.Action))
			}
			slices.Sort(actions)
			if want := []string{string(domain.AuditJoinAdminApproved), string(domain.AuditJoinOwnerApproved)}; !slices.Equal(actions, want) {
				t.Errorf("audit actions = %v, want %v", actions, want)
			}
		})
	}
}

// The admin and an Administrador decide the same request at the same time: both
// succeed, the request ends approved with both sides set, and the person gets
// one approval email (plus a waiting note when the admin was first).
func TestAdminAndOwnerApproveJoinConcurrent(t *testing.T) {
	for round := 1; round <= 5; round++ {
		t.Run(fmt.Sprintf("round %d", round), func(t *testing.T) {
			f := newJoinFixture(t)
			now := testNow.Add(time.Hour)
			var adminErr, ownerErr error
			var wg sync.WaitGroup
			wg.Go(func() {
				adminErr = AdminApproveJoin(t.Context(), f.s, testOrigin, f.admin, f.requester.ID, f.c.ID, now)
			})
			wg.Go(func() {
				ownerErr = OwnerApproveJoin(t.Context(), f.s, testOrigin, f.master, f.requester.ID, f.c.ID, now)
			})
			wg.Wait()
			if adminErr != nil || ownerErr != nil {
				t.Fatalf("admin: %v, Administrador: %v, want both to succeed", adminErr, ownerErr)
			}

			w := joinWant{
				role: domain.RoleMember, status: domain.MembershipStatusApproved,
				admin: f.admin.ID, owner: f.master.ID, adminAt: now, ownerAt: now, decidedAt: now,
			}
			joinRequireMembership(t, membershipOf(t, f.s, f.requester.ID, f.c.ID), w)

			msgs := outboxMessages(t, f.s)
			approved, waiting := joinEmailKinds(msgs, "seu pedido para entrar em Alfa como Membro")
			if approved != 1 || waiting > 1 || len(msgs) != approved+waiting {
				t.Errorf("%d emails: %d approvals and %d waiting notes, want 1 approval and at most 1 note:\n%+v", len(msgs), approved, waiting, msgs)
			}
			if entries := auditEntries(t, f.s); len(entries) != 2 {
				t.Errorf("%d audit entries, want 2: %+v", len(entries), entries)
			}
		})
	}
}

func TestAdminApproveJoinRefused(t *testing.T) {
	f := newJoinFixture(t)
	now := testNow.Add(time.Hour)
	approve := func(actor, user domain.User, c domain.Collective) func() error {
		return func() error { return AdminApproveJoin(t.Context(), f.s, testOrigin, actor, user.ID, c.ID, now) }
	}
	joinRequireRefused(t, f, []joinRefusal{
		{"an Administrador is not the site admin", approve(f.master, f.requester, f.c), ErrNotAllowed},
		{"a Membro", approve(f.member, f.requester, f.c), ErrNotAllowed},
		{"an admin with a disabled account", approve(joinDisabled(f.admin), f.requester, f.c), ErrNotAllowed},
		{"nobody", approve(domain.User{}, f.requester, f.c), ErrNotAllowed},
		{"a collective that is not approved", approve(f.admin, f.requester, f.pending), ErrNotAllowed},
		{"an unknown collective", approve(f.admin, f.requester, domain.Collective{ID: f.gone}), domain.ErrNotFound},
		{"a person who asked nothing", approve(f.admin, f.outsider, f.c), domain.ErrNotFound},
		{"admin side already decided", approve(f.admin, f.adminDone, f.c), ErrDecided},
		{"already a member", approve(f.admin, f.member, f.c), ErrDecided},
		{"a requester whose account is not active", approve(f.admin, f.inactive, f.c), ErrNotAllowed},
	})
}

func TestOwnerApproveJoinRefused(t *testing.T) {
	f := newJoinFixture(t)
	now := testNow.Add(time.Hour)
	approve := func(actor, user domain.User, c domain.Collective) func() error {
		return func() error { return OwnerApproveJoin(t.Context(), f.s, testOrigin, actor, user.ID, c.ID, now) }
	}
	joinRequireRefused(t, f, []joinRefusal{
		{"a Membro", approve(f.member, f.requester, f.c), ErrNotAllowed},
		{"an Administrador of another collective", approve(f.stranger, f.requester, f.c), ErrNotAllowed},
		{"the site admin, who is not an Administrador here", approve(f.admin, f.requester, f.c), ErrNotAllowed},
		{"an Administrador with a disabled account", approve(joinDisabled(f.master), f.requester, f.c), ErrNotAllowed},
		{"a person whose own request to be Administrador is pending", approve(f.hopeful, f.hopeful, f.c), ErrNotAllowed},
		{"a collective that is not approved", approve(f.master, f.requester, f.pending), ErrNotAllowed},
		{"an unknown collective, which an outsider never learns exists", approve(f.master, f.requester, domain.Collective{ID: f.gone}), ErrNotAllowed},
		{"a locked collective", approve(f.master, f.requester, f.locked), ErrLocked},
		{"a person who asked nothing", approve(f.master, f.outsider, f.c), domain.ErrNotFound},
		{"owner side already decided", approve(f.master, f.ownerDone, f.c), ErrDecided},
		{"already a member", approve(f.master, f.member, f.c), ErrDecided},
		{"a requester whose account is not active", approve(f.master, f.inactive, f.c), ErrNotAllowed},
	})
}

func TestRejectJoin(t *testing.T) {
	now := testNow.Add(time.Hour)
	tests := []struct {
		name       string
		actor      func(f joinFixture) domain.User
		user       func(f joinFixture) domain.User
		reason     string // as given
		wantReason string // as stored and mailed
	}{
		{"the site admin", func(f joinFixture) domain.User { return f.admin }, func(f joinFixture) domain.User { return f.requester }, "  não combina  ", "não combina"},
		{"an Administrador, no reason", func(f joinFixture) domain.User { return f.coAdmin }, func(f joinFixture) domain.User { return f.ownerDone }, "", ""},
		{"an Administrador, after the admin approved", func(f joinFixture) domain.User { return f.master }, func(f joinFixture) domain.User { return f.adminDone }, "sem vaga", "sem vaga"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newJoinFixture(t)
			actor, u := tt.actor(f), tt.user(f)

			if err := RejectJoin(t.Context(), f.s, testOrigin, actor, u.ID, f.c.ID, tt.reason, now); err != nil {
				t.Fatalf("RejectJoin: %v", err)
			}

			// A rejection deletes the request, so the person can ask again.
			if _, err := f.s.Repos().Memberships.Get(t.Context(), u.ID, f.c.ID); !errors.Is(err, domain.ErrNotFound) {
				t.Errorf("membership: err = %v, want ErrNotFound", err)
			}
			joinRequireTouched(t, f.s, f.c, now)

			msg := onlyMessage(t, outboxMessages(t, f.s))
			if msg.To != u.Email {
				t.Errorf("email to %q, want %q", msg.To, u.Email)
			}
			for _, want := range []string{u.Name, "seu pedido para entrar em Alfa como Membro: recusado", testOrigin + "/coletivos"} {
				if !strings.Contains(msg.Text, want) {
					t.Errorf("email lacks %q:\n%s", want, msg.Text)
				}
			}
			if got := strings.Contains(msg.Text, "motivo: "+tt.wantReason); tt.wantReason != "" && !got {
				t.Errorf("email lacks the reason %q:\n%s", tt.wantReason, msg.Text)
			}
			if tt.wantReason == "" && strings.Contains(msg.Text, "motivo:") {
				t.Errorf("no reason was given:\n%s", msg.Text)
			}

			want := domain.AuditEntry{
				At: now, Action: domain.AuditJoinRejected,
				ActorID: actor.ID, ActorName: actor.Name,
				UserID: u.ID, UserName: u.Name,
				CollectiveID: f.c.ID, CollectiveName: "Alfa", Detail: "Membro", Reason: tt.wantReason,
			}
			if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, want) {
				t.Errorf("audit entry = %+v, want %+v", got, want)
			}
		})
	}
}

func TestRejectJoinRefused(t *testing.T) {
	f := newJoinFixture(t)
	now := testNow.Add(time.Hour)
	reject := func(actor, user domain.User, c domain.Collective) func() error {
		return func() error { return RejectJoin(t.Context(), f.s, testOrigin, actor, user.ID, c.ID, "não", now) }
	}
	joinRequireRefused(t, f, []joinRefusal{
		{"a Membro", reject(f.member, f.requester, f.c), ErrNotAllowed},
		{"an Administrador of another collective", reject(f.stranger, f.requester, f.c), ErrNotAllowed},
		{"an admin with a disabled account", reject(joinDisabled(f.admin), f.requester, f.c), ErrNotAllowed},
		{"a person whose own request is pending", reject(f.hopeful, f.requester, f.c), ErrNotAllowed},
		{"a collective that is not approved, by the admin", reject(f.admin, f.requester, f.pending), ErrNotAllowed},
		{"a collective that is not approved, by an Administrador", reject(f.master, f.requester, f.pending), ErrNotAllowed},
		{"an unknown collective, by the admin", reject(f.admin, f.requester, domain.Collective{ID: f.gone}), domain.ErrNotFound},
		{"an unknown collective, by an Administrador", reject(f.master, f.requester, domain.Collective{ID: f.gone}), ErrNotAllowed},
		{"a locked collective, by an Administrador", reject(f.master, f.requester, f.locked), ErrLocked},
		{"a person who asked nothing", reject(f.admin, f.outsider, f.c), domain.ErrNotFound},
		{"already a member", reject(f.admin, f.member, f.c), ErrDecided},
	})
}

// The reason is checked before anything else, and a bad one changes nothing.
func TestRejectJoinReason(t *testing.T) {
	f := newJoinFixture(t)
	now := testNow.Add(time.Hour)
	tests := []struct {
		name   string
		actor  domain.User
		reason string
	}{
		{"too long", f.admin, strings.Repeat("a", domain.AuditTextMax+1)},
		{"a control character", f.admin, "não\x00"},
		{"invalid UTF-8", f.master, "\xff"},
		{"before the actor is checked", f.member, strings.Repeat("a", domain.AuditTextMax+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := f.snapshot(t)
			err := RejectJoin(t.Context(), f.s, testOrigin, tt.actor, f.requester.ID, f.c.ID, tt.reason, now)
			requireReasonError(t, err)
			if after := f.snapshot(t); after != before {
				t.Errorf("a bad reason changed the fixture:\nbefore:\n%s\nafter:\n%s", before, after)
			}
		})
	}
	requireNoSideEffects(t, f.s)
}

// The lock freezes the Administradores' decisions, not the site admin's: the
// admin still approves and rejects the requests of a locked collective.
func TestAdminDecisionsIgnoreTheLock(t *testing.T) {
	f := newJoinFixture(t)
	now := testNow.Add(time.Hour)

	if err := AdminApproveJoin(t.Context(), f.s, testOrigin, f.admin, f.requester.ID, f.locked.ID, now); err != nil {
		t.Errorf("AdminApproveJoin on a locked collective: %v", err)
	}
	if m := membershipOf(t, f.s, f.requester.ID, f.locked.ID); m.AdminApprovedBy != f.admin.ID {
		t.Errorf("admin side = %q, want %q", m.AdminApprovedBy, f.admin.ID)
	}
	if err := RejectJoin(t.Context(), f.s, testOrigin, f.admin, f.hopeful.ID, f.locked.ID, "não", now); err != nil {
		t.Errorf("RejectJoin on a locked collective: %v", err)
	}
	if _, err := f.s.Repos().Memberships.Get(t.Context(), f.hopeful.ID, f.locked.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("rejected membership: err = %v, want ErrNotFound", err)
	}
}

func TestPromote(t *testing.T) {
	f := newJoinFixture(t)
	now := testNow.Add(time.Hour)
	before := membershipOf(t, f.s, f.member.ID, f.c.ID)
	if ok, err := f.s.Repos().Memberships.UserCanCreateEvents(t.Context(), f.member.ID); err != nil || ok {
		t.Fatalf("UserCanCreateEvents before = %v, %v, want false: a Membro does not publish events", ok, err)
	}

	if err := Promote(t.Context(), f.s, f.master, f.member.ID, f.c.ID, now); err != nil {
		t.Fatalf("Promote: %v", err)
	}

	w := joinWantOf(before)
	w.role = domain.RoleOwner
	joinRequireMembership(t, membershipOf(t, f.s, f.member.ID, f.c.ID), w)
	if ok, err := f.s.Repos().Memberships.UserCanCreateEvents(t.Context(), f.member.ID); err != nil || !ok {
		t.Errorf("UserCanCreateEvents after = %v, %v, want true", ok, err)
	}
	joinRequireTouched(t, f.s, f.c, now)

	// A promotion is not a decision about anybody's request: no email.
	if msgs := outboxMessages(t, f.s); len(msgs) != 0 {
		t.Errorf("%d emails queued, want none: %+v", len(msgs), msgs)
	}
	want := domain.AuditEntry{
		At: now, Action: domain.AuditMemberPromoted,
		ActorID: f.master.ID, ActorName: f.master.Name,
		UserID: f.member.ID, UserName: f.member.Name,
		CollectiveID: f.c.ID, CollectiveName: "Alfa",
	}
	if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, want) {
		t.Errorf("audit entry = %+v, want %+v", got, want)
	}
}

func TestPromoteRefused(t *testing.T) {
	f := newJoinFixture(t)
	now := testNow.Add(time.Hour)
	promote := func(actor, user domain.User, c domain.Collective) func() error {
		return func() error { return Promote(t.Context(), f.s, actor, user.ID, c.ID, now) }
	}
	joinRequireRefused(t, f, []joinRefusal{
		{"a Membro", promote(f.member, f.member, f.c), ErrNotAllowed},
		{"an Administrador of another collective", promote(f.stranger, f.member, f.c), ErrNotAllowed},
		{"the site admin, who is not an Administrador here", promote(f.admin, f.member, f.c), ErrNotAllowed},
		{"an Administrador with a disabled account", promote(joinDisabled(f.master), f.member, f.c), ErrNotAllowed},
		{"a person whose own request to be Administrador is pending", promote(f.hopeful, f.member, f.c), ErrNotAllowed},
		{"a collective that is not approved", promote(f.master, f.member, f.pending), ErrNotAllowed},
		{"an unknown collective", promote(f.master, f.member, domain.Collective{ID: f.gone}), ErrNotAllowed},
		{"a locked collective", promote(f.master, f.member, f.locked), ErrLocked},
		{"an Administrador already", promote(f.master, f.coAdmin, f.c), ErrDecided},
		{"a request that is still pending", promote(f.master, f.requester, f.c), ErrDecided},
		{"a person who is not in the collective", promote(f.master, f.outsider, f.c), domain.ErrNotFound},
	})
}

func TestDemote(t *testing.T) {
	now := testNow.Add(time.Hour)
	tests := []struct {
		name  string
		actor func(f joinFixture) domain.User
	}{
		{"another Administrador", func(f joinFixture) domain.User { return f.master }},
		{"themself, while another Administrador remains", func(f joinFixture) domain.User { return f.coAdmin }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newJoinFixture(t)
			actor := tt.actor(f)
			before := membershipOf(t, f.s, f.coAdmin.ID, f.c.ID)

			if err := Demote(t.Context(), f.s, actor, f.coAdmin.ID, f.c.ID, now); err != nil {
				t.Fatalf("Demote: %v", err)
			}

			w := joinWantOf(before)
			w.role = domain.RoleMember
			joinRequireMembership(t, membershipOf(t, f.s, f.coAdmin.ID, f.c.ID), w)
			joinRequireTouched(t, f.s, f.c, now)

			if msgs := outboxMessages(t, f.s); len(msgs) != 0 {
				t.Errorf("%d emails queued, want none: %+v", len(msgs), msgs)
			}
			want := domain.AuditEntry{
				At: now, Action: domain.AuditMemberDemoted,
				ActorID: actor.ID, ActorName: actor.Name,
				UserID: f.coAdmin.ID, UserName: f.coAdmin.Name,
				CollectiveID: f.c.ID, CollectiveName: "Alfa",
			}
			if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, want) {
				t.Errorf("audit entry = %+v, want %+v", got, want)
			}
		})
	}
}

func TestDemoteRefused(t *testing.T) {
	f := newJoinFixture(t)
	now := testNow.Add(time.Hour)
	demote := func(actor, user domain.User, c domain.Collective) func() error {
		return func() error { return Demote(t.Context(), f.s, actor, user.ID, c.ID, now) }
	}
	joinRequireRefused(t, f, []joinRefusal{
		{"a Membro", demote(f.member, f.coAdmin, f.c), ErrNotAllowed},
		{"an Administrador of another collective", demote(f.stranger, f.coAdmin, f.c), ErrNotAllowed},
		{"the site admin, who is not an Administrador here", demote(f.admin, f.coAdmin, f.c), ErrNotAllowed},
		{"an Administrador with a disabled account", demote(joinDisabled(f.master), f.coAdmin, f.c), ErrNotAllowed},
		{"a person whose own request to be Administrador is pending", demote(f.hopeful, f.coAdmin, f.c), ErrNotAllowed},
		{"a collective that is not approved", demote(f.master, f.coAdmin, f.pending), ErrNotAllowed},
		{"an unknown collective", demote(f.master, f.coAdmin, domain.Collective{ID: f.gone}), ErrNotAllowed},
		{"a locked collective", demote(f.master, f.coAdmin, f.locked), ErrLocked},
		{"a Membro already", demote(f.master, f.member, f.c), ErrDecided},
		{"a pending request to be Administrador", demote(f.master, f.hopeful, f.c), ErrDecided},
		{"a person who is not in the collective", demote(f.master, f.outsider, f.c), domain.ErrNotFound},
		{"the Master, by themself", demote(f.master, f.master, f.c), ErrMaster},
		{"the Master, by another Administrador", demote(f.coAdmin, f.master, f.c), ErrMaster},
	})
}

// Nothing but the count protects the last Administrador when the Master is gone
// (the creator's account was deleted) or never held an Administrador seat. The
// others that are not active approved Administradores do not count: an account
// that is disabled, a request to become Administrador that is still pending and
// a Membro.
func TestDemoteLastAdministrador(t *testing.T) {
	tests := []struct {
		name      string
		createdBy func(founder domain.User) string
	}{
		{"no Master", func(domain.User) string { return "" }},
		{"the Master is only a Membro", func(founder domain.User) string { return founder.ID }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestStore(t)
			admin := insertAdmin(t, s, "ada@agenda.test")
			solo := insertUser(t, s, "sol@agenda.test")
			founder := insertUser(t, s, "fabio@agenda.test")
			disabled := insertUser(t, s, "olga@agenda.test", func(u *domain.User) { u.Status = domain.UserStatusDisabled })
			hopeful := insertUser(t, s, "hugo@agenda.test")
			c := insertCollective(t, s, "Alfa", tt.createdBy(founder), joinApproved(admin))
			none := domain.User{}
			insertMembership(t, s, joinMembership(solo, c, domain.RoleOwner, admin, solo))
			insertMembership(t, s, joinMembership(disabled, c, domain.RoleOwner, admin, solo))
			insertMembership(t, s, joinMembership(hopeful, c, domain.RoleOwner, none, none))
			insertMembership(t, s, joinMembership(founder, c, domain.RoleMember, admin, solo))
			now := testNow.Add(time.Hour)

			// The only active Administrador cannot leave.
			requireErrorIs(t, Demote(t.Context(), s, solo, solo.ID, c.ID, now), ErrLastOwner)
			if m := membershipOf(t, s, solo.ID, c.ID); m.Role != domain.RoleOwner {
				t.Errorf("role after the refusal = %q, want owner", m.Role)
			}
			if got := collectiveOf(t, s, c.ID); !got.UpdatedAt.Truncate(time.Millisecond).Equal(testNow) {
				t.Errorf("UpdatedAt = %v after a refusal, want it unchanged", got.UpdatedAt)
			}
			requireNoSideEffects(t, s)

			// With a second active Administrador, one may leave and the other cannot.
			other := insertUser(t, s, "otto@agenda.test")
			insertMembership(t, s, joinMembership(other, c, domain.RoleOwner, admin, solo))
			if err := Demote(t.Context(), s, solo, solo.ID, c.ID, now); err != nil {
				t.Fatalf("Demote while another Administrador remains: %v", err)
			}
			if m := membershipOf(t, s, solo.ID, c.ID); m.Role != domain.RoleMember {
				t.Errorf("role = %q, want member", m.Role)
			}
			requireErrorIs(t, Demote(t.Context(), s, other, other.ID, c.ID, now.Add(time.Minute)), ErrLastOwner)
			if got := joinActiveAdministradores(t, s, c.ID); got != 1 {
				t.Errorf("%d active Administradores left, want 1", got)
			}
		})
	}
}

// Two Administradores demote each other at the same time in a collective whose
// only Administradores they are: exactly one demotion is made, and the collective
// keeps an approved Administrador. The loser is refused: when its transaction
// runs again it reads that it is no longer an Administrador (ErrNotAllowed); a
// count that saw itself as the last one would be ErrLastOwner, and ErrDecided
// would mean its target was already demoted.
func TestDemoteConcurrent(t *testing.T) {
	for round := 1; round <= 5; round++ {
		t.Run(fmt.Sprintf("round %d", round), func(t *testing.T) {
			s := newTestStore(t)
			admin := insertAdmin(t, s, "ada@agenda.test")
			a := insertUser(t, s, "ana@agenda.test")
			b := insertUser(t, s, "bia@agenda.test")
			c := insertCollective(t, s, "Alfa", "", joinApproved(admin))
			insertMembership(t, s, joinMembership(a, c, domain.RoleOwner, admin, a))
			insertMembership(t, s, joinMembership(b, c, domain.RoleOwner, admin, a))
			now := testNow.Add(time.Hour)

			var errA, errB error
			var wg sync.WaitGroup
			wg.Go(func() { errA = Demote(t.Context(), s, a, b.ID, c.ID, now) })
			wg.Go(func() { errB = Demote(t.Context(), s, b, a.ID, c.ID, now) })
			wg.Wait()

			switch {
			case errA == nil && errB == nil:
				t.Fatal("both demotions succeeded: the collective has no Administrador left")
			case errA != nil && errB != nil:
				t.Fatalf("both demotions failed: %v, %v", errA, errB)
			}
			loser := errA
			if loser == nil {
				loser = errB
			}
			if isNoneOf(loser, ErrNotAllowed, ErrLastOwner, ErrDecided) {
				t.Errorf("the losing demotion returned %v, want ErrNotAllowed, ErrLastOwner or ErrDecided", loser)
			}
			if got := joinActiveAdministradores(t, s, c.ID); got != 1 {
				t.Errorf("%d active Administradores left, want exactly 1", got)
			}
			onlyAudit(t, auditEntries(t, s))
		})
	}
}

func TestCollectiveView(t *testing.T) {
	f := newJoinFixture(t)

	page, err := CollectiveView(t.Context(), f.s, f.coAdmin, f.c.ID)
	if err != nil {
		t.Fatalf("CollectiveView: %v", err)
	}
	if page.Collective.ID != f.c.ID || page.Collective.Name != "Alfa" || page.Collective.CreatedBy != f.master.ID {
		t.Errorf("collective = %+v, want Alfa created by the Master", page.Collective)
	}
	// Approved memberships only, the Administradores first and then by name.
	joinRequireMembers(t, "members", page.Members, f.master, f.coAdmin, f.member)
	// The requests an Administrador can still decide: pending, owner side open,
	// the person's account active (the Administradores first, then by name). The
	// one the Administrador side already decided and the one of an account that
	// awaits approval are left out.
	joinRequireMembers(t, "requests", page.Requests, f.hopeful, f.adminDone, f.requester)
	for _, v := range page.Members {
		if v.Status != domain.MembershipStatusApproved {
			t.Errorf("member %s has status %q", v.UserName, v.Status)
		}
	}
	for _, v := range page.Requests {
		if v.Status != domain.MembershipStatusPending || v.OwnerApprovedAt != nil || v.UserStatus != domain.UserStatusActive {
			t.Errorf("request of %s: status %q, owner side %v, account %q", v.UserName, v.Status, v.OwnerApprovedAt, v.UserStatus)
		}
	}
}

// The page never confirms a collective exists to someone who may not manage it:
// every refusal is domain.ErrNotFound.
func TestCollectiveViewRefused(t *testing.T) {
	f := newJoinFixture(t)
	view := func(actor domain.User, c domain.Collective) func() error {
		return func() error {
			page, err := CollectiveView(t.Context(), f.s, actor, c.ID)
			if err == nil {
				return fmt.Errorf("CollectiveView returned the page of %q", page.Collective.Name)
			}
			return err
		}
	}
	joinRequireRefused(t, f, []joinRefusal{
		{"a Membro", view(f.member, f.c), domain.ErrNotFound},
		{"an Administrador of another collective", view(f.stranger, f.c), domain.ErrNotFound},
		{"a person outside every collective", view(f.outsider, f.c), domain.ErrNotFound},
		{"the site admin, who is not an Administrador here", view(f.admin, f.c), domain.ErrNotFound},
		{"an Administrador with a disabled account", view(joinDisabled(f.master), f.c), domain.ErrNotFound},
		{"a person whose own request to be Administrador is pending", view(f.hopeful, f.c), domain.ErrNotFound},
		{"a collective that is not approved", view(f.master, f.pending), domain.ErrNotFound},
		{"an unknown collective", view(f.master, domain.Collective{ID: f.gone}), domain.ErrNotFound},
	})
}

func TestCollectivePageIsMaster(t *testing.T) {
	tests := []struct {
		name      string
		createdBy string
		user      string
		want      bool
	}{
		{"the creator", "u1", "u1", true},
		{"another person", "u1", "u2", false},
		{"nobody asked", "u1", "", false},
		{"the creator was deleted", "", "u1", false},
		{"the creator was deleted and nobody asked", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page := CollectivePage{Collective: domain.Collective{CreatedBy: tt.createdBy}}
			if got := page.IsMaster(tt.user); got != tt.want {
				t.Errorf("IsMaster(%q) with creator %q = %v, want %v", tt.user, tt.createdBy, got, tt.want)
			}
		})
	}
}

// adminApproveJoinTx and rejectJoinTx are what the admin queue's "approve
// everything" reuses: they write inside the caller's transaction and add their
// line to the caller's decision, so the person gets one email for every item.
// The admin side checks no account status: the queue also decides the join
// requests of an account it approves in the same call.
func TestJoinTxHelpersShareTheCallersDecision(t *testing.T) {
	link := testOrigin + "/coletivos"
	now := testNow.Add(time.Hour)

	tests := []struct {
		name  string
		user  func(f joinFixture) domain.User
		run   func(ctx context.Context, r domain.Repos, f joinFixture, u domain.User, c domain.Collective, m domain.Membership, d *decision) error
		lines []string
		after func(t *testing.T, f joinFixture, u domain.User)
	}{
		{
			name: "admin approval of an account that is not active yet",
			user: func(f joinFixture) domain.User { return f.inactive },
			run: func(ctx context.Context, r domain.Repos, f joinFixture, u domain.User, c domain.Collective, m domain.Membership, d *decision) error {
				return adminApproveJoinTx(ctx, r, f.admin, u, c, m, d, now)
			},
			lines: []string{"sua conta: aprovado", "seu pedido para entrar em Alfa como Membro: aprovado pelo admin; falta um administrador do coletivo"},
			after: func(t *testing.T, f joinFixture, u domain.User) {
				if m := membershipOf(t, f.s, u.ID, f.c.ID); m.AdminApprovedBy != f.admin.ID || !sameTime(m.AdminApprovedAt, now) || m.Status != domain.MembershipStatusPending {
					t.Errorf("membership = admin side %q at %v, status %q, want the admin's side set and still pending", m.AdminApprovedBy, m.AdminApprovedAt, m.Status)
				}
			},
		},
		{
			name: "admin approval that settles the request",
			user: func(f joinFixture) domain.User { return f.ownerDone },
			run: func(ctx context.Context, r domain.Repos, f joinFixture, u domain.User, c domain.Collective, m domain.Membership, d *decision) error {
				return adminApproveJoinTx(ctx, r, f.admin, u, c, m, d, now)
			},
			lines: []string{"sua conta: aprovado", "seu pedido para entrar em Alfa como Membro: aprovado"},
			after: func(t *testing.T, f joinFixture, u domain.User) {
				if m := membershipOf(t, f.s, u.ID, f.c.ID); m.Status != domain.MembershipStatusApproved || !sameTime(m.DecidedAt, now) {
					t.Errorf("membership = status %q, DecidedAt %v, want approved at %v", m.Status, m.DecidedAt, now)
				}
			},
		},
		{
			name: "rejection",
			user: func(f joinFixture) domain.User { return f.requester },
			run: func(ctx context.Context, r domain.Repos, f joinFixture, u domain.User, c domain.Collective, m domain.Membership, d *decision) error {
				return rejectJoinTx(ctx, r, f.admin, u, c, m, "não", d, now)
			},
			lines: []string{"sua conta: aprovado", "seu pedido para entrar em Alfa como Membro: recusado", "motivo: não"},
			after: func(t *testing.T, f joinFixture, u domain.User) {
				if _, err := f.s.Repos().Memberships.Get(t.Context(), u.ID, f.c.ID); !errors.Is(err, domain.ErrNotFound) {
					t.Errorf("membership: err = %v, want ErrNotFound", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newJoinFixture(t)
			u := tt.user(f)
			err := f.s.WithTx(t.Context(), func(ctx context.Context, r domain.Repos) error {
				c, err := r.Collectives.ByID(ctx, f.c.ID)
				if err != nil {
					return err
				}
				m, err := r.Memberships.Get(ctx, u.ID, f.c.ID)
				if err != nil {
					return err
				}
				d := newDecision(u.Email, u.Name)
				d.add(whatAccount, mail.DecisionApproved, "")
				if err := tt.run(ctx, r, f, u, c, m, d); err != nil {
					return err
				}
				return d.send(ctx, r, link, now)
			})
			if err != nil {
				t.Fatalf("transaction: %v", err)
			}
			tt.after(t, f, u)
			joinRequireTouched(t, f.s, f.c, now)

			msg := onlyMessage(t, outboxMessages(t, f.s))
			for _, want := range tt.lines {
				if !strings.Contains(msg.Text, want) {
					t.Errorf("email lacks %q:\n%s", want, msg.Text)
				}
			}
			// The helper writes the request's audit entry and leaves the account's
			// to its caller.
			onlyAudit(t, auditEntries(t, f.s))
		})
	}
}
