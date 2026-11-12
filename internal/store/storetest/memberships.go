package storetest

import (
	"fmt"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

func normViews(vs []domain.MembershipView) []domain.MembershipView {
	out := make([]domain.MembershipView, len(vs))
	for i, v := range vs {
		out[i] = normView(v)
	}
	return out
}

func (e env) views(userID string) []domain.MembershipView {
	e.t.Helper()
	got, err := e.r.Memberships.ListByUser(e.ctx, userID)
	noErr(e.t, "ListByUser("+userID+")", err)
	return got
}

func (e env) ownerEmails(collectiveID string) []string {
	e.t.Helper()
	got, err := e.r.Memberships.OwnerEmails(e.ctx, collectiveID)
	noErr(e.t, "OwnerEmails("+collectiveID+")", err)
	return got
}

func (e env) canCreateEvents(userID string) bool {
	e.t.Helper()
	got, err := e.r.Memberships.UserCanCreateEvents(e.ctx, userID)
	noErr(e.t, "UserCanCreateEvents("+userID+")", err)
	return got
}

// RunMemberships runs the contract of domain.Memberships.
func RunMemberships(t *testing.T, newStore NewStore) {
	t.Helper()
	runSubtests(t, newStore, []subtest{
		{"create stores every field and ListByUser shows it with the collective's fields", func(t *testing.T, e env) {
			ana, admin := e.user("ana"), e.user("admin")
			c := e.collective("Casa Azul", produtora, cApproved)
			m := e.member(ana, c, roleOwner, mApproved, func(m *domain.Membership) {
				m.AdminApprovedBy = admin.ID
				m.AdminApprovedAt = ptr(at(time.Minute))
				m.OwnerApprovedBy = admin.ID
				m.OwnerApprovedAt = ptr(at(2 * time.Minute))
				m.RequestedAt = at(-time.Hour)
				m.DecidedAt = ptr(at(3 * time.Minute))
			})
			want := []domain.MembershipView{{Membership: m, CollectiveName: "Casa Azul", CollectiveKind: produtora, CollectiveStatus: cApproved}}
			wantEqual(t, "ListByUser", normViews(e.views(ana.ID)), normViews(want))
		}},
		{"create validates the membership", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved)
			for _, tc := range []struct {
				field  string
				mutate func(*domain.Membership)
			}{
				{"user_id", func(m *domain.Membership) { m.UserID = "" }},
				{"collective_id", func(m *domain.Membership) { m.CollectiveID = "" }},
				{"role", func(m *domain.Membership) { m.Role = "bogus" }},
				{"status", func(m *domain.Membership) { m.Status = "bogus" }},
			} {
				m := newMembership(ana, c, roleMember, mPending, tc.mutate)
				wantFieldErrors(t, "create with a bad "+tc.field, e.r.Memberships.Create(e.ctx, m), tc.field)
			}
			if got := e.views(ana.ID); len(got) != 0 {
				t.Errorf("ListByUser after the failed creates = %v, want none", got)
			}
		}},
		{"create validates before it checks references", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved)
			m := newMembership(ana, c, "bogus", mPending, func(m *domain.Membership) { m.UserID = unknownRefs[0] })
			wantFieldErrors(t, "create", e.r.Memberships.Create(e.ctx, m), "role")
		}},
		{"create refuses a user, collective or approver that does not exist", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved)
			for _, id := range unknownRefs {
				for name, mutate := range map[string]func(*domain.Membership){
					"user":           func(m *domain.Membership) { m.UserID = id },
					"collective":     func(m *domain.Membership) { m.CollectiveID = id },
					"admin approver": func(m *domain.Membership) { m.AdminApprovedBy = id },
					"owner approver": func(m *domain.Membership) { m.OwnerApprovedBy = id },
				} {
					m := newMembership(ana, c, roleMember, mPending, mutate)
					isErr(t, fmt.Sprintf("create with an unknown %s %q", name, id), e.r.Memberships.Create(e.ctx, m), domain.ErrNotFound)
				}
			}
			if got := e.views(ana.ID); len(got) != 0 {
				t.Errorf("ListByUser after the failed creates = %v, want none", got)
			}
			e.member(ana, c, roleMember, mPending) // nothing was stored, so the pair is free
		}},
		{"create checks references before duplicates", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved)
			e.member(ana, c, roleMember, mPending)
			m := newMembership(ana, c, roleMember, mPending, func(m *domain.Membership) { m.AdminApprovedBy = unknownRefs[1] })
			isErr(t, "create", e.r.Memberships.Create(e.ctx, m), domain.ErrNotFound)
		}},
		{"create refuses a second membership for the same user and collective, whatever its role or status", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c, other := e.collective("Casa Azul", coletivo, cApproved), e.collective("Casa Verde", produtora, cApproved)
			first := e.member(ana, c, roleMember, mRejected)
			for _, role := range []domain.Role{roleMember, roleOwner} {
				for _, status := range []domain.MembershipStatus{mPending, mApproved, mRejected} {
					m := newMembership(ana, c, role, status)
					isErr(t, fmt.Sprintf("create as %s %s", role, status), e.r.Memberships.Create(e.ctx, m), domain.ErrAlreadyMember)
				}
			}
			got := e.views(ana.ID)
			if len(got) != 1 {
				t.Fatalf("ListByUser has %d memberships, want 1", len(got))
			}
			wantEqual(t, "the stored membership", normMembership(got[0].Membership), normMembership(first))

			e.member(bia, c, roleMember, mPending)    // another user, same collective
			e.member(ana, other, roleOwner, mPending) // same user, another collective
		}},
		{"create lets exactly one of concurrent calls for the same pair win", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved)
			errs := parallelErr(10, func(int) error {
				return e.r.Memberships.Create(e.ctx, newMembership(ana, c, roleMember, mPending))
			})
			wantOneWinner(t, "create", errs, domain.ErrAlreadyMember)
			if got := e.views(ana.ID); len(got) != 1 {
				t.Errorf("ListByUser has %d memberships, want 1", len(got))
			}
		}},
		{"ListByUser returns every status, ordered by the collective's name key", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			zeta := e.collective("Zeta Clube", coletivo, cApproved)
			alfa := e.collective("Alfa Clube", produtora, cPending)
			acao := e.collective("Ação Clube", coletivo, cRejected)
			beta := e.collective("beta clube", coletivo, cApproved)
			e.member(ana, zeta, roleOwner, mApproved)
			e.member(ana, alfa, roleMember, mPending)
			e.member(ana, acao, roleMember, mRejected)
			e.member(ana, beta, roleMember, mApproved)
			e.member(bia, beta, roleOwner, mApproved)

			got := e.views(ana.ID)
			var names []string
			for _, v := range got {
				names = append(names, v.CollectiveName)
			}
			wantStrings(t, "collective names", names, "Ação Clube", "Alfa Clube", "beta clube", "Zeta Clube")
			if len(got) != 4 {
				t.FailNow()
			}
			wantStatus := []domain.MembershipStatus{mRejected, mPending, mApproved, mApproved}
			wantCollective := []domain.CollectiveStatus{cRejected, cPending, cApproved, cApproved}
			for i, v := range got {
				if v.Status != wantStatus[i] || v.CollectiveStatus != wantCollective[i] || v.UserID != ana.ID {
					t.Errorf("view %d = %+v, want membership status %s, collective status %s and user %s", i, v, wantStatus[i], wantCollective[i], ana.ID)
				}
			}
			if got := e.views(bia.ID); len(got) != 1 || got[0].CollectiveKind != coletivo {
				t.Errorf("ListByUser(bia) = %v, want her one membership", got)
			}
		}},
		{"ListByUser is empty for a user without memberships or an unknown user", func(t *testing.T, e env) {
			ana := e.user("ana")
			if got := e.views(ana.ID); len(got) != 0 {
				t.Errorf("ListByUser(ana) = %v, want none", got)
			}
			for _, id := range unknownIDs {
				if got := e.views(id); len(got) != 0 {
					t.Errorf("ListByUser(%q) = %v, want none", id, got)
				}
			}
		}},
		{"OwnerEmails returns the active approved owners in ascending order", func(t *testing.T, e env) {
			c := e.collective("Casa Azul", coletivo, cApproved)
			other := e.collective("Casa Verde", produtora, cApproved)
			zeca, ana, bia := e.user("zeca"), e.user("ana"), e.user("bia")
			e.member(zeca, c, roleOwner, mApproved)
			e.member(ana, c, roleOwner, mApproved)
			e.member(bia, c, roleOwner, mApproved)
			e.member(e.user("dede", func(u *domain.User) { u.Status = uDisabled }), c, roleOwner, mApproved)
			e.member(e.user("pedro", func(u *domain.User) { u.Status = uPendingApproval }), c, roleOwner, mApproved)
			e.member(e.user("paula"), c, roleOwner, mPending)
			e.member(e.user("rita"), c, roleOwner, mRejected)
			e.member(e.user("mario"), c, roleMember, mApproved)
			e.member(e.user("olga"), other, roleOwner, mApproved)

			wantStrings(t, "OwnerEmails", e.ownerEmails(c.ID), "ana@example.com", "bia@example.com", "zeca@example.com")
			wantStrings(t, "OwnerEmails of the other collective", e.ownerEmails(other.ID), "olga@example.com")
		}},
		{"OwnerEmails does not look at the collective's status", func(t *testing.T, e env) {
			ana := e.user("ana")
			for i, status := range []domain.CollectiveStatus{cPending, cRejected} {
				c := e.collective(fmt.Sprintf("Coletivo %d", i), coletivo, status)
				e.member(ana, c, roleOwner, mApproved)
				wantStrings(t, "OwnerEmails of a "+string(status)+" collective", e.ownerEmails(c.ID), "ana@example.com")
			}
		}},
		{"OwnerEmails is empty for a collective without owners or an unknown collective", func(t *testing.T, e env) {
			c := e.collective("Casa Azul", coletivo, cApproved)
			wantStrings(t, "OwnerEmails", e.ownerEmails(c.ID))
			for _, id := range unknownIDs {
				wantStrings(t, fmt.Sprintf("OwnerEmails(%q)", id), e.ownerEmails(id))
			}
		}},
		{"UserCanCreateEvents needs an active user with an approved owner membership in an approved collective", func(t *testing.T, e env) {
			for i, tc := range []struct {
				name       string
				userStatus domain.UserStatus
				role       domain.Role
				membership domain.MembershipStatus
				collective domain.CollectiveStatus
				want       bool
			}{
				{"all in order", uActive, roleOwner, mApproved, cApproved, true},
				{"disabled user", uDisabled, roleOwner, mApproved, cApproved, false},
				{"user pending approval", uPendingApproval, roleOwner, mApproved, cApproved, false},
				{"user pending email", uPendingEmail, roleOwner, mApproved, cApproved, false},
				{"member, not owner", uActive, roleMember, mApproved, cApproved, false},
				{"pending membership", uActive, roleOwner, mPending, cApproved, false},
				{"rejected membership", uActive, roleOwner, mRejected, cApproved, false},
				{"pending collective", uActive, roleOwner, mApproved, cPending, false},
				{"rejected collective", uActive, roleOwner, mApproved, cRejected, false},
			} {
				u := e.user(fmt.Sprintf("user%d", i), func(u *domain.User) { u.Status = tc.userStatus })
				c := e.collective(fmt.Sprintf("Coletivo %d", i), coletivo, tc.collective)
				e.member(u, c, tc.role, tc.membership)
				if got := e.canCreateEvents(u.ID); got != tc.want {
					t.Errorf("%s: UserCanCreateEvents = %v, want %v", tc.name, got, tc.want)
				}
			}
		}},
		{"UserCanCreateEvents looks across all of the user's memberships", func(t *testing.T, e env) {
			pending := e.collective("Coletivo Pendente", coletivo, cPending)
			approved := e.collective("Coletivo Aprovado", coletivo, cApproved)

			both := e.user("ana")
			e.member(both, pending, roleOwner, mApproved)
			e.member(both, approved, roleOwner, mApproved)
			if !e.canCreateEvents(both.ID) {
				t.Error("an owner of an approved collective, who also owns a pending one, cannot create events")
			}

			split := e.user("bia")
			e.member(split, pending, roleOwner, mApproved)
			e.member(split, approved, roleMember, mApproved)
			if e.canCreateEvents(split.ID) {
				t.Error("a user who owns only a pending collective and is a member of an approved one can create events")
			}

			none := e.user("caio")
			if e.canCreateEvents(none.ID) {
				t.Error("a user without memberships can create events")
			}
		}},
		{"UserCanCreateEvents is false for an unknown user", func(t *testing.T, e env) {
			for _, id := range unknownIDs {
				if e.canCreateEvents(id) {
					t.Errorf("UserCanCreateEvents(%q) = true", id)
				}
			}
		}},
	})
}
