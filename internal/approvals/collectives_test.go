package approvals

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// collectiveFixture is a store with a site admin, the person who asked for a
// collective and that collective, named Alfa.
type collectiveFixture struct {
	s       domain.Store
	admin   domain.User
	creator domain.User
	c       domain.Collective
}

// newPendingCollective is the state a signup that creates a collective leaves:
// the collective awaits the admin, and the creator's membership is an owner
// membership with only the owner side approved.
func newPendingCollective(t *testing.T) collectiveFixture {
	t.Helper()
	s := newTestStore(t)
	admin := insertAdmin(t, s, "ada@agenda.test")
	creator := insertUser(t, s, "ana@agenda.test")
	c, _ := insertNewCollective(t, s, "Alfa", creator)
	return collectiveFixture{s: s, admin: admin, creator: creator, c: c}
}

// newApprovedCollective is an approved collective with the given blocks and its
// creator as an approved owner.
func newApprovedCollective(t *testing.T, eventsBlocked, locked bool) collectiveFixture {
	t.Helper()
	s := newTestStore(t)
	admin := insertAdmin(t, s, "ada@agenda.test")
	creator := insertUser(t, s, "ana@agenda.test")
	approvedAt := testNow
	c := insertCollective(t, s, "Alfa", creator.ID, func(c *domain.Collective) {
		c.Status = domain.CollectiveStatusApproved
		c.ApprovedBy, c.ApprovedAt = admin.ID, &approvedAt
		c.EventsBlocked, c.Locked = eventsBlocked, locked
	})
	insertMembership(t, s, domain.Membership{
		UserID: creator.ID, CollectiveID: c.ID, Role: domain.RoleOwner, Status: domain.MembershipStatusApproved,
		AdminApprovedBy: admin.ID, AdminApprovedAt: &approvedAt,
		OwnerApprovedBy: creator.ID, OwnerApprovedAt: &approvedAt,
		RequestedAt: testNow, DecidedAt: &approvedAt,
	})
	return collectiveFixture{s: s, admin: admin, creator: creator, c: c}
}

// sameCollective reports whether two reads of a collective hold the same
// mutable state.
func sameCollective(a, b domain.Collective) bool {
	return a.Name == b.Name && a.NameKey == b.NameKey && a.Status == b.Status && a.ApprovedBy == b.ApprovedBy &&
		a.EventsBlocked == b.EventsBlocked && a.Locked == b.Locked &&
		a.UpdatedAt.Truncate(time.Millisecond).Equal(b.UpdatedAt.Truncate(time.Millisecond)) &&
		(a.ApprovedAt == nil) == (b.ApprovedAt == nil) && (a.ApprovedAt == nil || sameTime(a.ApprovedAt, *b.ApprovedAt))
}

// requireNoSideEffects fails the test when an email is queued or an audit entry
// written. It reads each once, so call it once per test.
func requireNoSideEffects(t *testing.T, s domain.Store) {
	t.Helper()
	if msgs := outboxMessages(t, s); len(msgs) != 0 {
		t.Errorf("%d emails queued, want none: %+v", len(msgs), msgs)
	}
	if entries := auditEntries(t, s); len(entries) != 0 {
		t.Errorf("%d audit entries, want none: %+v", len(entries), entries)
	}
}

// isNoneOf reports whether err matches none of the given errors.
func isNoneOf(err error, targets ...error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return false
		}
	}
	return true
}

func TestApproveCollective(t *testing.T) {
	f := newPendingCollective(t)
	now := testNow.Add(time.Hour)

	if err := ApproveCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, now); err != nil {
		t.Fatalf("ApproveCollective: %v", err)
	}

	c := collectiveOf(t, f.s, f.c.ID)
	if c.Status != domain.CollectiveStatusApproved || c.ApprovedBy != f.admin.ID || !sameTime(c.ApprovedAt, now) {
		t.Errorf("collective = status %q, approved by %q at %v; want approved by %q at %v", c.Status, c.ApprovedBy, c.ApprovedAt, f.admin.ID, now)
	}
	if !c.UpdatedAt.Truncate(time.Millisecond).Equal(now) {
		t.Errorf("UpdatedAt = %v, want %v", c.UpdatedAt, now)
	}
	if c.EventsBlocked || c.Locked || c.Name != "Alfa" || c.CreatedBy != f.creator.ID {
		t.Errorf("collective changed more than its approval: %+v", c)
	}

	// The creator's membership becomes approved with the collective: the admin
	// side is the admin's decision, and the owner side stays the creator's own.
	m := membershipOf(t, f.s, f.creator.ID, f.c.ID)
	if m.Role != domain.RoleOwner || m.Status != domain.MembershipStatusApproved {
		t.Errorf("membership = role %q, status %q, want owner, approved", m.Role, m.Status)
	}
	if m.AdminApprovedBy != f.admin.ID || !sameTime(m.AdminApprovedAt, now) {
		t.Errorf("admin side = %q at %v, want %q at %v", m.AdminApprovedBy, m.AdminApprovedAt, f.admin.ID, now)
	}
	if m.OwnerApprovedBy != f.creator.ID || !sameTime(m.OwnerApprovedAt, testNow) {
		t.Errorf("owner side = %q at %v, want the creator's own at %v", m.OwnerApprovedBy, m.OwnerApprovedAt, testNow)
	}
	if !sameTime(m.DecidedAt, now) || !m.RequestedAt.Truncate(time.Millisecond).Equal(testNow) {
		t.Errorf("DecidedAt = %v, RequestedAt = %v, want %v and %v", m.DecidedAt, m.RequestedAt, now, testNow)
	}
	// The point of the approval: the creator can now publish events.
	if ok, err := f.s.Repos().Memberships.UserCanCreateEvents(t.Context(), f.creator.ID); err != nil || !ok {
		t.Errorf("UserCanCreateEvents = %v, %v, want true", ok, err)
	}

	msg := onlyMessage(t, outboxMessages(t, f.s))
	if msg.To != f.creator.Email {
		t.Errorf("email to %q, want %q", msg.To, f.creator.Email)
	}
	for _, want := range []string{f.creator.Name, "o coletivo Alfa: aprovado", testOrigin + "/coletivos"} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("email lacks %q:\n%s", want, msg.Text)
		}
	}
	if strings.Contains(msg.Text, "motivo:") {
		t.Errorf("an approval has no reason:\n%s", msg.Text)
	}

	want := domain.AuditEntry{
		At: now, Action: domain.AuditCollectiveApproved,
		ActorID: f.admin.ID, ActorName: f.admin.Name,
		UserID: f.creator.ID, UserName: f.creator.Name,
		CollectiveID: f.c.ID, CollectiveName: "Alfa",
	}
	if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, want) {
		t.Errorf("audit entry = %+v, want %+v", got, want)
	}
}

// The collective's approval is the admin side of the creator's membership; the
// membership becomes approved only when its owner side is set too. A creator
// whose membership lacks the owner side (not what a signup leaves, but
// approveCollectiveTx also serves flows that build the membership otherwise)
// gets the admin side and stays pending.
func TestApproveCollectiveCreatorWithoutOwnerSide(t *testing.T) {
	s := newTestStore(t)
	admin := insertAdmin(t, s, "ada@agenda.test")
	creator := insertUser(t, s, "ana@agenda.test")
	c := insertCollective(t, s, "Alfa", creator.ID)
	insertMembership(t, s, domain.Membership{
		UserID: creator.ID, CollectiveID: c.ID, Role: domain.RoleOwner, Status: domain.MembershipStatusPending,
		RequestedAt: testNow,
	})
	now := testNow.Add(time.Hour)

	if err := ApproveCollective(t.Context(), s, testOrigin, admin, c.ID, now); err != nil {
		t.Fatalf("ApproveCollective: %v", err)
	}

	if got := collectiveOf(t, s, c.ID); got.Status != domain.CollectiveStatusApproved {
		t.Errorf("collective status = %q, want approved", got.Status)
	}
	m := membershipOf(t, s, creator.ID, c.ID)
	if m.Status != domain.MembershipStatusPending || m.DecidedAt != nil {
		t.Errorf("membership = status %q, DecidedAt %v, want pending and not decided", m.Status, m.DecidedAt)
	}
	if m.AdminApprovedBy != admin.ID || !sameTime(m.AdminApprovedAt, now) {
		t.Errorf("admin side = %q at %v, want %q at %v", m.AdminApprovedBy, m.AdminApprovedAt, admin.ID, now)
	}
	if m.OwnerApprovedBy != "" || m.OwnerApprovedAt != nil {
		t.Errorf("owner side = %q at %v, want it still open", m.OwnerApprovedBy, m.OwnerApprovedAt)
	}
	if ok, err := s.Repos().Memberships.UserCanCreateEvents(t.Context(), creator.ID); err != nil || ok {
		t.Errorf("UserCanCreateEvents = %v, %v, want false: one side is still open", ok, err)
	}
}

func TestApproveCollectiveRefused(t *testing.T) {
	// want is the sentinel the call returns; nil means the call returns an error
	// that is none of the sentinels (a broken invariant the web layer shows as a
	// server error, not as "not found").
	tests := []struct {
		name  string
		setup func(t *testing.T, f *collectiveFixture) (actor domain.User, id string)
		want  error
	}{
		{"actor is not an admin", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.creator, f.c.ID
		}, ErrNotAllowed},
		{"actor is a disabled admin", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			a := f.admin
			a.Status = domain.UserStatusDisabled
			return a, f.c.ID
		}, ErrNotAllowed},
		{"a non-admin learns nothing about an unknown collective", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.creator, "no-such-collective"
		}, ErrNotAllowed},
		{"unknown collective", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.admin, "no-such-collective"
		}, domain.ErrNotFound},
		{"collective already approved", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			c := f.c
			approvedAt := testNow
			c.Status, c.ApprovedBy, c.ApprovedAt = domain.CollectiveStatusApproved, f.admin.ID, &approvedAt
			if err := f.s.Repos().Collectives.Update(t.Context(), c); err != nil {
				t.Fatalf("approve the fixture: %v", err)
			}
			return f.admin, f.c.ID
		}, ErrDecided},
		{"creator awaits the account approval", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			if err := f.s.Repos().Users.SetStatus(t.Context(), f.creator.ID, domain.UserStatusPendingApproval, testNow); err != nil {
				t.Fatalf("set status: %v", err)
			}
			return f.admin, f.c.ID
		}, ErrNotAllowed},
		{"creator is disabled", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			if err := f.s.Repos().Users.SetStatus(t.Context(), f.creator.ID, domain.UserStatusDisabled, testNow); err != nil {
				t.Fatalf("set status: %v", err)
			}
			return f.admin, f.c.ID
		}, ErrNotAllowed},
		{"creator was deleted", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			if err := f.s.Repos().Users.Delete(t.Context(), f.creator.ID); err != nil {
				t.Fatalf("delete creator: %v", err)
			}
			return f.admin, f.c.ID
		}, ErrNotAllowed},
		{"creator has no membership", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			if err := f.s.Repos().Memberships.Delete(t.Context(), f.creator.ID, f.c.ID); err != nil {
				t.Fatalf("delete membership: %v", err)
			}
			return f.admin, f.c.ID
		}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPendingCollective(t)
			actor, id := tt.setup(t, &f)
			before := collectiveOf(t, f.s, f.c.ID)

			err := ApproveCollective(t.Context(), f.s, testOrigin, actor, id, testNow.Add(time.Hour))
			if tt.want != nil {
				requireErrorIs(t, err, tt.want)
			} else if err == nil || !isNoneOf(err, ErrNotAllowed, ErrDecided, ErrLocked, ErrMaster, ErrLastOwner, ErrSelf, domain.ErrNotFound) {
				t.Fatalf("err = %v, want an error that is none of the sentinels", err)
			}

			if after := collectiveOf(t, f.s, f.c.ID); !sameCollective(before, after) {
				t.Errorf("collective changed: before %+v, after %+v", before, after)
			}
			requireNoSideEffects(t, f.s)
		})
	}
}

func TestApproveCollectiveTwice(t *testing.T) {
	f := newPendingCollective(t)
	if err := ApproveCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, testNow.Add(time.Hour)); err != nil {
		t.Fatalf("first call: %v", err)
	}
	err := ApproveCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, testNow.Add(2*time.Hour))
	requireErrorIs(t, err, ErrDecided)

	// The second call changed nothing: one email, one audit entry, the first time.
	onlyMessage(t, outboxMessages(t, f.s))
	onlyAudit(t, auditEntries(t, f.s))
	if c := collectiveOf(t, f.s, f.c.ID); !sameTime(c.ApprovedAt, testNow.Add(time.Hour)) {
		t.Errorf("ApprovedAt = %v, want the first call's time", c.ApprovedAt)
	}
}

// Two admins press the button at the same time: one decision is taken, the
// other call sees it and answers ErrDecided, and the person gets one email.
func TestApproveCollectiveConcurrent(t *testing.T) {
	f := newPendingCollective(t)
	other := insertAdmin(t, f.s, "bob@agenda.test")
	actors := []domain.User{f.admin, other, f.admin, other}
	errs := make([]error, len(actors))

	var wg sync.WaitGroup
	for i, actor := range actors {
		wg.Go(func() {
			errs[i] = ApproveCollective(t.Context(), f.s, testOrigin, actor, f.c.ID, testNow.Add(time.Hour))
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
		t.Errorf("%d calls approved the collective, want exactly 1 (errors: %v)", done, errs)
	}
	onlyMessage(t, outboxMessages(t, f.s))
	onlyAudit(t, auditEntries(t, f.s))
}

func TestRejectCollective(t *testing.T) {
	f := newPendingCollective(t)
	bystander := insertUser(t, f.s, "bia@agenda.test")
	now := testNow.Add(time.Hour)

	if err := RejectCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, "o nome se confunde com outro coletivo", now); err != nil {
		t.Fatalf("RejectCollective: %v", err)
	}

	// The collective and the creator's membership are gone; the people stay.
	if _, err := f.s.Repos().Collectives.ByID(t.Context(), f.c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("collective after the rejection: err = %v, want ErrNotFound", err)
	}
	if _, err := f.s.Repos().Memberships.Get(t.Context(), f.creator.ID, f.c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("membership after the rejection: err = %v, want ErrNotFound", err)
	}
	for _, u := range []domain.User{f.creator, f.admin, bystander} {
		if _, err := f.s.Repos().Users.ByID(t.Context(), u.ID); err != nil {
			t.Errorf("user %s after the rejection: %v", u.Name, err)
		}
	}
	// The person can ask again, with the same name.
	insertNewCollective(t, f.s, "Alfa", f.creator)

	msg := onlyMessage(t, outboxMessages(t, f.s))
	if msg.To != f.creator.Email {
		t.Errorf("email to %q, want %q", msg.To, f.creator.Email)
	}
	// The link is the login page: the creator may still await the account
	// approval, and the collective the page would show is gone.
	for _, want := range []string{f.creator.Name, "o coletivo Alfa: recusado", "motivo: o nome se confunde com outro coletivo", testOrigin + "/entrar"} {
		if !strings.Contains(msg.Text, want) {
			t.Errorf("email lacks %q:\n%s", want, msg.Text)
		}
	}

	want := domain.AuditEntry{
		At: now, Action: domain.AuditCollectiveRejected,
		ActorID: f.admin.ID, ActorName: f.admin.Name,
		UserID: f.creator.ID, UserName: f.creator.Name,
		CollectiveID: f.c.ID, CollectiveName: "Alfa",
		Reason: "o nome se confunde com outro coletivo",
	}
	if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, want) {
		t.Errorf("audit entry = %+v, want %+v", got, want)
	}
}

// A produtora is called "a produtora", not "o coletivo", in the decision email.
func TestCollectiveDecisionNamesTheProdutora(t *testing.T) {
	for _, tc := range []struct {
		name    string
		decide  func(t *testing.T, f collectiveFixture) error
		outcome string
	}{
		{"approve", func(t *testing.T, f collectiveFixture) error {
			return ApproveCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, testNow.Add(time.Hour))
		}, "a produtora Alfa: aprovado"},
		{"reject", func(t *testing.T, f collectiveFixture) error {
			return RejectCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, "", testNow.Add(time.Hour))
		}, "a produtora Alfa: recusado"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			admin := insertAdmin(t, s, "ada@agenda.test")
			creator := insertUser(t, s, "ana@agenda.test")
			c, _ := insertNewCollective(t, s, "Alfa", creator, func(c *domain.Collective) { c.Kind = domain.CollectiveKindProdutora })

			if err := tc.decide(t, collectiveFixture{s: s, admin: admin, creator: creator, c: c}); err != nil {
				t.Fatalf("decide: %v", err)
			}
			msg := onlyMessage(t, outboxMessages(t, s))
			if !strings.Contains(msg.Text, tc.outcome) || strings.Contains(msg.Text, "o coletivo") {
				t.Errorf("email lacks %q or still says \"o coletivo\":\n%s", tc.outcome, msg.Text)
			}
		})
	}
}

func TestRejectCollectiveReason(t *testing.T) {
	t.Run("the reason is optional", func(t *testing.T) {
		f := newPendingCollective(t)
		if err := RejectCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, "  \n ", testNow); err != nil {
			t.Fatalf("RejectCollective: %v", err)
		}
		if msg := onlyMessage(t, outboxMessages(t, f.s)); !strings.Contains(msg.Text, "o coletivo Alfa: recusado") || strings.Contains(msg.Text, "motivo:") {
			t.Errorf("email = %s", msg.Text)
		}
		if e := onlyAudit(t, auditEntries(t, f.s)); e.Reason != "" {
			t.Errorf("audit reason = %q, want empty", e.Reason)
		}
	})

	t.Run("the reason is stored as CheckReason cleans it", func(t *testing.T) {
		f := newPendingCollective(t)
		if err := RejectCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, "  não\r\npode  ", testNow); err != nil {
			t.Fatalf("RejectCollective: %v", err)
		}
		if e := onlyAudit(t, auditEntries(t, f.s)); e.Reason != "não\npode" {
			t.Errorf("audit reason = %q, want %q", e.Reason, "não\npode")
		}
	})

	for name, reason := range map[string]string{
		"too long":          strings.Repeat("a", domain.AuditTextMax+1),
		"control character": "a\x00b",
	} {
		t.Run("a reason that is "+name+" changes nothing", func(t *testing.T) {
			f := newPendingCollective(t)
			before := collectiveOf(t, f.s, f.c.ID)
			err := RejectCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, reason, testNow)
			requireReasonError(t, err)
			if after := collectiveOf(t, f.s, f.c.ID); !sameCollective(before, after) {
				t.Errorf("collective changed: before %+v, after %+v", before, after)
			}
			membershipOf(t, f.s, f.creator.ID, f.c.ID)
			requireNoSideEffects(t, f.s)
		})
	}
}

func TestRejectCollectiveRefused(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, f *collectiveFixture) (actor domain.User, id string)
		reason string
		want   error // nil: a reason error
	}{
		{"actor is not an admin", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.creator, f.c.ID
		}, "", ErrNotAllowed},
		{"the admin check comes before the reason check", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.creator, f.c.ID
		}, "a\x00b", ErrNotAllowed},
		{"a non-admin learns nothing about an unknown collective", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.creator, "no-such-collective"
		}, "", ErrNotAllowed},
		{"the reason check comes before the lookup", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.admin, "no-such-collective"
		}, "a\x00b", nil},
		{"unknown collective", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.admin, "no-such-collective"
		}, "", domain.ErrNotFound},
		{"collective already approved", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			c := f.c
			approvedAt := testNow
			c.Status, c.ApprovedBy, c.ApprovedAt = domain.CollectiveStatusApproved, f.admin.ID, &approvedAt
			if err := f.s.Repos().Collectives.Update(t.Context(), c); err != nil {
				t.Fatalf("approve the fixture: %v", err)
			}
			return f.admin, f.c.ID
		}, "", ErrDecided},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPendingCollective(t)
			actor, id := tt.setup(t, &f)
			before := collectiveOf(t, f.s, f.c.ID)

			err := RejectCollective(t.Context(), f.s, testOrigin, actor, id, tt.reason, testNow.Add(time.Hour))
			if tt.want != nil {
				requireErrorIs(t, err, tt.want)
			} else {
				requireReasonError(t, err)
			}

			if after := collectiveOf(t, f.s, f.c.ID); !sameCollective(before, after) {
				t.Errorf("collective changed: before %+v, after %+v", before, after)
			}
			membershipOf(t, f.s, f.creator.ID, f.c.ID)
			requireNoSideEffects(t, f.s)
		})
	}
}

// A collective whose creator was deleted can still be rejected: nobody is left
// to tell, so no email is queued, and the audit entry names nobody.
func TestRejectCollectiveCreatorDeleted(t *testing.T) {
	f := newPendingCollective(t)
	if err := f.s.Repos().Users.Delete(t.Context(), f.creator.ID); err != nil {
		t.Fatalf("delete creator: %v", err)
	}
	now := testNow.Add(time.Hour)

	if err := RejectCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, "sem dono", now); err != nil {
		t.Fatalf("RejectCollective: %v", err)
	}
	if _, err := f.s.Repos().Collectives.ByID(t.Context(), f.c.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("collective after the rejection: err = %v, want ErrNotFound", err)
	}
	if msgs := outboxMessages(t, f.s); len(msgs) != 0 {
		t.Errorf("%d emails queued for a deleted creator: %+v", len(msgs), msgs)
	}
	want := domain.AuditEntry{
		At: now, Action: domain.AuditCollectiveRejected,
		ActorID: f.admin.ID, ActorName: f.admin.Name,
		CollectiveID: f.c.ID, CollectiveName: "Alfa", Reason: "sem dono",
	}
	if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, want) {
		t.Errorf("audit entry = %+v, want %+v", got, want)
	}
}

func TestRejectCollectiveTwice(t *testing.T) {
	f := newPendingCollective(t)
	if err := RejectCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, "", testNow.Add(time.Hour)); err != nil {
		t.Fatalf("first call: %v", err)
	}
	err := RejectCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, "", testNow.Add(2*time.Hour))
	requireErrorIs(t, err, domain.ErrNotFound)
	onlyMessage(t, outboxMessages(t, f.s))
	onlyAudit(t, auditEntries(t, f.s))
}

// An approval and a rejection race for one collective: exactly one decision is
// taken, and the loser sees it (a collective that was approved is ErrDecided to
// the rejection; one that was deleted is ErrNotFound to the approval).
func TestApproveAndRejectCollectiveConcurrent(t *testing.T) {
	for round := 1; round <= 5; round++ {
		t.Run(fmt.Sprintf("round %d", round), func(t *testing.T) {
			f := newPendingCollective(t)
			var approveErr, rejectErr error
			var wg sync.WaitGroup
			wg.Go(func() {
				approveErr = ApproveCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, testNow.Add(time.Hour))
			})
			wg.Go(func() {
				rejectErr = RejectCollective(t.Context(), f.s, testOrigin, f.admin, f.c.ID, "não", testNow.Add(time.Hour))
			})
			wg.Wait()

			_, readErr := f.s.Repos().Collectives.ByID(t.Context(), f.c.ID)
			msg := onlyMessage(t, outboxMessages(t, f.s))
			entry := onlyAudit(t, auditEntries(t, f.s))
			switch {
			case approveErr == nil:
				requireErrorIs(t, rejectErr, ErrDecided)
				if readErr != nil || collectiveOf(t, f.s, f.c.ID).Status != domain.CollectiveStatusApproved {
					t.Errorf("approved collective: read err = %v", readErr)
				}
				if entry.Action != domain.AuditCollectiveApproved || !strings.Contains(msg.Text, "aprovado") {
					t.Errorf("audit action = %q, email:\n%s", entry.Action, msg.Text)
				}
			case rejectErr == nil:
				requireErrorIs(t, approveErr, domain.ErrNotFound)
				if !errors.Is(readErr, domain.ErrNotFound) {
					t.Errorf("rejected collective: read err = %v, want ErrNotFound", readErr)
				}
				if entry.Action != domain.AuditCollectiveRejected || !strings.Contains(msg.Text, "recusado") {
					t.Errorf("audit action = %q, email:\n%s", entry.Action, msg.Text)
				}
			default:
				t.Fatalf("both calls failed: approve %v, reject %v", approveErr, rejectErr)
			}
		})
	}
}

// approveCollectiveTx and rejectCollectiveTx are what the admin queue's "approve
// everything" reuses: they write inside the caller's transaction and add their
// line to the caller's decision, so the person gets one email for every item.
func TestCollectiveTxHelpersShareTheCallersDecision(t *testing.T) {
	link := testOrigin + "/coletivos"

	tests := []struct {
		name  string
		run   func(ctx context.Context, r domain.Repos, f collectiveFixture, c domain.Collective, d *decision, now time.Time) error
		lines []string
		after func(t *testing.T, f collectiveFixture)
	}{
		{
			name: "approve",
			run: func(ctx context.Context, r domain.Repos, f collectiveFixture, c domain.Collective, d *decision, now time.Time) error {
				return approveCollectiveTx(ctx, r, f.admin, f.creator, c, d, now)
			},
			lines: []string{"sua conta: aprovado", "o coletivo Alfa: aprovado"},
			after: func(t *testing.T, f collectiveFixture) {
				if c := collectiveOf(t, f.s, f.c.ID); c.Status != domain.CollectiveStatusApproved {
					t.Errorf("status = %q, want approved", c.Status)
				}
			},
		},
		{
			name: "reject",
			run: func(ctx context.Context, r domain.Repos, f collectiveFixture, c domain.Collective, d *decision, now time.Time) error {
				return rejectCollectiveTx(ctx, r, f.admin, f.creator, c, "não", d, now)
			},
			lines: []string{"sua conta: aprovado", "o coletivo Alfa: recusado", "motivo: não"},
			after: func(t *testing.T, f collectiveFixture) {
				if _, err := f.s.Repos().Collectives.ByID(t.Context(), f.c.ID); !errors.Is(err, domain.ErrNotFound) {
					t.Errorf("collective: err = %v, want ErrNotFound", err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newPendingCollective(t)
			now := testNow.Add(time.Hour)
			err := f.s.WithTx(t.Context(), func(ctx context.Context, r domain.Repos) error {
				c, err := r.Collectives.ByID(ctx, f.c.ID)
				if err != nil {
					return err
				}
				d := newDecision(f.creator.Email, f.creator.Name)
				d.add(whatAccount, mail.DecisionApproved, "")
				if err := tt.run(ctx, r, f, c, d, now); err != nil {
					return err
				}
				return d.send(ctx, r, link, now)
			})
			if err != nil {
				t.Fatalf("transaction: %v", err)
			}
			tt.after(t, f)
			msg := onlyMessage(t, outboxMessages(t, f.s))
			for _, want := range tt.lines {
				if !strings.Contains(msg.Text, want) {
					t.Errorf("email lacks %q:\n%s", want, msg.Text)
				}
			}
			// The helper writes the collective's audit entry and leaves the
			// account's to its caller.
			onlyAudit(t, auditEntries(t, f.s))
		})
	}
}

func TestSetCollectiveBlocks(t *testing.T) {
	tests := []struct {
		name                   string
		eventsBlocked, locked  bool // before
		wantEvents, wantLocked bool // asked
		wantDetail             string
		reason, wantReason     string
	}{
		{"block the events", false, false, true, false, "eventos bloqueados", "abuso", "abuso"},
		{"lock the collective", false, false, false, true, "membros e nome congelados", "", ""},
		{"both at once", false, false, true, true, "eventos bloqueados; membros e nome congelados", "  disputa\r\ninterna ", "disputa\ninterna"},
		{"free the events", true, true, false, true, "eventos liberados", "", ""},
		{"unlock the collective", true, true, true, false, "membros e nome liberados", "", ""},
		{"free both", true, true, false, false, "eventos liberados; membros e nome liberados", "resolvido", "resolvido"},
		{"lock a collective whose events are blocked", true, false, true, true, "membros e nome congelados", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newApprovedCollective(t, tt.eventsBlocked, tt.locked)
			now := testNow.Add(time.Hour)

			if err := SetCollectiveBlocks(t.Context(), f.s, f.admin, f.c.ID, tt.wantEvents, tt.wantLocked, tt.reason, now); err != nil {
				t.Fatalf("SetCollectiveBlocks: %v", err)
			}

			c := collectiveOf(t, f.s, f.c.ID)
			if c.EventsBlocked != tt.wantEvents || c.Locked != tt.wantLocked {
				t.Errorf("blocks = events %v, locked %v, want %v, %v", c.EventsBlocked, c.Locked, tt.wantEvents, tt.wantLocked)
			}
			if !c.UpdatedAt.Truncate(time.Millisecond).Equal(now) {
				t.Errorf("UpdatedAt = %v, want %v", c.UpdatedAt, now)
			}
			// Nothing else about the collective moves.
			if c.Status != domain.CollectiveStatusApproved || c.ApprovedBy != f.admin.ID || !sameTime(c.ApprovedAt, testNow) || c.Name != "Alfa" {
				t.Errorf("collective changed more than its blocks: %+v", c)
			}
			if m := membershipOf(t, f.s, f.creator.ID, f.c.ID); m.Status != domain.MembershipStatusApproved || m.Role != domain.RoleOwner {
				t.Errorf("membership = %+v, want it untouched", m)
			}

			want := domain.AuditEntry{
				At: now, Action: domain.AuditCollectiveBlocks,
				ActorID: f.admin.ID, ActorName: f.admin.Name,
				CollectiveID: f.c.ID, CollectiveName: "Alfa",
				Detail: tt.wantDetail, Reason: tt.wantReason,
			}
			if got := onlyAudit(t, auditEntries(t, f.s)); !sameAudit(got, want) {
				t.Errorf("audit entry = %+v, want %+v", got, want)
			}
			// A block is not a decision about the person's request: no email.
			if msgs := outboxMessages(t, f.s); len(msgs) != 0 {
				t.Errorf("%d emails queued, want none: %+v", len(msgs), msgs)
			}
		})
	}
}

// A call that asks for what the collective already has records nothing, so the
// log shows changes only.
func TestSetCollectiveBlocksNoChange(t *testing.T) {
	f := newApprovedCollective(t, true, false)
	before := collectiveOf(t, f.s, f.c.ID)

	if err := SetCollectiveBlocks(t.Context(), f.s, f.admin, f.c.ID, true, false, "de novo", testNow.Add(time.Hour)); err != nil {
		t.Fatalf("SetCollectiveBlocks: %v", err)
	}
	if after := collectiveOf(t, f.s, f.c.ID); !sameCollective(before, after) {
		t.Errorf("collective changed: before %+v, after %+v", before, after)
	}
	requireNoSideEffects(t, f.s)
}

func TestSetCollectiveBlocksRefused(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(t *testing.T, f *collectiveFixture) (actor domain.User, id string)
		reason string
		want   error // nil: a reason error
	}{
		{"actor is not an admin", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.creator, f.c.ID
		}, "", ErrNotAllowed},
		{"the admin check comes before the reason check", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.creator, f.c.ID
		}, "a\x00b", ErrNotAllowed},
		{"a reason with a control character", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.admin, f.c.ID
		}, "a\x00b", nil},
		{"a reason that is too long", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.admin, f.c.ID
		}, strings.Repeat("a", domain.AuditTextMax+1), nil},
		{"the reason check comes before the lookup", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.admin, "no-such-collective"
		}, "a\x00b", nil},
		{"unknown collective", func(t *testing.T, f *collectiveFixture) (domain.User, string) {
			return f.admin, "no-such-collective"
		}, "", domain.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newApprovedCollective(t, false, false)
			actor, id := tt.setup(t, &f)
			before := collectiveOf(t, f.s, f.c.ID)

			err := SetCollectiveBlocks(t.Context(), f.s, actor, id, true, true, tt.reason, testNow.Add(time.Hour))
			if tt.want != nil {
				requireErrorIs(t, err, tt.want)
			} else {
				requireReasonError(t, err)
			}

			if after := collectiveOf(t, f.s, f.c.ID); !sameCollective(before, after) {
				t.Errorf("collective changed: before %+v, after %+v", before, after)
			}
			requireNoSideEffects(t, f.s)
		})
	}
}

// A collective that is still pending takes no block, and a refused call leaves
// it pending.
func TestSetCollectiveBlocksOnPendingCollective(t *testing.T) {
	f := newPendingCollective(t)
	before := collectiveOf(t, f.s, f.c.ID)

	err := SetCollectiveBlocks(t.Context(), f.s, f.admin, f.c.ID, true, true, "", testNow.Add(time.Hour))
	requireErrorIs(t, err, ErrNotAllowed)
	if after := collectiveOf(t, f.s, f.c.ID); !sameCollective(before, after) {
		t.Errorf("collective changed: before %+v, after %+v", before, after)
	}
	requireNoSideEffects(t, f.s)
}
