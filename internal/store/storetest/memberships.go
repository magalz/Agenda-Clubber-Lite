package storetest

import (
	"fmt"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

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

func (e env) membership(userID, collectiveID string) domain.Membership {
	e.t.Helper()
	got, err := e.r.Memberships.Get(e.ctx, userID, collectiveID)
	noErr(e.t, "Get("+userID+", "+collectiveID+")", err)
	return got
}

// decided fills both approval sides and the decision time, and moves RequestedAt
// away from the fixture's default, so a field that is not stored shows.
func decided(adminBy, ownerBy string) func(*domain.Membership) {
	return func(m *domain.Membership) {
		m.AdminApprovedBy = adminBy
		m.AdminApprovedAt = ptr(at(time.Minute))
		m.OwnerApprovedBy = ownerBy
		m.OwnerApprovedAt = ptr(at(2 * time.Minute))
		m.RequestedAt = at(-time.Hour)
		m.DecidedAt = ptr(at(3 * time.Minute))
	}
}

// blockEvents makes a collective fixture have its events blocked.
func blockEvents(c *domain.Collective) { c.EventsBlocked = true }

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
			wantEqual(t, "ListByUser", normAll(e.views(ana.ID), normView), normAll(want, normView))
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
		{"UserCanCreateEvents needs an active user with an approved owner membership in an approved collective whose events are not blocked", func(t *testing.T, e env) {
			for i, tc := range []struct {
				name       string
				userStatus domain.UserStatus
				role       domain.Role
				membership domain.MembershipStatus
				collective domain.CollectiveStatus
				blocked    bool
				want       bool
			}{
				// A collective made without EventsBlocked has no events_blocked
				// field in the database: it counts as not blocked.
				{"all in order", uActive, roleOwner, mApproved, cApproved, false, true},
				{"disabled user", uDisabled, roleOwner, mApproved, cApproved, false, false},
				{"user pending approval", uPendingApproval, roleOwner, mApproved, cApproved, false, false},
				{"user pending email", uPendingEmail, roleOwner, mApproved, cApproved, false, false},
				{"member, not owner", uActive, roleMember, mApproved, cApproved, false, false},
				{"pending membership", uActive, roleOwner, mPending, cApproved, false, false},
				{"rejected membership", uActive, roleOwner, mRejected, cApproved, false, false},
				{"pending collective", uActive, roleOwner, mApproved, cPending, false, false},
				{"rejected collective", uActive, roleOwner, mApproved, cRejected, false, false},
				{"events blocked", uActive, roleOwner, mApproved, cApproved, true, false},
			} {
				u := e.user(fmt.Sprintf("user%d", i), func(u *domain.User) { u.Status = tc.userStatus })
				c := e.collective(fmt.Sprintf("Coletivo %d", i), coletivo, tc.collective, func(c *domain.Collective) { c.EventsBlocked = tc.blocked })
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
		{"UserCanCreateEvents is false when the only qualifying collective has its events blocked, true when another one qualifies", func(t *testing.T, e env) {
			blocked := e.collective("Coletivo Bloqueado", coletivo, cApproved, blockEvents)
			open := e.collective("Coletivo Aberto", coletivo, cApproved)

			ana := e.user("ana")
			e.member(ana, blocked, roleOwner, mApproved)
			if e.canCreateEvents(ana.ID) {
				t.Error("an owner whose only qualifying collective has its events blocked can create events")
			}
			e.member(ana, open, roleOwner, mApproved)
			if !e.canCreateEvents(ana.ID) {
				t.Error("an owner of a blocked collective who also owns an unblocked one cannot create events")
			}

			bia := e.user("bia")
			e.member(bia, blocked, roleOwner, mApproved)
			e.member(bia, open, roleMember, mApproved)
			if e.canCreateEvents(bia.ID) {
				t.Error("a user who owns only a blocked collective and is a member of an unblocked one can create events")
			}
		}},
		{"UserCanCreateEvents is false for an unknown user", func(t *testing.T, e env) {
			for _, id := range unknownIDs {
				if e.canCreateEvents(id) {
					t.Errorf("UserCanCreateEvents(%q) = true", id)
				}
			}
		}},
		{"Get returns the membership with every field", func(t *testing.T, e env) {
			ana, admin, boss := e.user("ana"), e.user("admin"), e.user("boss")
			c, other := e.collective("Casa Azul", produtora, cApproved), e.collective("Casa Verde", coletivo, cApproved)
			m := e.member(ana, c, roleOwner, mApproved, decided(admin.ID, boss.ID))
			e.member(ana, other, roleMember, mPending)
			wantEqual(t, "Get", normMembership(e.membership(ana.ID, c.ID)), normMembership(m))
		}},
		{"Get is ErrNotFound for a pair without a membership and for IDs the store never issued", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Casa Azul", coletivo, cApproved)
			e.member(bia, c, roleMember, mPending)
			_, err := e.r.Memberships.Get(e.ctx, ana.ID, c.ID)
			isErr(t, "Get of a user who has no membership in the collective", err, domain.ErrNotFound)
			for _, id := range unknownIDs {
				for name, pair := range map[string][2]string{
					"user":       {id, c.ID},
					"collective": {bia.ID, id},
					"both":       {id, id},
				} {
					_, err := e.r.Memberships.Get(e.ctx, pair[0], pair[1])
					isErr(t, fmt.Sprintf("Get with an unknown %s %q", name, id), err, domain.ErrNotFound)
				}
			}
		}},
		{"Update stores every mutable field", func(t *testing.T, e env) {
			ana, admin, boss := e.user("ana"), e.user("admin"), e.user("boss")
			c := e.collective("Casa Azul", coletivo, cApproved)
			e.member(ana, c, roleMember, mPending)
			want := newMembership(ana, c, roleOwner, mApproved, decided(admin.ID, boss.ID))
			want.RequestedAt = base // Update never changes it
			noErr(t, "Update", e.r.Memberships.Update(e.ctx, want))
			wantEqual(t, "Get after Update", normMembership(e.membership(ana.ID, c.ID)), normMembership(want))
		}},
		{"Update never changes RequestedAt, whatever the membership it is given carries", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved)
			e.member(ana, c, roleMember, mPending, func(m *domain.Membership) { m.RequestedAt = at(-time.Hour) })
			m := newMembership(ana, c, roleMember, mRejected, func(m *domain.Membership) { m.RequestedAt = at(time.Hour) })
			noErr(t, "Update", e.r.Memberships.Update(e.ctx, m))
			got := e.membership(ana.ID, c.ID)
			sameTime(t, "RequestedAt", got.RequestedAt, at(-time.Hour))
			if got.Status != mRejected {
				t.Errorf("Status = %s, want %s: the rest of the update was not stored", got.Status, mRejected)
			}
		}},
		{"Update clears the approver sides and the decision time that the membership leaves empty", func(t *testing.T, e env) {
			ana, admin, boss := e.user("ana"), e.user("admin"), e.user("boss")
			c := e.collective("Casa Azul", coletivo, cApproved)
			e.member(ana, c, roleOwner, mApproved, decided(admin.ID, boss.ID))

			// The admin side and the decision go; the Administrador side stays.
			m := newMembership(ana, c, roleOwner, mPending, func(m *domain.Membership) {
				m.OwnerApprovedBy = boss.ID
				m.OwnerApprovedAt = ptr(at(2 * time.Minute))
				m.RequestedAt = at(-time.Hour)
			})
			noErr(t, "Update clearing the admin side", e.r.Memberships.Update(e.ctx, m))
			wantEqual(t, "Get after clearing the admin side", normMembership(e.membership(ana.ID, c.ID)), normMembership(m))

			// Now the other side goes too.
			m = newMembership(ana, c, roleMember, mPending, func(m *domain.Membership) { m.RequestedAt = at(-time.Hour) })
			noErr(t, "Update clearing the other side", e.r.Memberships.Update(e.ctx, m))
			wantEqual(t, "Get after clearing both sides", normMembership(e.membership(ana.ID, c.ID)), normMembership(m))
		}},
		{"Update changes only the membership of that user in that collective", func(t *testing.T, e env) {
			ana, bia, admin := e.user("ana"), e.user("bia"), e.user("admin")
			c, other := e.collective("Casa Azul", coletivo, cApproved), e.collective("Casa Verde", coletivo, cApproved)
			e.member(ana, c, roleMember, mPending)
			sameUser := e.member(ana, other, roleMember, mPending)
			sameCollective := e.member(bia, c, roleMember, mPending)
			m := newMembership(ana, c, roleOwner, mApproved, func(m *domain.Membership) { m.AdminApprovedBy = admin.ID })
			noErr(t, "Update", e.r.Memberships.Update(e.ctx, m))
			wantEqual(t, "the same user's other membership", normMembership(e.membership(ana.ID, other.ID)), normMembership(sameUser))
			wantEqual(t, "the other user's membership", normMembership(e.membership(bia.ID, c.ID)), normMembership(sameCollective))
		}},
		{"Update validates the membership and stores nothing", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved)
			before := e.member(ana, c, roleMember, mPending)
			for _, tc := range []struct {
				field  string
				mutate func(*domain.Membership)
			}{
				{"user_id", func(m *domain.Membership) { m.UserID = "" }},
				{"collective_id", func(m *domain.Membership) { m.CollectiveID = "" }},
				{"role", func(m *domain.Membership) { m.Role = "bogus" }},
				{"status", func(m *domain.Membership) { m.Status = "bogus" }},
			} {
				m := newMembership(ana, c, roleOwner, mApproved, tc.mutate)
				wantFieldErrors(t, "update with a bad "+tc.field, e.r.Memberships.Update(e.ctx, m), tc.field)
			}
			// The fields are checked before the references.
			m := newMembership(ana, c, "bogus", mApproved, func(m *domain.Membership) { m.UserID = unknownRefs[0] })
			wantFieldErrors(t, "update with a bad role and an unknown user", e.r.Memberships.Update(e.ctx, m), "role")
			wantEqual(t, "Get after the failed updates", normMembership(e.membership(ana.ID, c.ID)), normMembership(before))
		}},
		{"Update is ErrNotFound for a membership that does not exist and creates none", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Casa Azul", coletivo, cApproved)
			bias := e.member(bia, c, roleMember, mPending)
			isErr(t, "update of a pair without a membership", e.r.Memberships.Update(e.ctx, newMembership(ana, c, roleOwner, mApproved)), domain.ErrNotFound)
			for _, id := range unknownRefs {
				for name, mutate := range map[string]func(*domain.Membership){
					"user":       func(m *domain.Membership) { m.UserID = id },
					"collective": func(m *domain.Membership) { m.CollectiveID = id },
				} {
					m := newMembership(bia, c, roleOwner, mApproved, mutate)
					isErr(t, fmt.Sprintf("update with an unknown %s %q", name, id), e.r.Memberships.Update(e.ctx, m), domain.ErrNotFound)
				}
			}
			if got := e.views(ana.ID); len(got) != 0 {
				t.Errorf("ListByUser(ana) after the failed update = %v, want none", got)
			}
			wantEqual(t, "the existing membership", normMembership(e.membership(bia.ID, c.ID)), normMembership(bias))
		}},
		{"Update is ErrNotFound for an approver that does not exist and changes nothing", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved)
			before := e.member(ana, c, roleMember, mPending)
			for _, id := range unknownRefs {
				for name, mutate := range map[string]func(*domain.Membership){
					"admin approver": func(m *domain.Membership) { m.AdminApprovedBy = id },
					"owner approver": func(m *domain.Membership) { m.OwnerApprovedBy = id },
				} {
					m := newMembership(ana, c, roleOwner, mApproved, mutate)
					isErr(t, fmt.Sprintf("update with an unknown %s %q", name, id), e.r.Memberships.Update(e.ctx, m), domain.ErrNotFound)
				}
			}
			wantEqual(t, "Get after the failed updates", normMembership(e.membership(ana.ID, c.ID)), normMembership(before))
		}},
		{"Delete removes the membership and leaves the user, the collective and the other memberships", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c, other := e.collective("Casa Azul", coletivo, cApproved), e.collective("Casa Verde", coletivo, cApproved)
			e.member(ana, c, roleMember, mRejected)
			sameUser := e.member(ana, other, roleMember, mPending)
			sameCollective := e.member(bia, c, roleOwner, mApproved)
			noErr(t, "Delete", e.r.Memberships.Delete(e.ctx, ana.ID, c.ID))

			_, err := e.r.Memberships.Get(e.ctx, ana.ID, c.ID)
			isErr(t, "Get after Delete", err, domain.ErrNotFound)
			e.getUser(ana.ID)
			if _, err := e.r.Collectives.ByID(e.ctx, c.ID); err != nil {
				t.Errorf("ByID after the membership's Delete: %v, want the collective to stay", err)
			}
			wantEqual(t, "the same user's other membership", normMembership(e.membership(ana.ID, other.ID)), normMembership(sameUser))
			wantEqual(t, "the other user's membership", normMembership(e.membership(bia.ID, c.ID)), normMembership(sameCollective))
			e.member(ana, c, roleMember, mPending) // the pair is free again
		}},
		{"Delete is ErrNotFound for a second delete, a pair without a membership and IDs the store never issued", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Casa Azul", coletivo, cApproved)
			e.member(ana, c, roleMember, mPending)
			noErr(t, "first Delete", e.r.Memberships.Delete(e.ctx, ana.ID, c.ID))
			isErr(t, "second Delete", e.r.Memberships.Delete(e.ctx, ana.ID, c.ID), domain.ErrNotFound)
			isErr(t, "Delete of a user who has no membership in the collective", e.r.Memberships.Delete(e.ctx, bia.ID, c.ID), domain.ErrNotFound)
			for _, id := range unknownIDs {
				for name, pair := range map[string][2]string{
					"user":       {id, c.ID},
					"collective": {bia.ID, id},
					"both":       {id, id},
				} {
					isErr(t, fmt.Sprintf("Delete with an unknown %s %q", name, id), e.r.Memberships.Delete(e.ctx, pair[0], pair[1]), domain.ErrNotFound)
				}
			}
		}},
	})
}
