package storetest

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

func (e env) auditList(limit int) []domain.AuditEntry {
	e.t.Helper()
	got, err := e.r.AuditEntries.List(e.ctx, limit)
	noErr(e.t, fmt.Sprintf("List(%d)", limit), err)
	return got
}

func auditID(a domain.AuditEntry) string { return a.ID }

// RunAuditEntries runs the contract of domain.AuditEntries.
func RunAuditEntries(t *testing.T, newStore NewStore) {
	t.Helper()
	runSubtests(t, newStore, []subtest{
		{"Create assigns an ID, ignores the one the caller set and stores every other field as given", func(t *testing.T, e env) {
			full := func(when time.Time) func(*domain.AuditEntry) {
				return func(a *domain.AuditEntry) {
					a.ID = "chosen-by-the-caller"
					a.At = inLocalZone(when)
					a.Action = domain.AuditCollectiveRenamed
					a.ActorID, a.ActorName = "actor-1", "Ana Admin"
					a.UserID, a.UserName = "user-1", "Bia Souza"
					a.CollectiveID, a.CollectiveName = "collective-1", "Bloco Luz"
					a.Detail, a.Reason = "Luz -> Bloco Luz", "o nome antigo era ambíguo"
				}
			}
			first := newAuditEntry(full(at(90*time.Minute + 123*time.Millisecond)))
			noErr(t, "Create", e.r.AuditEntries.Create(e.ctx, &first))
			if first.ID == "" || first.ID == "chosen-by-the-caller" {
				t.Fatalf("ID = %q, want one assigned by the store", first.ID)
			}
			// The same caller ID again must not collide: it is never used.
			second := newAuditEntry(full(at(91*time.Minute + 456*time.Millisecond)))
			noErr(t, "Create with the same caller ID", e.r.AuditEntries.Create(e.ctx, &second))
			if second.ID == "" || second.ID == "chosen-by-the-caller" || second.ID == first.ID {
				t.Fatalf("second ID = %q, want one that differs from %q", second.ID, first.ID)
			}

			got := e.auditList(10)
			wantEqual(t, "listed entries", normAll(got, normAuditEntry), normAll([]domain.AuditEntry{second, first}, normAuditEntry))
			wantUTC(t, "At of the newer entry", &got[0].At, second.At)
			wantUTC(t, "At of the older entry", &got[1].At, first.At)
		}},
		{"Create stores an entry without the optional fields as one that has none", func(t *testing.T, e env) {
			a := e.auditEntry()
			wantEqual(t, "listed entries", normAll(e.auditList(10), normAuditEntry), normAll([]domain.AuditEntry{a}, normAuditEntry))
		}},
		{"Create stores every action", func(t *testing.T, e env) {
			actions := []domain.AuditAction{
				domain.AuditAccountApproved, domain.AuditAccountRejected, domain.AuditAccountBlocked, domain.AuditAccountUnblocked,
				domain.AuditCollectiveRequested, domain.AuditCollectiveApproved, domain.AuditCollectiveRejected,
				domain.AuditCollectiveRenamed, domain.AuditCollectiveBlocks,
				domain.AuditJoinRequested, domain.AuditJoinAdminApproved, domain.AuditJoinOwnerApproved, domain.AuditJoinRejected,
				domain.AuditMemberPromoted, domain.AuditMemberDemoted,
			}
			// The newest entry first, as List returns them.
			var want []domain.AuditEntry
			for i, action := range actions {
				a := e.auditEntry(func(a *domain.AuditEntry) { a.Action = action; a.At = at(time.Duration(i) * time.Second) })
				want = append([]domain.AuditEntry{a}, want...)
			}
			wantEqual(t, "listed entries", normAll(e.auditList(domain.ListLimitMax), normAuditEntry), normAll(want, normAuditEntry))
		}},
		{"Create stores names and texts of the greatest length, in runes", func(t *testing.T, e env) {
			a := e.auditEntry(func(a *domain.AuditEntry) {
				a.ActorName = strings.Repeat("ç", domain.UserNameMax)
				a.UserID, a.UserName = "user-1", strings.Repeat("ã", domain.UserNameMax)
				a.CollectiveID, a.CollectiveName = "collective-1", strings.Repeat("é", domain.CollectiveNameMax)
				a.Detail = strings.Repeat("→", domain.AuditTextMax)
				a.Reason = strings.Repeat("ô", domain.AuditTextMax)
			})
			wantEqual(t, "listed entries", normAll(e.auditList(10), normAuditEntry), normAll([]domain.AuditEntry{a}, normAuditEntry))
		}},
		{"Create validates the entry and stores nothing", func(t *testing.T, e env) {
			tooLong := func(n int) string { return strings.Repeat("a", n+1) }
			for _, tc := range []struct {
				what, field string
				mutate      func(*domain.AuditEntry)
			}{
				{"an action that is not defined", "action", func(a *domain.AuditEntry) { a.Action = "bogus" }},
				{"no action", "action", func(a *domain.AuditEntry) { a.Action = "" }},
				{"no time", "at", func(a *domain.AuditEntry) { a.At = time.Time{} }},
				{"no actor", "actor_id", func(a *domain.AuditEntry) { a.ActorID = "" }},
				{"a long actor name", "actor_name", func(a *domain.AuditEntry) { a.ActorName = tooLong(domain.UserNameMax) }},
				{"a long user name", "user_name", func(a *domain.AuditEntry) { a.UserName = tooLong(domain.UserNameMax) }},
				{"a long collective name", "collective_name", func(a *domain.AuditEntry) { a.CollectiveName = tooLong(domain.CollectiveNameMax) }},
				{"a long detail", "detail", func(a *domain.AuditEntry) { a.Detail = tooLong(domain.AuditTextMax) }},
				{"a long reason", "reason", func(a *domain.AuditEntry) { a.Reason = tooLong(domain.AuditTextMax) }},
			} {
				a := newAuditEntry(tc.mutate)
				wantFieldErrors(t, "Create with "+tc.what, e.r.AuditEntries.Create(e.ctx, &a), tc.field)
			}
			if got := e.auditList(domain.ListLimitMax); len(got) != 0 {
				t.Errorf("List returned %d entries after the failed Creates, want none", len(got))
			}
		}},
		{"Create does not check the IDs in the entry", func(t *testing.T, e env) {
			var want []domain.AuditEntry
			for i, id := range unknownRefs {
				a := e.auditEntry(func(a *domain.AuditEntry) {
					a.At = at(time.Duration(i) * time.Second)
					a.ActorID, a.UserID, a.CollectiveID = id, id, id
				})
				want = append([]domain.AuditEntry{a}, want...)
			}
			wantEqual(t, "listed entries", normAll(e.auditList(domain.ListLimitMax), normAuditEntry), normAll(want, normAuditEntry))
		}},
		{"List of an empty store returns nothing", func(t *testing.T, e env) {
			for _, limit := range []int{-1, 0, 1, domain.ListLimitMax} {
				if got := e.auditList(limit); len(got) != 0 {
					t.Errorf("List(%d) returned %d entries, want none", limit, len(got))
				}
			}
		}},
		{"List returns the newest entries first and breaks ties by ID, descending", func(t *testing.T, e env) {
			tie := at(5 * time.Minute)
			atTime := func(when time.Time) func(*domain.AuditEntry) { return func(a *domain.AuditEntry) { a.At = when } }
			// Created out of order: the entry made first has the smallest ID but
			// is newer than the ties, and the newest entry is made in the middle.
			justAfter := e.auditEntry(atTime(tie.Add(time.Millisecond)))
			tie1 := e.auditEntry(atTime(tie))
			oldest := e.auditEntry(atTime(tie.Add(-time.Hour)))
			tie2 := e.auditEntry(atTime(tie))
			newest := e.auditEntry(atTime(tie.Add(time.Hour)))
			tie3 := e.auditEntry(atTime(tie))

			ties := []domain.AuditEntry{tie1, tie2, tie3}
			if tie1.ID == tie2.ID || tie2.ID == tie3.ID || tie1.ID == tie3.ID {
				t.Fatalf("tied IDs = %q, %q, %q, want three different ones", tie1.ID, tie2.ID, tie3.ID)
			}
			sort.Slice(ties, func(i, j int) bool { return ties[i].ID > ties[j].ID })
			want := append([]domain.AuditEntry{newest, justAfter}, ties...)
			want = append(want, oldest)
			got := e.auditList(10)
			wantIDsBy(t, "List", auditID, got, want...)
			wantEqual(t, "listed entries", normAll(got, normAuditEntry), normAll(want, normAuditEntry))
		}},
		{"List returns at most limit entries, with limit clamped to 1..ListLimitMax", func(t *testing.T, e env) {
			oldest := e.auditEntry(func(a *domain.AuditEntry) { a.At = at(time.Minute) })
			middle := e.auditEntry(func(a *domain.AuditEntry) { a.At = at(2 * time.Minute) })
			newest := e.auditEntry(func(a *domain.AuditEntry) { a.At = at(3 * time.Minute) })
			all := []domain.AuditEntry{newest, middle, oldest}
			for _, tc := range []struct {
				limit int
				want  []domain.AuditEntry
			}{
				{limit: 2, want: all[:2]},
				{limit: 1, want: all[:1]},
				{limit: 0, want: all[:1]},
				{limit: -5, want: all[:1]},
				{limit: math.MinInt, want: all[:1]},
				{limit: 3, want: all},
				{limit: domain.ListLimitMax, want: all},
				{limit: domain.ListLimitMax + 1, want: all},
				{limit: 2 * domain.ListLimitMax, want: all},
				{limit: math.MaxInt, want: all},
			} {
				wantIDsBy(t, fmt.Sprintf("List(%d)", tc.limit), auditID, e.auditList(tc.limit), tc.want...)
			}
		}},
		{"Deleting a user or a collective leaves the entries as they were", func(t *testing.T, e env) {
			u := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			e.member(u, c, roleOwner, mApproved)
			about := e.auditEntry(func(a *domain.AuditEntry) {
				a.At = at(time.Minute)
				a.Action = domain.AuditJoinAdminApproved
				a.ActorID, a.ActorName = "admin-1", "Admin"
				a.UserID, a.UserName = u.ID, u.Name
				a.CollectiveID, a.CollectiveName = c.ID, c.Name
				a.Detail, a.Reason = "owner", "conhecida da produção"
			})
			byUser := e.auditEntry(func(a *domain.AuditEntry) {
				a.At = at(2 * time.Minute)
				a.Action = domain.AuditCollectiveRenamed
				a.ActorID, a.ActorName = u.ID, u.Name
				a.CollectiveID, a.CollectiveName = c.ID, c.Name
				a.Detail = "Luz -> Bloco Luz"
			})
			want := normAll([]domain.AuditEntry{byUser, about}, normAuditEntry)
			wantEqual(t, "listed entries before the deletes", normAll(e.auditList(10), normAuditEntry), want)

			noErr(t, "delete the user", e.r.Users.Delete(e.ctx, u.ID))
			wantEqual(t, "listed entries after deleting the user", normAll(e.auditList(10), normAuditEntry), want)
			noErr(t, "delete the collective", e.r.Collectives.Delete(e.ctx, c.ID))
			wantEqual(t, "listed entries after deleting the collective", normAll(e.auditList(10), normAuditEntry), want)

			_, err := e.r.Users.ByID(e.ctx, u.ID)
			isErr(t, "ByID of the deleted user", err, domain.ErrNotFound)
			_, err = e.r.Collectives.ByID(e.ctx, c.ID)
			isErr(t, "ByID of the deleted collective", err, domain.ErrNotFound)
		}},
	})
}
