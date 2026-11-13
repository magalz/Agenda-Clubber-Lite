package approvals

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// accountsFixture is a store with a site admin.
type accountsFixture struct {
	s     domain.Store
	admin domain.User
}

func newAccountsFixture(t *testing.T) accountsFixture {
	t.Helper()
	s := newTestStore(t)
	return accountsFixture{s: s, admin: insertAdmin(t, s, "ada@agenda.test")}
}

// accountsStatus makes the user have the given status.
func accountsStatus(status domain.UserStatus) func(*domain.User) {
	return func(u *domain.User) { u.Status = status }
}

// accountsPending stores a person whose email is confirmed and who awaits the
// admin, as a signup leaves them.
func accountsPending(t *testing.T, s domain.Store, email string, mutate ...func(*domain.User)) domain.User {
	t.Helper()
	return insertUser(t, s, email, append([]func(*domain.User){accountsStatus(domain.UserStatusPendingApproval)}, mutate...)...)
}

// accountsApprovedCollective stores a collective approved by approver, with
// owner as its Master and approved Administrador.
func accountsApprovedCollective(t *testing.T, s domain.Store, name string, owner, approver domain.User, mutate ...func(*domain.Collective)) domain.Collective {
	t.Helper()
	c := insertCollective(t, s, name, owner.ID, append([]func(*domain.Collective){joinApproved(approver)}, mutate...)...)
	insertMembership(t, s, joinMembership(owner, c, domain.RoleOwner, approver, owner))
	return c
}

// accountsRequest stores the pending request of u to join c in role, made at
// the given time, with the Administrador side already decided by owner (a zero
// User leaves it open) and the admin side open.
func accountsRequest(t *testing.T, s domain.Store, u domain.User, c domain.Collective, role domain.Role, owner domain.User, at time.Time) {
	t.Helper()
	m := joinMembership(u, c, role, domain.User{}, owner)
	m.RequestedAt = at
	insertMembership(t, s, m)
}

func accountsUser(t *testing.T, s domain.Store, id string) domain.User {
	t.Helper()
	u, err := s.Repos().Users.ByID(t.Context(), id)
	if err != nil {
		t.Fatalf("read user %s: %v", id, err)
	}
	return u
}

// accountsRequireGone fails the test when the user still exists.
func accountsRequireGone(t *testing.T, s domain.Store, u domain.User) {
	t.Helper()
	if _, err := s.Repos().Users.ByID(t.Context(), u.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("user %s after the decision: err = %v, want ErrNotFound", u.Email, err)
	}
}

// accountsRequireCollectiveGone fails the test when the collective still exists.
func accountsRequireCollectiveGone(t *testing.T, s domain.Store, c domain.Collective) {
	t.Helper()
	if _, err := s.Repos().Collectives.ByID(t.Context(), c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("collective %s after the decision: err = %v, want ErrNotFound", c.Name, err)
	}
}

// accountsSession stores a session of the user whose hash is n repeated.
func accountsSession(t *testing.T, s domain.Store, userID string, n byte) []byte {
	t.Helper()
	hash := bytes.Repeat([]byte{n}, 32)
	err := s.Repos().Sessions.Create(t.Context(), domain.Session{
		Hash: hash, UserID: userID, CreatedAt: testNow, LastSeenAt: testNow, ExpiresAt: testNow.Add(24 * time.Hour), UserAgent: "test",
	})
	if err != nil {
		t.Fatalf("insert session %d: %v", n, err)
	}
	return hash
}

// accountsSessionExists reports whether the session is stored.
func accountsSessionExists(t *testing.T, s domain.Store, hash []byte) bool {
	t.Helper()
	_, err := s.Repos().Sessions.Get(t.Context(), hash)
	switch {
	case err == nil:
		return true
	case errors.Is(err, domain.ErrNotFound):
		return false
	}
	t.Fatalf("read session: %v", err)
	return false
}

// accountsByRecipient groups the emails by address.
func accountsByRecipient(msgs []domain.OutboxMessage) map[string][]domain.OutboxMessage {
	by := map[string][]domain.OutboxMessage{}
	for _, m := range msgs {
		by[m.To] = append(by[m.To], m)
	}
	return by
}

// accountsOnlyTo returns the single email to the address, failing the test when
// there is any other number of them.
func accountsOnlyTo(t *testing.T, by map[string][]domain.OutboxMessage, to string) domain.OutboxMessage {
	t.Helper()
	if len(by[to]) != 1 {
		t.Fatalf("%d emails to %s, want exactly 1: %+v", len(by[to]), to, by[to])
	}
	return by[to][0]
}

// accountsRequireLines fails the test unless text holds every line, in this order.
func accountsRequireLines(t *testing.T, text string, lines ...string) {
	t.Helper()
	pos := 0
	for _, line := range lines {
		i := strings.Index(text[pos:], line)
		if i < 0 {
			t.Errorf("email lacks %q (after offset %d):\n%s", line, pos, text)
			return
		}
		pos += i + len(line)
	}
}

// accountsActions counts the audit entries by action.
func accountsActions(entries []domain.AuditEntry) map[domain.AuditAction]int {
	counts := map[domain.AuditAction]int{}
	for _, e := range entries {
		counts[e.Action]++
	}
	return counts
}

// accountsRequireActions fails the test unless the audit log holds exactly the
// given entries by action, all of them by actor and about user.
func accountsRequireActions(t *testing.T, entries []domain.AuditEntry, actor, user domain.User, want map[domain.AuditAction]int) {
	t.Helper()
	if got := accountsActions(entries); !maps.Equal(got, want) {
		t.Errorf("audit actions = %v, want %v", got, want)
	}
	for _, e := range entries {
		if e.ActorID != actor.ID || e.ActorName != actor.Name || e.UserID != user.ID || e.UserName != user.Name {
			t.Errorf("audit entry %s = actor %q, user %q; want actor %q, user %q", e.Action, e.ActorID, e.UserID, actor.ID, user.ID)
		}
	}
}

// accountsDecisions are the three decisions about one account that share their
// refusals, as a call each.
var accountsDecisions = []struct {
	name string
	call func(ctx context.Context, s domain.Store, actor domain.User, id string) error
}{
	{"ApproveAccount", func(ctx context.Context, s domain.Store, actor domain.User, id string) error {
		return ApproveAccount(ctx, s, testOrigin, actor, id, testNow.Add(time.Hour))
	}},
	{"ApprovePerson", func(ctx context.Context, s domain.Store, actor domain.User, id string) error {
		return ApprovePerson(ctx, s, testOrigin, actor, id, testNow.Add(time.Hour))
	}},
	{"RejectAccount", func(ctx context.Context, s domain.Store, actor domain.User, id string) error {
		return RejectAccount(ctx, s, testOrigin, actor, id, "", testNow.Add(time.Hour))
	}},
}

// ApproveAccount approves only the account: the person's own collectives and
// join requests keep waiting, and the Administradores of the collectives the
// person asked to join are told, since the requests are visible to them now.
func TestApproveAccount(t *testing.T) {
	f := newAccountsFixture(t)
	owner := insertUser(t, f.s, "olga@agenda.test")
	p := accountsPending(t, f.s, "paula@agenda.test")
	alfa, alfaM := insertNewCollective(t, f.s, "Alfa", p)
	beta := accountsApprovedCollective(t, f.s, "Beta", owner, f.admin)
	accountsRequest(t, f.s, p, beta, domain.RoleMember, domain.User{}, testNow)
	betaBefore, betaMBefore := collectiveOf(t, f.s, beta.ID), membershipOf(t, f.s, p.ID, beta.ID)
	now := testNow.Add(time.Hour)

	if err := ApproveAccount(t.Context(), f.s, testOrigin, f.admin, p.ID, now); err != nil {
		t.Fatalf("ApproveAccount: %v", err)
	}

	got := accountsUser(t, f.s, p.ID)
	if got.Status != domain.UserStatusActive || got.ApprovedBy != f.admin.ID || !sameTime(got.ApprovedAt, now) {
		t.Errorf("account = status %q, approved by %q at %v; want active, by %q at %v", got.Status, got.ApprovedBy, got.ApprovedAt, f.admin.ID, now)
	}
	if !got.UpdatedAt.Truncate(time.Millisecond).Equal(now) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, now)
	}

	// Nothing else was decided.
	if c := collectiveOf(t, f.s, alfa.ID); !sameCollective(c, alfa) {
		t.Errorf("the person's new collective changed: %+v, was %+v", c, alfa)
	}
	if m := membershipOf(t, f.s, p.ID, alfa.ID); m.Status != alfaM.Status || m.AdminApprovedAt != nil || m.DecidedAt != nil {
		t.Errorf("the creator's membership changed: %+v, was %+v", m, alfaM)
	}
	if c := collectiveOf(t, f.s, beta.ID); !sameCollective(c, betaBefore) {
		t.Errorf("the collective of the join request changed: %+v, was %+v", c, betaBefore)
	}
	if m := membershipOf(t, f.s, p.ID, beta.ID); !reflect.DeepEqual(m, betaMBefore) {
		t.Errorf("the join request changed: %+v, was %+v", m, betaMBefore)
	}

	// The person hears about the account only; the Administrador of Beta hears
	// about the request, which they can see from now on.
	msgs := outboxMessages(t, f.s)
	if len(msgs) != 2 {
		t.Fatalf("%d emails queued, want 2: %+v", len(msgs), msgs)
	}
	by := accountsByRecipient(msgs)
	text := accountsOnlyTo(t, by, p.Email).Text
	accountsRequireLines(t, text, p.Name, "- sua conta: aprovado", "[→] "+testOrigin+"/entrar")
	for _, unwanted := range []string{"Alfa", "Beta", "motivo:"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("the account email mentions %q:\n%s", unwanted, text)
		}
	}
	notice := accountsOnlyTo(t, by, owner.Email)
	for _, want := range []string{p.Name, "Beta", "Membro", testOrigin + collectivePath(beta.ID)} {
		if !strings.Contains(notice.Text, want) {
			t.Errorf("the notice to the Administrador lacks %q:\n%s", want, notice.Text)
		}
	}

	entry := onlyAudit(t, auditEntries(t, f.s))
	if !sameAudit(entry, domain.AuditEntry{At: now, Action: domain.AuditAccountApproved, ActorID: f.admin.ID, ActorName: f.admin.Name, UserID: p.ID, UserName: p.Name}) {
		t.Errorf("audit entry = %+v", entry)
	}
}

// "Aprovar tudo": the account, every collective the person created and the admin
// side of every join request, in one transaction, with one email to the person.
func TestApprovePerson(t *testing.T) {
	f := newAccountsFixture(t)
	other := insertAdmin(t, f.s, "bob@agenda.test")
	owner := insertUser(t, f.s, "olga@agenda.test")
	p := accountsPending(t, f.s, "paula@agenda.test")
	alfa, _ := insertNewCollective(t, f.s, "Alfa", p)
	zeta, _ := insertNewCollective(t, f.s, "Zeta", p, func(c *domain.Collective) { c.Kind = domain.CollectiveKindProdutora })
	beta := accountsApprovedCollective(t, f.s, "Beta", owner, other, func(c *domain.Collective) { c.EventsBlocked, c.Locked = true, true })
	gama := accountsApprovedCollective(t, f.s, "Gama", owner, other)
	accountsRequest(t, f.s, p, beta, domain.RoleMember, domain.User{}, testNow)
	accountsRequest(t, f.s, p, gama, domain.RoleOwner, owner, testNow)
	now := testNow.Add(time.Hour)

	if err := ApprovePerson(t.Context(), f.s, testOrigin, f.admin, p.ID, now); err != nil {
		t.Fatalf("ApprovePerson: %v", err)
	}

	got := accountsUser(t, f.s, p.ID)
	if got.Status != domain.UserStatusActive || got.ApprovedBy != f.admin.ID || !sameTime(got.ApprovedAt, now) {
		t.Errorf("account = status %q, approved by %q at %v; want active, by %q at %v", got.Status, got.ApprovedBy, got.ApprovedAt, f.admin.ID, now)
	}

	// The collectives the person created are approved, with their membership.
	for _, c := range []domain.Collective{alfa, zeta} {
		gc := collectiveOf(t, f.s, c.ID)
		if gc.Status != domain.CollectiveStatusApproved || gc.ApprovedBy != f.admin.ID || !sameTime(gc.ApprovedAt, now) || gc.Name != c.Name || gc.Kind != c.Kind {
			t.Errorf("%s = %+v, want approved by %q at %v", c.Name, gc, f.admin.ID, now)
		}
		m := membershipOf(t, f.s, p.ID, c.ID)
		if m.Status != domain.MembershipStatusApproved || m.Role != domain.RoleOwner || m.AdminApprovedBy != f.admin.ID || !sameTime(m.AdminApprovedAt, now) || !sameTime(m.DecidedAt, now) {
			t.Errorf("membership in %s = %+v, want approved by %q at %v", c.Name, m, f.admin.ID, now)
		}
	}
	if ok, err := f.s.Repos().Memberships.UserCanCreateEvents(t.Context(), p.ID); err != nil || !ok {
		t.Errorf("UserCanCreateEvents = %v, %v, want true", ok, err)
	}

	// A join request waits for its Administrador, and the collective keeps its
	// blocks and its approver: the admin's decision writes it back as it is.
	gb := collectiveOf(t, f.s, beta.ID)
	if gb.Status != domain.CollectiveStatusApproved || gb.ApprovedBy != other.ID || !gb.EventsBlocked || !gb.Locked || !gb.UpdatedAt.Truncate(time.Millisecond).Equal(now) {
		t.Errorf("Beta = %+v, want approved by %q, still blocked and locked, updated at %v", gb, other.ID, now)
	}
	mb := membershipOf(t, f.s, p.ID, beta.ID)
	if mb.Status != domain.MembershipStatusPending || mb.AdminApprovedBy != f.admin.ID || !sameTime(mb.AdminApprovedAt, now) || mb.OwnerApprovedAt != nil || mb.DecidedAt != nil {
		t.Errorf("request to Beta = %+v, want pending with only the admin side decided at %v", mb, now)
	}
	// A request the Administrador had decided already is approved outright.
	gg := collectiveOf(t, f.s, gama.ID)
	if gg.ApprovedBy != other.ID || gg.Locked || gg.EventsBlocked {
		t.Errorf("Gama = %+v, want its approver %q and no blocks", gg, other.ID)
	}
	mg := membershipOf(t, f.s, p.ID, gama.ID)
	if mg.Status != domain.MembershipStatusApproved || mg.Role != domain.RoleOwner || mg.AdminApprovedBy != f.admin.ID || mg.OwnerApprovedBy != owner.ID || !sameTime(mg.DecidedAt, now) {
		t.Errorf("request to Gama = %+v, want approved now by %q and %q", mg, f.admin.ID, owner.ID)
	}

	// One email to the person, the account first and then the items by name; the
	// Administrador of Beta and Gama hears only of the request that waits for them.
	msgs := outboxMessages(t, f.s)
	if len(msgs) != 2 {
		t.Fatalf("%d emails queued, want 2: %+v", len(msgs), msgs)
	}
	by := accountsByRecipient(msgs)
	text := accountsOnlyTo(t, by, p.Email).Text
	accountsRequireLines(t, text,
		"- sua conta: aprovado",
		"- o coletivo Alfa: aprovado",
		"- seu pedido para entrar em Beta como Membro: aprovado pelo admin; falta um administrador do coletivo",
		"- seu pedido para entrar em Gama como Administrador: aprovado\n",
		"- a produtora Zeta: aprovado",
		"[→] "+testOrigin+"/coletivos")
	notice := accountsOnlyTo(t, by, owner.Email)
	if !strings.Contains(notice.Text, "Beta") || !strings.Contains(notice.Text, p.Name) || strings.Contains(notice.Text, "Gama") {
		t.Errorf("the notice to the Administrador should name Beta and not Gama:\n%s", notice.Text)
	}

	accountsRequireActions(t, auditEntries(t, f.s), f.admin, p, map[domain.AuditAction]int{
		domain.AuditAccountApproved:    1,
		domain.AuditCollectiveApproved: 2,
		domain.AuditJoinAdminApproved:  2,
	})

	// Nothing of the person is left for the admin.
	cards, err := AdminQueue(t.Context(), f.s, f.admin)
	if err != nil || len(cards) != 0 {
		t.Errorf("AdminQueue after everything was decided = %+v, %v, want no cards", cards, err)
	}
}

// Only what waits for the admin is decided: a request that is already approved,
// one whose admin side is set and one in a rejected collective are left alone.
func TestApprovePersonSkipsWhatIsNotOpen(t *testing.T) {
	f := newAccountsFixture(t)
	owner := insertUser(t, f.s, "olga@agenda.test")
	p := accountsPending(t, f.s, "paula@agenda.test")
	xis := accountsApprovedCollective(t, f.s, "Xis", owner, f.admin)
	ypsilon := accountsApprovedCollective(t, f.s, "Ypsilon", owner, f.admin)
	rejeitada := insertCollective(t, f.s, "Rejeitada", owner.ID, func(c *domain.Collective) { c.Status = domain.CollectiveStatusRejected })
	insertMembership(t, f.s, joinMembership(p, xis, domain.RoleMember, f.admin, owner))
	insertMembership(t, f.s, joinMembership(p, ypsilon, domain.RoleMember, f.admin, domain.User{}))
	accountsRequest(t, f.s, p, rejeitada, domain.RoleMember, domain.User{}, testNow)

	collectives := []domain.Collective{xis, ypsilon, rejeitada}
	beforeC := make([]domain.Collective, len(collectives))
	beforeM := make([]domain.Membership, len(collectives))
	for i, c := range collectives {
		beforeC[i], beforeM[i] = collectiveOf(t, f.s, c.ID), membershipOf(t, f.s, p.ID, c.ID)
	}
	now := testNow.Add(time.Hour)

	if err := ApprovePerson(t.Context(), f.s, testOrigin, f.admin, p.ID, now); err != nil {
		t.Fatalf("ApprovePerson: %v", err)
	}

	if got := accountsUser(t, f.s, p.ID); got.Status != domain.UserStatusActive {
		t.Errorf("account status = %q, want active", got.Status)
	}
	for i, c := range collectives {
		if got := collectiveOf(t, f.s, c.ID); !sameCollective(got, beforeC[i]) {
			t.Errorf("%s changed: %+v, was %+v", c.Name, got, beforeC[i])
		}
		if got := membershipOf(t, f.s, p.ID, c.ID); !reflect.DeepEqual(got, beforeM[i]) {
			t.Errorf("membership in %s changed: %+v, was %+v", c.Name, got, beforeM[i])
		}
	}

	// The person's email holds the account only. The request whose admin side
	// was set already is visible to its Administrador now, so it is told.
	msgs := outboxMessages(t, f.s)
	if len(msgs) != 2 {
		t.Fatalf("%d emails queued, want 2: %+v", len(msgs), msgs)
	}
	by := accountsByRecipient(msgs)
	text := accountsOnlyTo(t, by, p.Email).Text
	accountsRequireLines(t, text, "- sua conta: aprovado")
	if strings.Contains(text, "Xis") || strings.Contains(text, "Ypsilon") || strings.Contains(text, "Rejeitada") {
		t.Errorf("the person's email names an item that was not decided:\n%s", text)
	}
	if notice := accountsOnlyTo(t, by, owner.Email); !strings.Contains(notice.Text, "Ypsilon") {
		t.Errorf("the notice should name Ypsilon:\n%s", notice.Text)
	}
	accountsRequireActions(t, auditEntries(t, f.s), f.admin, p, map[domain.AuditAction]int{domain.AuditAccountApproved: 1})
}

// The three decisions about an account refuse the same people the same way, and
// leave everything as it was: an actor who is not an active admin is ErrNotAllowed,
// a target in any other state than the one the decision takes is ErrDecided, and
// an unknown target is ErrNotFound. The active account here has nothing open, so
// even ApprovePerson (which also decides the items of an active person) has
// nothing to do.
func TestAccountDecisionsRefused(t *testing.T) {
	f := newAccountsFixture(t)
	plain := insertUser(t, f.s, "bia@agenda.test")
	disabledAdmin := insertUser(t, f.s, "dora@agenda.test", func(u *domain.User) { u.IsAdmin, u.Status = true, domain.UserStatusDisabled })
	active := insertUser(t, f.s, "ana@agenda.test")
	unconfirmed := accountsPending(t, f.s, "ugo@agenda.test", accountsStatus(domain.UserStatusPendingEmail))
	disabled := insertUser(t, f.s, "dan@agenda.test", accountsStatus(domain.UserStatusDisabled))
	pending := accountsPending(t, f.s, "paula@agenda.test")

	tests := []struct {
		name   string
		actor  domain.User
		target string
		want   error
	}{
		{"actor is not an admin", plain, pending.ID, ErrNotAllowed},
		{"actor is a disabled admin", disabledAdmin, pending.ID, ErrNotAllowed},
		{"account is active", f.admin, active.ID, ErrDecided},
		{"email is not confirmed", f.admin, unconfirmed.ID, ErrDecided},
		{"account is disabled", f.admin, disabled.ID, ErrDecided},
		{"unknown account", f.admin, "no-such-user", domain.ErrNotFound},
	}
	for _, d := range accountsDecisions {
		for _, tt := range tests {
			t.Run(d.name+"/"+tt.name, func(t *testing.T) {
				requireErrorIs(t, d.call(t.Context(), f.s, tt.actor, tt.target), tt.want)
			})
		}
	}

	for u, want := range map[string]domain.UserStatus{
		active.ID: domain.UserStatusActive, unconfirmed.ID: domain.UserStatusPendingEmail,
		disabled.ID: domain.UserStatusDisabled, pending.ID: domain.UserStatusPendingApproval,
	} {
		if got := accountsUser(t, f.s, u).Status; got != want {
			t.Errorf("status of %s = %q, want %q", u, got, want)
		}
	}
	requireNoSideEffects(t, f.s)
}

// "Aprovar tudo" on an active person decides the items that wait for the admin
// and leaves the account alone: it is not pending, so there is no account line,
// no account audit entry and no notice to the Administradores (who saw the join
// request when it was made). A second call has nothing left to decide.
func TestApprovePersonActive(t *testing.T) {
	f := newAccountsFixture(t)
	owner := insertUser(t, f.s, "olga@agenda.test")
	p := insertUser(t, f.s, "ana@agenda.test")
	alfa, _ := insertNewCollective(t, f.s, "Alfa", p)
	beta := accountsApprovedCollective(t, f.s, "Beta", owner, f.admin)
	accountsRequest(t, f.s, p, beta, domain.RoleMember, domain.User{}, testNow)
	accountBefore := accountsUser(t, f.s, p.ID)
	now := testNow.Add(time.Hour)

	if err := ApprovePerson(t.Context(), f.s, testOrigin, f.admin, p.ID, now); err != nil {
		t.Fatalf("ApprovePerson: %v", err)
	}

	if got := accountsUser(t, f.s, p.ID); !reflect.DeepEqual(got, accountBefore) {
		t.Errorf("the account changed: %+v, was %+v", got, accountBefore)
	}
	gc := collectiveOf(t, f.s, alfa.ID)
	if gc.Status != domain.CollectiveStatusApproved || gc.ApprovedBy != f.admin.ID || !sameTime(gc.ApprovedAt, now) {
		t.Errorf("Alfa = %+v, want approved by %q at %v", gc, f.admin.ID, now)
	}
	if m := membershipOf(t, f.s, p.ID, alfa.ID); m.Status != domain.MembershipStatusApproved || m.AdminApprovedBy != f.admin.ID || !sameTime(m.DecidedAt, now) {
		t.Errorf("membership in Alfa = %+v, want approved by %q at %v", m, f.admin.ID, now)
	}
	mb := membershipOf(t, f.s, p.ID, beta.ID)
	if mb.Status != domain.MembershipStatusPending || mb.AdminApprovedBy != f.admin.ID || !sameTime(mb.AdminApprovedAt, now) || mb.OwnerApprovedAt != nil || mb.DecidedAt != nil {
		t.Errorf("request to Beta = %+v, want pending with only the admin side decided at %v", mb, now)
	}

	cards, err := AdminQueue(t.Context(), f.s, f.admin)
	if err != nil || len(cards) != 0 {
		t.Errorf("AdminQueue after everything was decided = %+v, %v, want no cards", cards, err)
	}
	requireErrorIs(t, ApprovePerson(t.Context(), f.s, testOrigin, f.admin, p.ID, now.Add(time.Hour)), ErrDecided)

	// One email, to the person and about the items only.
	msg := onlyMessage(t, outboxMessages(t, f.s))
	if msg.To != p.Email {
		t.Errorf("email to %q, want %q", msg.To, p.Email)
	}
	accountsRequireLines(t, msg.Text, p.Name,
		"- o coletivo Alfa: aprovado",
		"- seu pedido para entrar em Beta como Membro: aprovado pelo admin; falta um administrador do coletivo",
		"[→] "+testOrigin+"/coletivos")
	if strings.Contains(msg.Text, "sua conta") {
		t.Errorf("the email of an active person has an account line:\n%s", msg.Text)
	}
	accountsRequireActions(t, auditEntries(t, f.s), f.admin, p, map[domain.AuditAction]int{
		domain.AuditCollectiveApproved: 1,
		domain.AuditJoinAdminApproved:  1,
	})
}

// An active person with nothing that waits for the admin has nothing to decide:
// ErrDecided, and nothing is written or sent. That holds for a person with no
// items, for one whose join request the admin decided already, and for one whose
// only item is in a pending collective that somebody else created (not the
// person's to approve).
func TestApprovePersonNothingToDecide(t *testing.T) {
	f := newAccountsFixture(t)
	owner := insertUser(t, f.s, "olga@agenda.test")
	stranger := insertUser(t, f.s, "sara@agenda.test")
	bare := insertUser(t, f.s, "ana@agenda.test")
	decided := insertUser(t, f.s, "bia@agenda.test")
	foreign := insertUser(t, f.s, "caio@agenda.test")
	xis := accountsApprovedCollective(t, f.s, "Xis", owner, f.admin)
	beta := insertCollective(t, f.s, "Beta", stranger.ID)
	insertMembership(t, f.s, joinMembership(decided, xis, domain.RoleMember, f.admin, domain.User{}))
	accountsRequest(t, f.s, foreign, beta, domain.RoleMember, domain.User{}, testNow)
	xisBefore, betaBefore := collectiveOf(t, f.s, xis.ID), collectiveOf(t, f.s, beta.ID)
	decidedM, foreignM := membershipOf(t, f.s, decided.ID, xis.ID), membershipOf(t, f.s, foreign.ID, beta.ID)

	for _, u := range []domain.User{bare, decided, foreign} {
		requireErrorIs(t, ApprovePerson(t.Context(), f.s, testOrigin, f.admin, u.ID, testNow.Add(time.Hour)), ErrDecided)
		if got := accountsUser(t, f.s, u.ID).Status; got != domain.UserStatusActive {
			t.Errorf("status of %s = %q, want active", u.Email, got)
		}
	}

	if got := collectiveOf(t, f.s, xis.ID); !sameCollective(got, xisBefore) {
		t.Errorf("Xis changed: %+v, was %+v", got, xisBefore)
	}
	if got := collectiveOf(t, f.s, beta.ID); !sameCollective(got, betaBefore) {
		t.Errorf("Beta changed: %+v, was %+v", got, betaBefore)
	}
	if got := membershipOf(t, f.s, decided.ID, xis.ID); !reflect.DeepEqual(got, decidedM) {
		t.Errorf("membership in Xis changed: %+v, was %+v", got, decidedM)
	}
	if got := membershipOf(t, f.s, foreign.ID, beta.ID); !reflect.DeepEqual(got, foreignM) {
		t.Errorf("membership in Beta changed: %+v, was %+v", got, foreignM)
	}
	requireNoSideEffects(t, f.s)
}

// A pending collective the person has a request in but did not create is not
// the person's to approve: it is left as it is, with the request, and the rest is
// decided.
func TestApprovePersonSkipsForeignPendingCollective(t *testing.T) {
	f := newAccountsFixture(t)
	stranger := insertUser(t, f.s, "sara@agenda.test")
	p := accountsPending(t, f.s, "paula@agenda.test")
	alfa, _ := insertNewCollective(t, f.s, "Alfa", p)
	beta := insertCollective(t, f.s, "Beta", stranger.ID)
	accountsRequest(t, f.s, p, beta, domain.RoleMember, domain.User{}, testNow)
	betaBefore, betaM := collectiveOf(t, f.s, beta.ID), membershipOf(t, f.s, p.ID, beta.ID)
	now := testNow.Add(time.Hour)

	if err := ApprovePerson(t.Context(), f.s, testOrigin, f.admin, p.ID, now); err != nil {
		t.Fatalf("ApprovePerson: %v", err)
	}

	if got := accountsUser(t, f.s, p.ID).Status; got != domain.UserStatusActive {
		t.Errorf("account status = %q, want active", got)
	}
	if got := collectiveOf(t, f.s, alfa.ID).Status; got != domain.CollectiveStatusApproved {
		t.Errorf("Alfa status = %q, want approved", got)
	}
	if got := collectiveOf(t, f.s, beta.ID); !sameCollective(got, betaBefore) {
		t.Errorf("Beta changed: %+v, was %+v", got, betaBefore)
	}
	if got := membershipOf(t, f.s, p.ID, beta.ID); !reflect.DeepEqual(got, betaM) {
		t.Errorf("membership in Beta changed: %+v, was %+v", got, betaM)
	}
	msg := onlyMessage(t, outboxMessages(t, f.s))
	accountsRequireLines(t, msg.Text, "- sua conta: aprovado", "- o coletivo Alfa: aprovado")
	if strings.Contains(msg.Text, "Beta") {
		t.Errorf("the email names the collective that was not decided:\n%s", msg.Text)
	}
	accountsRequireActions(t, auditEntries(t, f.s), f.admin, p, map[domain.AuditAction]int{
		domain.AuditAccountApproved:    1,
		domain.AuditCollectiveApproved: 1,
	})
}

// Rejecting the account of a person who has a request in a pending collective
// they did not create deletes the account and its own collectives only: the
// other collective stays, and the store drops the person's membership in it with
// the account.
func TestRejectAccountSkipsForeignPendingCollective(t *testing.T) {
	f := newAccountsFixture(t)
	stranger := insertUser(t, f.s, "sara@agenda.test")
	p := accountsPending(t, f.s, "paula@agenda.test")
	alfa, _ := insertNewCollective(t, f.s, "Alfa", p)
	beta := insertCollective(t, f.s, "Beta", stranger.ID)
	accountsRequest(t, f.s, p, beta, domain.RoleMember, domain.User{}, testNow)
	betaBefore := collectiveOf(t, f.s, beta.ID)

	if err := RejectAccount(t.Context(), f.s, testOrigin, f.admin, p.ID, "", testNow.Add(time.Hour)); err != nil {
		t.Fatalf("RejectAccount: %v", err)
	}

	accountsRequireGone(t, f.s, p)
	accountsRequireCollectiveGone(t, f.s, alfa)
	if got := collectiveOf(t, f.s, beta.ID); !sameCollective(got, betaBefore) {
		t.Errorf("Beta changed: %+v, was %+v", got, betaBefore)
	}
	if _, err := f.s.Repos().Memberships.Get(t.Context(), p.ID, beta.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("request of the rejected person to Beta: err = %v, want ErrNotFound", err)
	}
	msg := onlyMessage(t, outboxMessages(t, f.s))
	accountsRequireLines(t, msg.Text, "- sua conta: recusado", "- o coletivo Alfa: recusado")
	if strings.Contains(msg.Text, "Beta") {
		t.Errorf("the email names the collective that was not decided:\n%s", msg.Text)
	}
	accountsRequireActions(t, auditEntries(t, f.s), f.admin, p, map[domain.AuditAction]int{
		domain.AuditAccountRejected:    1,
		domain.AuditCollectiveRejected: 1,
	})
}

// Four admins press "Aprovar tudo" on one person at once: one decision is taken,
// the other calls see it and answer ErrDecided, and the person gets one email.
func TestApprovePersonConcurrent(t *testing.T) {
	f := newAccountsFixture(t)
	other := insertAdmin(t, f.s, "bob@agenda.test")
	p := accountsPending(t, f.s, "paula@agenda.test")
	alfa, _ := insertNewCollective(t, f.s, "Alfa", p)
	actors := []domain.User{f.admin, other, f.admin, other}
	errs := make([]error, len(actors))

	var wg sync.WaitGroup
	for i, actor := range actors {
		wg.Go(func() {
			errs[i] = ApprovePerson(t.Context(), f.s, testOrigin, actor, p.ID, testNow.Add(time.Hour))
		})
	}
	wg.Wait()

	var done int
	for _, err := range errs {
		switch {
		case err == nil:
			done++
		case !errors.Is(err, ErrDecided):
			t.Errorf("a losing call returned %v, want ErrDecided", err)
		}
	}
	if done != 1 {
		t.Errorf("%d calls approved the person, want exactly 1 (errors: %v)", done, errs)
	}
	if got := accountsUser(t, f.s, p.ID).Status; got != domain.UserStatusActive {
		t.Errorf("account status = %q, want active", got)
	}
	if got := collectiveOf(t, f.s, alfa.ID).Status; got != domain.CollectiveStatusApproved {
		t.Errorf("Alfa status = %q, want approved", got)
	}
	onlyMessage(t, outboxMessages(t, f.s))
	if entries := auditEntries(t, f.s); len(entries) != 2 {
		t.Errorf("%d audit entries, want the account and the collective: %+v", len(entries), entries)
	}
}

// ApprovePerson and RejectAccount race for one person: exactly one decision is
// taken, and the loser sees it (a person who was approved is ErrDecided to the
// rejection; one who was deleted is ErrNotFound to the approval).
func TestApproveAndRejectAccountConcurrent(t *testing.T) {
	for round := 1; round <= 5; round++ {
		t.Run(fmt.Sprintf("round %d", round), func(t *testing.T) {
			f := newAccountsFixture(t)
			p := accountsPending(t, f.s, "paula@agenda.test")
			alfa, _ := insertNewCollective(t, f.s, "Alfa", p)
			var approveErr, rejectErr error
			var wg sync.WaitGroup
			wg.Go(func() {
				approveErr = ApprovePerson(t.Context(), f.s, testOrigin, f.admin, p.ID, testNow.Add(time.Hour))
			})
			wg.Go(func() {
				rejectErr = RejectAccount(t.Context(), f.s, testOrigin, f.admin, p.ID, "não", testNow.Add(time.Hour))
			})
			wg.Wait()

			msg := onlyMessage(t, outboxMessages(t, f.s))
			entries := auditEntries(t, f.s)
			switch {
			case approveErr == nil:
				requireErrorIs(t, rejectErr, ErrDecided)
				if accountsUser(t, f.s, p.ID).Status != domain.UserStatusActive || collectiveOf(t, f.s, alfa.ID).Status != domain.CollectiveStatusApproved {
					t.Errorf("the person and the collective should be approved")
				}
				accountsRequireLines(t, msg.Text, "- sua conta: aprovado", "- o coletivo Alfa: aprovado")
				accountsRequireActions(t, entries, f.admin, p, map[domain.AuditAction]int{domain.AuditAccountApproved: 1, domain.AuditCollectiveApproved: 1})
			case rejectErr == nil:
				requireErrorIs(t, approveErr, domain.ErrNotFound)
				accountsRequireGone(t, f.s, p)
				accountsRequireCollectiveGone(t, f.s, alfa)
				accountsRequireLines(t, msg.Text, "- sua conta: recusado", "- o coletivo Alfa: recusado")
				accountsRequireActions(t, entries, f.admin, p, map[domain.AuditAction]int{domain.AuditAccountRejected: 1, domain.AuditCollectiveRejected: 1})
			default:
				t.Fatalf("both calls failed: approve %v, reject %v", approveErr, rejectErr)
			}
		})
	}
}

// Rejecting an account deletes it and the collectives it created that still
// wait, with one email that holds the reason on every line. Its join requests go
// with the account (the store drops a deleted person's memberships) and are not
// decided one by one: no line in the email, no audit entry.
func TestRejectAccount(t *testing.T) {
	f := newAccountsFixture(t)
	owner := insertUser(t, f.s, "olga@agenda.test")
	p := accountsPending(t, f.s, "paula@agenda.test")
	bystander := accountsPending(t, f.s, "bia@agenda.test")
	alfa, _ := insertNewCollective(t, f.s, "Alfa", p)
	zeta, _ := insertNewCollective(t, f.s, "Zeta", p, func(c *domain.Collective) { c.Kind = domain.CollectiveKindProdutora })
	bystanderCollective, _ := insertNewCollective(t, f.s, "Omega", bystander)
	beta := accountsApprovedCollective(t, f.s, "Beta", owner, f.admin)
	gama := accountsApprovedCollective(t, f.s, "Gama", owner, f.admin)
	accountsRequest(t, f.s, p, beta, domain.RoleMember, domain.User{}, testNow)
	accountsRequest(t, f.s, p, gama, domain.RoleOwner, owner, testNow)
	accountsRequest(t, f.s, bystander, beta, domain.RoleMember, domain.User{}, testNow)
	bystanderBefore := collectiveOf(t, f.s, bystanderCollective.ID)
	now := testNow.Add(time.Hour)
	const reason = "cadastro duplicado"

	if err := RejectAccount(t.Context(), f.s, testOrigin, f.admin, p.ID, "  "+reason+"\r\n", now); err != nil {
		t.Fatalf("RejectAccount: %v", err)
	}

	accountsRequireGone(t, f.s, p)
	accountsRequireCollectiveGone(t, f.s, alfa)
	accountsRequireCollectiveGone(t, f.s, zeta)
	for _, c := range []domain.Collective{beta, gama} {
		if got := collectiveOf(t, f.s, c.ID); got.Status != domain.CollectiveStatusApproved {
			t.Errorf("%s = %+v, want it still approved", c.Name, got)
		}
		if _, err := f.s.Repos().Memberships.Get(t.Context(), p.ID, c.ID); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("request of the rejected person to %s: err = %v, want ErrNotFound", c.Name, err)
		}
		if _, err := f.s.Repos().Memberships.Get(t.Context(), owner.ID, c.ID); err != nil {
			t.Errorf("the owner's membership in %s: %v", c.Name, err)
		}
	}

	// Somebody else's account, collective and request stay.
	if got := accountsUser(t, f.s, bystander.ID).Status; got != domain.UserStatusPendingApproval {
		t.Errorf("bystander status = %q, want pending_approval", got)
	}
	if got := collectiveOf(t, f.s, bystanderCollective.ID); !sameCollective(got, bystanderBefore) {
		t.Errorf("bystander collective changed: %+v, was %+v", got, bystanderBefore)
	}
	if m := membershipOf(t, f.s, bystander.ID, beta.ID); m.Status != domain.MembershipStatusPending || m.AdminApprovedAt != nil {
		t.Errorf("bystander request changed: %+v", m)
	}

	// One email to the person, with the reason on each of the three lines and a
	// link to the signup page; the Administradores are told nothing.
	msg := onlyMessage(t, outboxMessages(t, f.s))
	if msg.To != p.Email {
		t.Errorf("email to %q, want %q", msg.To, p.Email)
	}
	accountsRequireLines(t, msg.Text,
		"- sua conta: recusado",
		"- o coletivo Alfa: recusado",
		"- a produtora Zeta: recusado",
		"[→] "+testOrigin+"/cadastro")
	if strings.Contains(msg.Text, "seu pedido") {
		t.Errorf("the email names a join request:\n%s", msg.Text)
	}
	if n := strings.Count(msg.Text, "motivo: "+reason+"\n"); n != 3 {
		t.Errorf("the reason appears %d times, want once on each of the 3 lines:\n%s", n, msg.Text)
	}

	entries := auditEntries(t, f.s)
	accountsRequireActions(t, entries, f.admin, p, map[domain.AuditAction]int{
		domain.AuditAccountRejected:    1,
		domain.AuditCollectiveRejected: 2,
	})
	for _, e := range entries {
		if e.Reason != reason {
			t.Errorf("audit entry %s has reason %q, want %q", e.Action, e.Reason, reason)
		}
	}

	// The person can sign up again, with the same address and the same names.
	again := accountsPending(t, f.s, p.Email)
	insertNewCollective(t, f.s, "Alfa", again)
}

// With no reason the email has no reason line, and a person with nothing else
// has the account line only.
func TestRejectAccountWithoutReason(t *testing.T) {
	f := newAccountsFixture(t)
	p := accountsPending(t, f.s, "paula@agenda.test")
	now := testNow.Add(time.Hour)

	if err := RejectAccount(t.Context(), f.s, testOrigin, f.admin, p.ID, "", now); err != nil {
		t.Fatalf("RejectAccount: %v", err)
	}

	accountsRequireGone(t, f.s, p)
	msg := onlyMessage(t, outboxMessages(t, f.s))
	accountsRequireLines(t, msg.Text, p.Name, "- sua conta: recusado", "[→] "+testOrigin+"/cadastro")
	if strings.Contains(msg.Text, "motivo:") {
		t.Errorf("a rejection with no reason has no reason line:\n%s", msg.Text)
	}
	entry := onlyAudit(t, auditEntries(t, f.s))
	if !sameAudit(entry, domain.AuditEntry{At: now, Action: domain.AuditAccountRejected, ActorID: f.admin.ID, ActorName: f.admin.Name, UserID: p.ID, UserName: p.Name}) {
		t.Errorf("audit entry = %+v", entry)
	}
}

// A reason that cannot be stored is refused, after the actor is checked and
// before anything is read or written.
func TestRejectAccountReason(t *testing.T) {
	f := newAccountsFixture(t)
	plain := insertUser(t, f.s, "bia@agenda.test")
	p := accountsPending(t, f.s, "paula@agenda.test")
	tooLong := strings.Repeat("a", domain.AuditTextMax+1)

	requireErrorIs(t, RejectAccount(t.Context(), f.s, testOrigin, plain, p.ID, tooLong, testNow), ErrNotAllowed)
	requireReasonError(t, RejectAccount(t.Context(), f.s, testOrigin, f.admin, p.ID, tooLong, testNow))
	requireReasonError(t, RejectAccount(t.Context(), f.s, testOrigin, f.admin, p.ID, "bad\x00reason", testNow))

	if got := accountsUser(t, f.s, p.ID).Status; got != domain.UserStatusPendingApproval {
		t.Errorf("account status = %q, want it untouched", got)
	}
	requireNoSideEffects(t, f.s)
}

// Blocking ends every session of the person, and only theirs. It queues no email:
// the person is shut out, not told a decision about a request.
func TestBlockAccount(t *testing.T) {
	f := newAccountsFixture(t)
	person := insertUser(t, f.s, "ana@agenda.test")
	bystander := insertUser(t, f.s, "bia@agenda.test")
	h1, h2 := accountsSession(t, f.s, person.ID, 1), accountsSession(t, f.s, person.ID, 2)
	h3 := accountsSession(t, f.s, bystander.ID, 3)
	now := testNow.Add(time.Hour)

	if err := BlockAccount(t.Context(), f.s, f.admin, person.ID, "  denúncia de golpe  ", now); err != nil {
		t.Fatalf("BlockAccount: %v", err)
	}

	got := accountsUser(t, f.s, person.ID)
	if got.Status != domain.UserStatusDisabled || !got.UpdatedAt.Truncate(time.Millisecond).Equal(now) {
		t.Errorf("account = status %q, updated at %v; want disabled at %v", got.Status, got.UpdatedAt, now)
	}
	if accountsSessionExists(t, f.s, h1) || accountsSessionExists(t, f.s, h2) {
		t.Error("a session of the blocked person is still there")
	}
	if !accountsSessionExists(t, f.s, h3) {
		t.Error("the session of somebody else was ended")
	}
	if got := accountsUser(t, f.s, bystander.ID).Status; got != domain.UserStatusActive {
		t.Errorf("bystander status = %q, want active", got)
	}
	if msgs := outboxMessages(t, f.s); len(msgs) != 0 {
		t.Errorf("%d emails queued, want none: %+v", len(msgs), msgs)
	}
	entry := onlyAudit(t, auditEntries(t, f.s))
	want := domain.AuditEntry{At: now, Action: domain.AuditAccountBlocked, ActorID: f.admin.ID, ActorName: f.admin.Name, UserID: person.ID, UserName: person.Name, Reason: "denúncia de golpe"}
	if !sameAudit(entry, want) {
		t.Errorf("audit entry = %+v, want %+v", entry, want)
	}
}

// An admin can block another admin, and the reason is optional.
func TestBlockAccountAnotherAdmin(t *testing.T) {
	f := newAccountsFixture(t)
	other := insertAdmin(t, f.s, "bob@agenda.test")

	if err := BlockAccount(t.Context(), f.s, f.admin, other.ID, "", testNow.Add(time.Hour)); err != nil {
		t.Fatalf("BlockAccount: %v", err)
	}

	if got := accountsUser(t, f.s, other.ID); got.Status != domain.UserStatusDisabled || !got.IsAdmin {
		t.Errorf("the other admin = status %q, admin %v; want disabled, still an admin", got.Status, got.IsAdmin)
	}
	if entry := onlyAudit(t, auditEntries(t, f.s)); entry.Action != domain.AuditAccountBlocked || entry.Reason != "" {
		t.Errorf("audit entry = %+v", entry)
	}
}

func TestBlockAccountRefused(t *testing.T) {
	f := newAccountsFixture(t)
	plain := insertUser(t, f.s, "bia@agenda.test")
	active := insertUser(t, f.s, "ana@agenda.test")
	hash := accountsSession(t, f.s, active.ID, 1)
	adminHash := accountsSession(t, f.s, f.admin.ID, 2)
	disabled := insertUser(t, f.s, "dan@agenda.test", accountsStatus(domain.UserStatusDisabled))
	pending := accountsPending(t, f.s, "paula@agenda.test")
	unconfirmed := accountsPending(t, f.s, "ugo@agenda.test", accountsStatus(domain.UserStatusPendingEmail))
	tooLong := strings.Repeat("a", domain.AuditTextMax+1)

	tests := []struct {
		name   string
		actor  domain.User
		target string
		reason string
		want   error
	}{
		{"actor is not an admin", plain, active.ID, "", ErrNotAllowed},
		{"actor is not an admin, bad reason", plain, active.ID, tooLong, ErrNotAllowed},
		{"an admin cannot block themself", f.admin, f.admin.ID, "", ErrSelf},
		{"an admin cannot block themself, bad reason", f.admin, f.admin.ID, tooLong, ErrSelf},
		{"account is already disabled", f.admin, disabled.ID, "", ErrDecided},
		{"account awaits approval", f.admin, pending.ID, "", ErrDecided},
		{"email is not confirmed", f.admin, unconfirmed.ID, "", ErrDecided},
		{"unknown account", f.admin, "no-such-user", "", domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireErrorIs(t, BlockAccount(t.Context(), f.s, tt.actor, tt.target, tt.reason, testNow.Add(time.Hour)), tt.want)
		})
	}
	t.Run("bad reason", func(t *testing.T) {
		requireReasonError(t, BlockAccount(t.Context(), f.s, f.admin, active.ID, tooLong, testNow.Add(time.Hour)))
	})

	for _, u := range []domain.User{active, f.admin} {
		if got := accountsUser(t, f.s, u.ID).Status; got != domain.UserStatusActive {
			t.Errorf("status of %s = %q, want active", u.Email, got)
		}
	}
	for status, u := range map[domain.UserStatus]domain.User{
		domain.UserStatusDisabled: disabled, domain.UserStatusPendingApproval: pending, domain.UserStatusPendingEmail: unconfirmed,
	} {
		if got := accountsUser(t, f.s, u.ID).Status; got != status {
			t.Errorf("status of %s = %q, want %q", u.Email, got, status)
		}
	}
	if !accountsSessionExists(t, f.s, hash) || !accountsSessionExists(t, f.s, adminHash) {
		t.Error("a refused block ended a session")
	}
	requireNoSideEffects(t, f.s)
}

func TestUnblockAccount(t *testing.T) {
	f := newAccountsFixture(t)
	person := insertUser(t, f.s, "ana@agenda.test", accountsStatus(domain.UserStatusDisabled))
	now := testNow.Add(time.Hour)

	if err := UnblockAccount(t.Context(), f.s, f.admin, person.ID, now); err != nil {
		t.Fatalf("UnblockAccount: %v", err)
	}

	got := accountsUser(t, f.s, person.ID)
	if got.Status != domain.UserStatusActive || !got.UpdatedAt.Truncate(time.Millisecond).Equal(now) {
		t.Errorf("account = status %q, updated at %v; want active at %v", got.Status, got.UpdatedAt, now)
	}
	if msgs := outboxMessages(t, f.s); len(msgs) != 0 {
		t.Errorf("%d emails queued, want none: %+v", len(msgs), msgs)
	}
	entry := onlyAudit(t, auditEntries(t, f.s))
	want := domain.AuditEntry{At: now, Action: domain.AuditAccountUnblocked, ActorID: f.admin.ID, ActorName: f.admin.Name, UserID: person.ID, UserName: person.Name}
	if !sameAudit(entry, want) {
		t.Errorf("audit entry = %+v, want %+v", entry, want)
	}
}

func TestUnblockAccountRefused(t *testing.T) {
	f := newAccountsFixture(t)
	plain := insertUser(t, f.s, "bia@agenda.test")
	disabled := insertUser(t, f.s, "dan@agenda.test", accountsStatus(domain.UserStatusDisabled))
	active := insertUser(t, f.s, "ana@agenda.test")
	pending := accountsPending(t, f.s, "paula@agenda.test")
	unconfirmed := accountsPending(t, f.s, "ugo@agenda.test", accountsStatus(domain.UserStatusPendingEmail))

	tests := []struct {
		name   string
		actor  domain.User
		target string
		want   error
	}{
		{"actor is not an admin", plain, disabled.ID, ErrNotAllowed},
		{"account is already active", f.admin, active.ID, ErrDecided},
		{"account awaits approval", f.admin, pending.ID, ErrDecided},
		{"email is not confirmed", f.admin, unconfirmed.ID, ErrDecided},
		{"unknown account", f.admin, "no-such-user", domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requireErrorIs(t, UnblockAccount(t.Context(), f.s, tt.actor, tt.target, testNow.Add(time.Hour)), tt.want)
		})
	}

	for status, u := range map[domain.UserStatus]domain.User{
		domain.UserStatusDisabled: disabled, domain.UserStatusActive: active,
		domain.UserStatusPendingApproval: pending, domain.UserStatusPendingEmail: unconfirmed,
	} {
		if got := accountsUser(t, f.s, u.ID).Status; got != status {
			t.Errorf("status of %s = %q, want %q", u.Email, got, status)
		}
	}
	requireNoSideEffects(t, f.s)
}

// accountsCards describes the cards as "name[collective,collective]", in order.
func accountsCards(cards []PersonCard) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		names := make([]string, len(c.Items))
		for j, item := range c.Items {
			names[j] = item.CollectiveName
		}
		out[i] = c.User.Name + "[" + strings.Join(names, ",") + "]"
	}
	return out
}

// The queue holds one card per person, in one order whatever the account's
// state: by the person's oldest request that waits for the admin, or by when the
// account was created when there is none; the items inside a card by request
// time. An active person whose request is older than every account that awaits
// approval comes first.
func TestAdminQueue(t *testing.T) {
	f := newAccountsFixture(t)
	owner := insertUser(t, f.s, "olga@agenda.test")
	beta := accountsApprovedCollective(t, f.s, "Beta", owner, f.admin)
	gama := accountsApprovedCollective(t, f.s, "Gama", owner, f.admin)
	delta := accountsApprovedCollective(t, f.s, "Delta", owner, f.admin)

	// p2 signed up after p1 and has nothing but the account.
	p2 := accountsPending(t, f.s, "p2@agenda.test", func(u *domain.User) { u.CreatedAt = testNow.Add(time.Minute) })
	p1 := accountsPending(t, f.s, "p1@agenda.test")
	insertNewCollective(t, f.s, "Alfa", p1)
	accountsRequest(t, f.s, p1, beta, domain.RoleMember, domain.User{}, testNow.Add(2*time.Minute))

	// veteran is active and has waited longer than everybody, mid sits between p1
	// and p2.
	veteran := insertUser(t, f.s, "veteran@agenda.test")
	accountsRequest(t, f.s, veteran, gama, domain.RoleMember, domain.User{}, testNow.Add(-time.Hour))
	mid := insertUser(t, f.s, "mid@agenda.test")
	accountsRequest(t, f.s, mid, gama, domain.RoleMember, domain.User{}, testNow.Add(30*time.Second))

	// late is the older account but has waited less than early.
	late := insertUser(t, f.s, "late@agenda.test")
	early := insertUser(t, f.s, "early@agenda.test", func(u *domain.User) { u.CreatedAt = testNow.Add(time.Hour) })
	accountsRequest(t, f.s, late, beta, domain.RoleMember, domain.User{}, testNow.Add(30*time.Minute))
	accountsRequest(t, f.s, early, gama, domain.RoleMember, domain.User{}, testNow.Add(20*time.Minute))
	accountsRequest(t, f.s, early, beta, domain.RoleMember, domain.User{}, testNow.Add(10*time.Minute))

	// None of these belongs in the queue.
	decided := insertUser(t, f.s, "decided@agenda.test")
	insertMembership(t, f.s, joinMembership(decided, delta, domain.RoleMember, f.admin, domain.User{}))
	idle := insertUser(t, f.s, "idle@agenda.test")
	insertMembership(t, f.s, joinMembership(idle, delta, domain.RoleMember, f.admin, owner))
	blocked := insertUser(t, f.s, "blocked@agenda.test", accountsStatus(domain.UserStatusDisabled))
	accountsRequest(t, f.s, blocked, delta, domain.RoleMember, domain.User{}, testNow)
	unconfirmed := insertUser(t, f.s, "unconfirmed@agenda.test", accountsStatus(domain.UserStatusPendingEmail))
	insertNewCollective(t, f.s, "Omega", unconfirmed)

	cards, err := AdminQueue(t.Context(), f.s, f.admin)
	if err != nil {
		t.Fatalf("AdminQueue: %v", err)
	}
	want := []string{
		veteran.Name + "[Gama]",
		p1.Name + "[Alfa,Beta]",
		mid.Name + "[Gama]",
		p2.Name + "[]",
		early.Name + "[Beta,Gama]",
		late.Name + "[Beta]",
	}
	if got := accountsCards(cards); !reflect.DeepEqual(got, want) {
		t.Fatalf("queue = %q, want %q", got, want)
	}
	for i, wantPending := range []bool{false, true, false, true, false, false} {
		if got := cards[i].AccountPending(); got != wantPending {
			t.Errorf("card %d (%s): AccountPending = %v, want %v", i, cards[i].User.Name, got, wantPending)
		}
	}
	for _, c := range cards {
		for _, item := range c.Items {
			if item.UserID != c.User.ID || item.Status != domain.MembershipStatusPending || item.AdminApprovedAt != nil {
				t.Errorf("card of %s holds an item that is not one of their open requests: %+v", c.User.Name, item)
			}
		}
	}
	if items := cards[1].Items; len(items) != 2 || items[0].CollectiveStatus != domain.CollectiveStatusPending || items[1].CollectiveStatus != domain.CollectiveStatusApproved {
		t.Errorf("items of %s = %+v, want the collective they asked for (pending) and then Beta (approved)", cards[1].User.Name, items)
	}
}

func TestAdminQueueEmpty(t *testing.T) {
	f := newAccountsFixture(t)
	insertUser(t, f.s, "ana@agenda.test")

	cards, err := AdminQueue(t.Context(), f.s, f.admin)
	if err != nil || len(cards) != 0 {
		t.Errorf("AdminQueue = %+v, %v, want no cards", cards, err)
	}
}

func TestAdminQueueRefused(t *testing.T) {
	f := newAccountsFixture(t)
	plain := insertUser(t, f.s, "bia@agenda.test")
	disabledAdmin := insertUser(t, f.s, "dora@agenda.test", func(u *domain.User) { u.IsAdmin, u.Status = true, domain.UserStatusDisabled })
	accountsPending(t, f.s, "paula@agenda.test")

	for name, actor := range map[string]domain.User{"not an admin": plain, "disabled admin": disabledAdmin} {
		t.Run(name, func(t *testing.T) {
			cards, err := AdminQueue(t.Context(), f.s, actor)
			requireErrorIs(t, err, ErrNotAllowed)
			if len(cards) != 0 {
				t.Errorf("cards = %+v, want none", cards)
			}
		})
	}
}

// failingCollectives is a domain.Collectives whose ByID fails with err. It
// embeds the interface only to satisfy it: the tests call nothing else.
type failingCollectives struct {
	domain.Collectives
	err error
}

func (f failingCollectives) ByID(context.Context, string) (domain.Collective, error) {
	return domain.Collective{}, f.err
}

// A membership always has its collective, so a missing one is a broken
// invariant: it is none of the outcomes a caller shows the person as a normal
// refusal (ErrNotAllowed, ErrDecided) or a missing target (domain.ErrNotFound).
// Any other failure of the read keeps its cause.
func TestAccountsCollectiveReadFailures(t *testing.T) {
	ctx := context.Background()
	v := domain.MembershipView{Membership: domain.Membership{CollectiveID: "c1"}}

	_, err := accountsCollective(ctx, domain.Repos{Collectives: failingCollectives{err: domain.ErrNotFound}}, v, "step")
	if err == nil || !isNoneOf(err, ErrNotAllowed, ErrDecided, domain.ErrNotFound) {
		t.Fatalf("a missing collective: err = %v, want an error that is none of the errors of the package", err)
	}

	boom := errors.New("boom")
	_, err = accountsCollective(ctx, domain.Repos{Collectives: failingCollectives{err: boom}}, v, "step")
	requireErrorIs(t, err, boom)
}
