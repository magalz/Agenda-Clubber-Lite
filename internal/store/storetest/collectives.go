package storetest

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// serializationTimeout bounds the two-transaction subtest, so a backend that
// never lets the second transaction finish fails the subtest instead of hanging.
const serializationTimeout = 30 * time.Second

// searchFixture stores the collectives the SearchApproved subtests look through:
// five approved ones (their keys are acao direta, beco da casa, c.a. funk,
// casa azul and casa verde) and three that are not approved.
func (e env) searchFixture() {
	e.t.Helper()
	e.collective("Casa Azul", coletivo, cApproved)
	e.collective("Casa Verde", produtora, cApproved)
	e.collective("Beco da Casa", coletivo, cApproved)
	e.collective("Ação Direta", coletivo, cApproved)
	e.collective("C.A. Funk", produtora, cApproved)
	e.collective("Casa Rosa", coletivo, cPending)
	e.collective("Casa Preta", coletivo, cRejected)
	e.collective("Casa Lilás", produtora, cPending)
}

func (e env) search(query string, kind domain.CollectiveKind, limit int) []domain.Collective {
	e.t.Helper()
	got, err := e.r.Collectives.SearchApproved(e.ctx, query, kind, limit)
	noErr(e.t, fmt.Sprintf("SearchApproved(%q, %q, %d)", query, kind, limit), err)
	return got
}

func wantNames(t *testing.T, what string, got []domain.Collective, want ...string) {
	t.Helper()
	names := make([]string, len(got))
	for i, c := range got {
		names[i] = c.Name
	}
	wantStrings(t, what, names, want...)
}

func (e env) listCollectives(status domain.CollectiveStatus, limit int) []domain.Collective {
	e.t.Helper()
	got, err := e.r.Collectives.ListByStatus(e.ctx, status, limit)
	noErr(e.t, fmt.Sprintf("ListByStatus(%q, %d)", status, limit), err)
	return got
}

func (e env) storedCollective(id string) domain.Collective {
	e.t.Helper()
	c, err := e.r.Collectives.ByID(e.ctx, id)
	noErr(e.t, "get collective "+id, err)
	return c
}

func (e env) updateCollective(c domain.Collective) {
	e.t.Helper()
	noErr(e.t, "update collective "+c.Name, e.r.Collectives.Update(e.ctx, c))
}

// wantStoredCollective checks that the collective stored under want.ID is want.
func (e env) wantStoredCollective(what string, want domain.Collective) {
	e.t.Helper()
	wantEqual(e.t, what, normCollective(e.storedCollective(want.ID)), normCollective(want))
}

// renamed returns c under another name (and the key that goes with it).
func renamed(c domain.Collective, name string) domain.Collective {
	c.Name = name
	c.NameKey = domain.NameKey(name)
	return c
}

// RunCollectives runs the contract of domain.Collectives.
func RunCollectives(t *testing.T, newStore NewStore) {
	t.Helper()
	runSubtests(t, newStore, []subtest{
		{"create assigns an ID and stores every field", func(t *testing.T, e env) {
			ana, admin := e.user("ana"), e.user("admin")
			c := newCollective("Casa Azul", coletivo, cApproved, func(c *domain.Collective) {
				c.ID = "chosen-by-the-caller"
				c.CreatedBy = ana.ID
				c.ApprovedBy = admin.ID
				c.ApprovedAt = ptr(at(time.Minute))
				c.CreatedAt = at(-time.Hour)
				c.UpdatedAt = at(2 * time.Minute)
			})
			noErr(t, "create", e.r.Collectives.Create(e.ctx, &c))
			if c.ID == "" || c.ID == "chosen-by-the-caller" {
				t.Fatalf("ID = %q, want one assigned by the store", c.ID)
			}
			other := e.collective("Casa Verde", produtora, cPending)
			if other.ID == "" || other.ID == c.ID {
				t.Errorf("second ID = %q, want one that differs from %q", other.ID, c.ID)
			}

			byID, err := e.r.Collectives.ByID(e.ctx, c.ID)
			noErr(t, "ByID", err)
			wantEqual(t, "ByID", normCollective(byID), normCollective(c))
			byKey, err := e.r.Collectives.ByNameKey(e.ctx, c.NameKey)
			noErr(t, "ByNameKey", err)
			wantEqual(t, "ByNameKey", normCollective(byKey), normCollective(c))
		}},
		{"create validates the collective", func(t *testing.T, e env) {
			for _, tc := range []struct {
				field  string
				mutate func(*domain.Collective)
			}{
				{"name", func(c *domain.Collective) { c.Name, c.NameKey = "x", "x" }},
				{"name_key", func(c *domain.Collective) { c.NameKey = "outra chave" }},
				{"kind", func(c *domain.Collective) { c.Kind = "bogus" }},
				{"status", func(c *domain.Collective) { c.Status = "bogus" }},
			} {
				c := newCollective("Casa Azul", coletivo, cApproved, tc.mutate)
				wantFieldErrors(t, "create with a bad "+tc.field, e.r.Collectives.Create(e.ctx, &c), tc.field)
			}
			_, err := e.r.Collectives.ByNameKey(e.ctx, domain.NameKey("Casa Azul"))
			isErr(t, "ByNameKey after the failed creates", err, domain.ErrNotFound)
		}},
		{"create validates before it checks references and the name", func(t *testing.T, e env) {
			e.collective("Casa Azul", coletivo, cApproved)
			c := newCollective("Casa Azul", coletivo, cApproved, func(c *domain.Collective) {
				c.Kind = "bogus"
				c.CreatedBy = unknownRefs[0]
			})
			wantFieldErrors(t, "create", e.r.Collectives.Create(e.ctx, &c), "kind")
		}},
		{"create refuses a CreatedBy or ApprovedBy that is not a user", func(t *testing.T, e env) {
			ana := e.user("ana")
			for _, id := range unknownRefs {
				c := newCollective("Casa Azul", coletivo, cApproved, func(c *domain.Collective) { c.CreatedBy = id })
				isErr(t, fmt.Sprintf("create with CreatedBy %q", id), e.r.Collectives.Create(e.ctx, &c), domain.ErrNotFound)
				c = newCollective("Casa Azul", coletivo, cApproved, func(c *domain.Collective) {
					c.CreatedBy = ana.ID
					c.ApprovedBy = id
				})
				isErr(t, fmt.Sprintf("create with ApprovedBy %q", id), e.r.Collectives.Create(e.ctx, &c), domain.ErrNotFound)
			}
			_, err := e.r.Collectives.ByNameKey(e.ctx, domain.NameKey("Casa Azul"))
			isErr(t, "ByNameKey after the failed creates", err, domain.ErrNotFound)
		}},
		{"create checks references before the name", func(t *testing.T, e env) {
			e.collective("Casa Azul", coletivo, cApproved)
			c := newCollective("Casa Azul", coletivo, cApproved, func(c *domain.Collective) { c.CreatedBy = unknownRefs[1] })
			isErr(t, "create", e.r.Collectives.Create(e.ctx, &c), domain.ErrNotFound)
		}},
		{"create refuses a name key that is taken, in any status and kind", func(t *testing.T, e env) {
			first := e.collective("Ação Direta", coletivo, cPending)
			for _, tc := range []struct {
				name   string
				kind   domain.CollectiveKind
				status domain.CollectiveStatus
			}{
				{"Ação Direta", coletivo, cPending},
				{"ACAO  direta", produtora, cApproved},
				{" acao direta ", coletivo, cRejected},
			} {
				c := newCollective(tc.name, tc.kind, tc.status)
				isErr(t, fmt.Sprintf("create %q", tc.name), e.r.Collectives.Create(e.ctx, &c), domain.ErrNameTaken)
			}
			got, err := e.r.Collectives.ByNameKey(e.ctx, first.NameKey)
			noErr(t, "ByNameKey", err)
			wantEqual(t, "the stored collective", normCollective(got), normCollective(first))
		}},
		{"create lets exactly one of concurrent calls with the same name key win", func(t *testing.T, e env) {
			errs := parallelErr(10, func(int) error {
				c := newCollective("Casa Concorrente", coletivo, cApproved)
				return e.r.Collectives.Create(e.ctx, &c)
			})
			wantOneWinner(t, "create", errs, domain.ErrNameTaken)
			wantNames(t, "the stored collectives", e.search("concorrente", "", domain.SearchLimitMax), "Casa Concorrente")
		}},
		{"ByID returns ErrNotFound for an ID that is not a collective", func(t *testing.T, e env) {
			e.collective("Casa Azul", coletivo, cApproved)
			for _, id := range unknownIDs {
				_, err := e.r.Collectives.ByID(e.ctx, id)
				isErr(t, fmt.Sprintf("ByID(%q)", id), err, domain.ErrNotFound)
			}
		}},
		{"ByNameKey matches the whole key and nothing else", func(t *testing.T, e env) {
			c := e.collective("Casa Azul", coletivo, cApproved)
			for _, key := range []string{"Casa Azul", "casa", "azul", "casa  azul", "casa azul ", "", ".*", "casa.azul"} {
				_, err := e.r.Collectives.ByNameKey(e.ctx, key)
				isErr(t, fmt.Sprintf("ByNameKey(%q)", key), err, domain.ErrNotFound)
			}
			got, err := e.r.Collectives.ByNameKey(e.ctx, "casa azul")
			noErr(t, "ByNameKey", err)
			if got.ID != c.ID {
				t.Errorf("ByNameKey ID = %q, want %q", got.ID, c.ID)
			}
		}},
		{"SearchApproved matches a substring of the name key, ignoring case, accents and spacing", func(t *testing.T, e env) {
			e.searchFixture()
			for _, tc := range []struct {
				query string
				want  []string
			}{
				{"casa", []string{"Beco da Casa", "Casa Azul", "Casa Verde"}},
				{"CASA", []string{"Beco da Casa", "Casa Azul", "Casa Verde"}},
				{"acao", []string{"Ação Direta"}},
				{"AÇÃO", []string{"Ação Direta"}},
				{"da  casa", []string{"Beco da Casa"}},
				{"  azul ", []string{"Casa Azul"}},
				{"lilas", nil}, // pending
				{"preta", nil}, // rejected
				{"nada disso", nil},
			} {
				wantNames(t, fmt.Sprintf("SearchApproved(%q)", tc.query), e.search(tc.query, "", domain.SearchLimitMax), tc.want...)
			}
		}},
		{"SearchApproved with an empty query returns every approved collective", func(t *testing.T, e env) {
			e.searchFixture()
			all := []string{"Ação Direta", "Beco da Casa", "C.A. Funk", "Casa Azul", "Casa Verde"}
			wantNames(t, "empty query", e.search("", "", domain.SearchLimitMax), all...)
			wantNames(t, "blank query", e.search("   ", "", domain.SearchLimitMax), all...)
		}},
		{"SearchApproved filters by kind", func(t *testing.T, e env) {
			e.searchFixture()
			wantNames(t, "coletivo", e.search("", coletivo, domain.SearchLimitMax), "Ação Direta", "Beco da Casa", "Casa Azul")
			wantNames(t, "produtora", e.search("", produtora, domain.SearchLimitMax), "C.A. Funk", "Casa Verde")
			wantNames(t, "casa in produtora", e.search("casa", produtora, domain.SearchLimitMax), "Casa Verde")
			wantNames(t, "casa in coletivo", e.search("casa", coletivo, domain.SearchLimitMax), "Beco da Casa", "Casa Azul")
		}},
		{"SearchApproved treats the query as plain text, never as a pattern", func(t *testing.T, e env) {
			e.searchFixture()
			wantNames(t, ".", e.search(".", "", domain.SearchLimitMax), "C.A. Funk")
			wantNames(t, "c.a.", e.search("c.a.", "", domain.SearchLimitMax), "C.A. Funk")
			for _, q := range []string{".*", "(", "[a-z]", "^casa", "casa$", "casa|beco", `\`, "c.sa", "ca+sa", "?"} {
				wantNames(t, fmt.Sprintf("SearchApproved(%q)", q), e.search(q, "", domain.SearchLimitMax))
			}
		}},
		{"SearchApproved orders by name key in byte order", func(t *testing.T, e env) {
			// "ação luz" has the lowest key but, as a name, sorts after "Luz".
			for _, name := range []string{"Luza", "Luz_1", "Luz 2", "ação luz", "Luz-1", "Beta Luz", "Luz 10"} {
				e.collective(name, coletivo, cApproved)
			}
			wantNames(t, "luz", e.search("luz", "", domain.SearchLimitMax), "ação luz", "Beta Luz", "Luz 10", "Luz 2", "Luz-1", "Luz_1", "Luza")
		}},
		{"SearchApproved returns at most limit results, with limit clamped to SearchLimitMin..SearchLimitMax", func(t *testing.T, e env) {
			lo, hi := domain.SearchLimitMin, domain.SearchLimitMax
			for i := 1; i <= hi+10; i++ {
				e.collective(fmt.Sprintf("Bloco %02d", i), coletivo, cApproved)
			}
			for _, tc := range []struct{ limit, want int }{{10, 10}, {hi, hi}, {hi + 1, hi}, {2 * hi, hi}, {lo, lo}, {lo - 1, lo}, {-5, lo}} {
				got := e.search("bloco", "", tc.limit)
				if len(got) != tc.want {
					t.Errorf("SearchApproved with limit %d returned %d collectives, want %d", tc.limit, len(got), tc.want)
				}
			}
			wantNames(t, "first three", e.search("bloco", "", 3), "Bloco 01", "Bloco 02", "Bloco 03")
		}},
		{"SearchApproved returns the stored fields", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved, func(c *domain.Collective) {
				c.CreatedBy = ana.ID
				c.ApprovedAt = ptr(at(time.Minute))
			})
			got := e.search("azul", "", 5)
			if len(got) != 1 {
				t.Fatalf("SearchApproved returned %d collectives, want 1", len(got))
			}
			wantEqual(t, "collective", normCollective(got[0]), normCollective(c))
		}},
		{"Delete removes the collective and its memberships but not the users", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Casa Azul", coletivo, cApproved)
			other := e.collective("Casa Verde", produtora, cApproved)
			e.member(ana, c, roleOwner, mApproved)
			e.member(bia, c, roleMember, mPending)
			e.member(ana, other, roleOwner, mApproved)

			noErr(t, "Delete", e.r.Collectives.Delete(e.ctx, c.ID))

			_, err := e.r.Collectives.ByID(e.ctx, c.ID)
			isErr(t, "ByID after Delete", err, domain.ErrNotFound)
			_, err = e.r.Collectives.ByNameKey(e.ctx, c.NameKey)
			isErr(t, "ByNameKey after Delete", err, domain.ErrNotFound)
			if got, err := e.r.Memberships.ListByUser(e.ctx, bia.ID); err != nil || len(got) != 0 {
				t.Errorf("ListByUser(bia) = %v, %v, want no memberships", got, err)
			}
			views, err := e.r.Memberships.ListByUser(e.ctx, ana.ID)
			noErr(t, "ListByUser(ana)", err)
			if len(views) != 1 || views[0].CollectiveID != other.ID {
				t.Errorf("ListByUser(ana) = %v, want only the membership in %q", views, other.Name)
			}
			owners, err := e.r.Memberships.OwnerEmails(e.ctx, c.ID)
			noErr(t, "OwnerEmails", err)
			wantStrings(t, "OwnerEmails of the deleted collective", owners)
			e.getUser(ana.ID)
			e.getUser(bia.ID)
			if _, err := e.r.Collectives.ByID(e.ctx, other.ID); err != nil {
				t.Errorf("the other collective: %v", err)
			}
		}},
		{"Delete frees the name key and leaves no memberships behind", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved)
			e.member(ana, c, roleOwner, mApproved)
			noErr(t, "Delete", e.r.Collectives.Delete(e.ctx, c.ID))

			again := e.collective("Casa Azul", produtora, cPending)
			if views, err := e.r.Memberships.ListByUser(e.ctx, ana.ID); err != nil || len(views) != 0 {
				t.Errorf("ListByUser = %v, %v, want no memberships", views, err)
			}
			e.member(ana, again, roleMember, mPending) // the pair is free again
		}},
		{"Delete returns ErrNotFound for a collective that does not exist", func(t *testing.T, e env) {
			c := e.collective("Casa Azul", coletivo, cApproved)
			for _, id := range unknownIDs {
				isErr(t, fmt.Sprintf("Delete(%q)", id), e.r.Collectives.Delete(e.ctx, id), domain.ErrNotFound)
			}
			noErr(t, "Delete", e.r.Collectives.Delete(e.ctx, c.ID))
			isErr(t, "second Delete", e.r.Collectives.Delete(e.ctx, c.ID), domain.ErrNotFound)
		}},
		{"ListByStatus returns the collectives with this status and no other", func(t *testing.T, e env) {
			e.searchFixture()
			all := domain.ListLimitMax
			wantNames(t, "approved", e.listCollectives(cApproved, all), "Ação Direta", "Beco da Casa", "C.A. Funk", "Casa Azul", "Casa Verde")
			wantNames(t, "pending", e.listCollectives(cPending, all), "Casa Lilás", "Casa Rosa")
			wantNames(t, "rejected", e.listCollectives(cRejected, all), "Casa Preta")
		}},
		{"ListByStatus returns an empty list when no collective has the status", func(t *testing.T, e env) {
			for _, status := range []domain.CollectiveStatus{cPending, cApproved, cRejected} {
				wantNames(t, fmt.Sprintf("%s in an empty store", status), e.listCollectives(status, domain.ListLimitMax))
			}
			e.collective("Casa Azul", coletivo, cApproved)
			wantNames(t, "pending", e.listCollectives(cPending, domain.ListLimitMax))
			wantNames(t, "rejected", e.listCollectives(cRejected, domain.ListLimitMax))
		}},
		{"ListByStatus orders by name key in byte order", func(t *testing.T, e env) {
			// "ação luz" has the lowest key ("acao luz") but, as a name, sorts last.
			for _, name := range []string{"Luza", "Luz_1", "Luz 2", "ação luz", "Luz-1", "Beta Luz", "Luz 10"} {
				e.collective(name, coletivo, cPending)
			}
			e.collective("Luz Aprovada", coletivo, cApproved)
			wantNames(t, "pending", e.listCollectives(cPending, domain.ListLimitMax), "ação luz", "Beta Luz", "Luz 10", "Luz 2", "Luz-1", "Luz_1", "Luza")
		}},
		{"ListByStatus returns at most limit results, with limit clamped to 1..ListLimitMax", func(t *testing.T, e env) {
			hi := domain.ListLimitMax
			for i := 1; i <= hi+10; i++ {
				e.collective(fmt.Sprintf("Bloco %03d", i), coletivo, cPending)
			}
			e.collective("Bloco Aprovado", coletivo, cApproved)
			for _, tc := range []struct{ limit, want int }{{10, 10}, {hi, hi}, {hi + 1, hi}, {2 * hi, hi}, {1, 1}, {0, 1}, {-5, 1}} {
				got := e.listCollectives(cPending, tc.limit)
				if len(got) != tc.want {
					t.Errorf("ListByStatus with limit %d returned %d collectives, want %d", tc.limit, len(got), tc.want)
				}
			}
			wantNames(t, "first three", e.listCollectives(cPending, 3), "Bloco 001", "Bloco 002", "Bloco 003")
			if got := e.listCollectives(cPending, hi+1); len(got) == hi && got[hi-1].Name != fmt.Sprintf("Bloco %03d", hi) {
				t.Errorf("the last of the %d collectives is %q, want %q", hi, got[hi-1].Name, fmt.Sprintf("Bloco %03d", hi))
			}
		}},
		{"ListByStatus returns the stored fields", func(t *testing.T, e env) {
			ana, admin := e.user("ana"), e.user("admin")
			c := e.collective("Casa Azul", produtora, cApproved, func(c *domain.Collective) {
				c.CreatedBy = ana.ID
				c.ApprovedBy = admin.ID
				c.ApprovedAt = ptr(at(time.Minute))
				c.EventsBlocked = true
				c.Locked = true
			})
			plain := e.collective("Casa Verde", coletivo, cApproved)
			got := e.listCollectives(cApproved, 5)
			if len(got) != 2 {
				t.Fatalf("ListByStatus returned %d collectives, want 2", len(got))
			}
			wantEqual(t, "first collective", normCollective(got[0]), normCollective(c))
			wantEqual(t, "second collective", normCollective(got[1]), normCollective(plain))
		}},
		{"ListByStatus refuses a status that is not defined", func(t *testing.T, e env) {
			e.collective("Casa Azul", coletivo, cApproved)
			for _, status := range []domain.CollectiveStatus{"", "bogus", "APPROVED", "approved "} {
				got, err := e.r.Collectives.ListByStatus(e.ctx, status, domain.ListLimitMax)
				wantFieldErrors(t, fmt.Sprintf("ListByStatus(%q)", status), err, "status")
				if len(got) != 0 {
					t.Errorf("ListByStatus(%q) returned %d collectives with its error, want none", status, len(got))
				}
			}
		}},
		{"Update stores every mutable field", func(t *testing.T, e env) {
			ana, admin := e.user("ana"), e.user("admin")
			c := e.collective("Casa Azul", coletivo, cPending, func(c *domain.Collective) { c.CreatedBy = ana.ID })
			other := e.collective("Casa Verde", produtora, cPending)

			c = renamed(c, "Casa Índigo")
			c.Status = cApproved
			c.ApprovedBy = admin.ID
			c.ApprovedAt = ptr(at(time.Minute))
			c.EventsBlocked = true
			c.Locked = true
			c.UpdatedAt = at(2 * time.Minute)
			e.updateCollective(c)

			e.wantStoredCollective("after Update", c)
			byKey, err := e.r.Collectives.ByNameKey(e.ctx, c.NameKey)
			noErr(t, "ByNameKey of the new name", err)
			if byKey.ID != c.ID {
				t.Errorf("ByNameKey ID = %q, want %q", byKey.ID, c.ID)
			}
			_, err = e.r.Collectives.ByNameKey(e.ctx, domain.NameKey("Casa Azul"))
			isErr(t, "ByNameKey of the old name", err, domain.ErrNotFound)
			e.wantStoredCollective("the other collective", other)
		}},
		{"Update stores each mutable field on its own", func(t *testing.T, e env) {
			admin := e.user("admin")
			for i, tc := range []struct {
				field  string
				mutate func(*domain.Collective)
			}{
				{"name", func(c *domain.Collective) { *c = renamed(*c, "Casa Índigo") }},
				{"status", func(c *domain.Collective) { c.Status = cApproved }},
				{"approved_by", func(c *domain.Collective) { c.ApprovedBy = admin.ID }},
				{"approved_at", func(c *domain.Collective) { c.ApprovedAt = ptr(at(time.Minute)) }},
				{"events_blocked", func(c *domain.Collective) { c.EventsBlocked = true }},
				{"locked", func(c *domain.Collective) { c.Locked = true }},
				{"updated_at", func(c *domain.Collective) { c.UpdatedAt = at(2 * time.Minute) }},
			} {
				c := e.collective(fmt.Sprintf("Bloco %d", i), coletivo, cPending)
				tc.mutate(&c)
				noErr(t, "Update of "+tc.field, e.r.Collectives.Update(e.ctx, c))
				e.wantStoredCollective("after the Update of "+tc.field, c)
			}
		}},
		{"Update clears ApprovedBy and ApprovedAt, and stores the flags as set and as cleared", func(t *testing.T, e env) {
			admin := e.user("admin")
			c := e.collective("Casa Azul", coletivo, cApproved, func(c *domain.Collective) {
				c.ApprovedBy = admin.ID
				c.ApprovedAt = ptr(at(time.Minute))
				c.EventsBlocked = true
				c.Locked = true
			})
			c.ApprovedBy, c.ApprovedAt, c.EventsBlocked, c.Locked = "", nil, false, false
			c.Status = cPending
			c.UpdatedAt = at(time.Minute)
			e.updateCollective(c)
			e.wantStoredCollective("after the Update that clears everything", c)

			for _, tc := range []struct{ events, locked bool }{
				{true, false}, {false, true}, {true, true}, {false, false}, {true, false}, {false, false},
			} {
				c.EventsBlocked, c.Locked = tc.events, tc.locked
				e.updateCollective(c)
				e.wantStoredCollective(fmt.Sprintf("with EventsBlocked=%v and Locked=%v", tc.events, tc.locked), c)
			}
		}},
		{"Update never changes Kind, CreatedBy or CreatedAt", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Casa Azul", coletivo, cPending, func(c *domain.Collective) { c.CreatedBy = ana.ID })

			changed := c
			changed.Kind = produtora
			changed.CreatedBy = bia.ID
			changed.CreatedAt = at(-time.Hour)
			changed.Status = cApproved // a mutable field does change
			e.updateCollective(changed)
			want := c
			want.Status = cApproved
			e.wantStoredCollective("after Update with another Kind, CreatedBy and CreatedAt", want)

			changed.CreatedBy = ""
			e.updateCollective(changed)
			e.wantStoredCollective("after Update with an empty CreatedBy", want)
		}},
		{"Update with the collective as it was read returns nil and changes nothing", func(t *testing.T, e env) {
			ana, admin := e.user("ana"), e.user("admin")
			e.collective("Casa Verde", produtora, cApproved)
			c := e.collective("Casa Azul", coletivo, cApproved, func(c *domain.Collective) {
				c.CreatedBy = ana.ID
				c.ApprovedBy = admin.ID
				c.ApprovedAt = ptr(at(time.Minute))
				c.Locked = true
			})
			got := e.storedCollective(c.ID)
			e.updateCollective(got) // its own name key is not a taken one
			e.wantStoredCollective("after Update", got)
			e.updateCollective(got)
			e.wantStoredCollective("after a second Update", got)
		}},
		{"Update validates the collective and changes nothing", func(t *testing.T, e env) {
			c := e.collective("Casa Azul", coletivo, cApproved)
			for _, tc := range []struct {
				field  string
				mutate func(*domain.Collective)
			}{
				{"name", func(c *domain.Collective) { c.Name, c.NameKey = "x", "x" }},
				{"name_key", func(c *domain.Collective) { c.NameKey = "outra chave" }},
				{"kind", func(c *domain.Collective) { c.Kind = "bogus" }},
				{"status", func(c *domain.Collective) { c.Status = "bogus" }},
			} {
				bad := c
				bad.Locked = true
				tc.mutate(&bad)
				wantFieldErrors(t, "Update with a bad "+tc.field, e.r.Collectives.Update(e.ctx, bad), tc.field)
				e.wantStoredCollective("after Update with a bad "+tc.field, c)
			}
		}},
		{"Update validates before it checks the ID, the references and the name", func(t *testing.T, e env) {
			e.collective("Casa Azul", coletivo, cApproved)
			c := e.collective("Casa Verde", produtora, cApproved)
			bad := c
			bad.ID = unknownIDs[2]
			bad.ApprovedBy = unknownRefs[1]
			bad.Kind = "bogus"
			wantFieldErrors(t, "Update of a missing collective", e.r.Collectives.Update(e.ctx, bad), "kind")
			bad = renamed(c, "Casa Azul") // a name key that is taken
			bad.ApprovedBy = unknownRefs[1]
			bad.Status = "bogus"
			wantFieldErrors(t, "Update to a taken name", e.r.Collectives.Update(e.ctx, bad), "status")
			e.wantStoredCollective("after both", c)
		}},
		{"Update returns ErrNotFound for a collective that does not exist", func(t *testing.T, e env) {
			c := e.collective("Casa Azul", coletivo, cApproved)
			other := e.collective("Casa Verde", produtora, cApproved)
			for _, id := range unknownIDs {
				missing := renamed(c, "Casa Nova")
				missing.ID = id
				isErr(t, fmt.Sprintf("Update with ID %q", id), e.r.Collectives.Update(e.ctx, missing), domain.ErrNotFound)
			}
			noErr(t, "Delete", e.r.Collectives.Delete(e.ctx, c.ID))
			isErr(t, "Update of a deleted collective", e.r.Collectives.Update(e.ctx, c), domain.ErrNotFound)
			_, err := e.r.Collectives.ByID(e.ctx, c.ID)
			isErr(t, "ByID after the Update of a deleted collective", err, domain.ErrNotFound)
			_, err = e.r.Collectives.ByNameKey(e.ctx, domain.NameKey("Casa Nova"))
			isErr(t, "ByNameKey after the Updates of missing collectives", err, domain.ErrNotFound)
			e.wantStoredCollective("the other collective", other)
		}},
		{"Update refuses an ApprovedBy that is not a user and changes nothing", func(t *testing.T, e env) {
			c := e.collective("Casa Azul", coletivo, cPending)
			gone := e.user("gone")
			noErr(t, "Delete user", e.r.Users.Delete(e.ctx, gone.ID))
			for _, id := range append([]string{gone.ID}, unknownRefs...) {
				bad := renamed(c, "Casa Nova")
				bad.ApprovedBy = id
				bad.Status = cApproved
				bad.Locked = true
				isErr(t, fmt.Sprintf("Update with ApprovedBy %q", id), e.r.Collectives.Update(e.ctx, bad), domain.ErrNotFound)
				e.wantStoredCollective(fmt.Sprintf("after Update with ApprovedBy %q", id), c)
			}
		}},
		{"Update refuses a name key that another collective has and changes nothing", func(t *testing.T, e env) {
			azul := e.collective("Casa Azul", coletivo, cApproved)
			rosa := e.collective("Casa Rosa", produtora, cRejected)
			verde := e.collective("Casa Verde", produtora, cPending)
			for _, name := range []string{"Casa Azul", "CASA  azul", " casa azul ", "Casa Rosa"} {
				bad := renamed(verde, name)
				bad.Status = cApproved
				bad.Locked = true
				isErr(t, fmt.Sprintf("Update to %q", name), e.r.Collectives.Update(e.ctx, bad), domain.ErrNameTaken)
				e.wantStoredCollective(fmt.Sprintf("the collective after Update to %q", name), verde)
			}
			e.wantStoredCollective("the first collective", azul)
			e.wantStoredCollective("the second collective", rosa)
		}},
		{"Update lets exactly one of concurrent renames to the same name key win", func(t *testing.T, e env) {
			cs := make([]domain.Collective, 10)
			for i := range cs {
				cs[i] = e.collective(fmt.Sprintf("Bloco %02d", i), coletivo, cPending)
			}
			errs := parallelErr(len(cs), func(i int) error {
				return e.r.Collectives.Update(e.ctx, renamed(cs[i], "Casa Concorrente"))
			})
			wantOneWinner(t, "Update", errs, domain.ErrNameTaken)
			taken := 0
			for _, c := range e.listCollectives(cPending, domain.ListLimitMax) {
				if c.Name == "Casa Concorrente" {
					taken++
				}
			}
			if taken != 1 {
				t.Errorf("%d collectives are named Casa Concorrente, want 1", taken)
			}
		}},
		{"Update serializes a transaction that read the collective before another one changed it", func(t *testing.T, e env) {
			// Transaction A reads the collective and waits until the writer B has
			// committed Locked=true; then A sets EventsBlocked on the value it read
			// and calls Update. A backend that lets A commit that stale copy loses
			// B's change. One that makes A run again (the contract says the backend
			// retries one of two conflicting transactions) shows A the new value.
			// The wait assumes a backend where B can commit while A is open.
			c := e.collective("Casa Azul", coletivo, cApproved)
			ctx, cancel := context.WithTimeout(e.ctx, serializationTimeout)
			defer cancel()

			var (
				mu        sync.Mutex
				runs      int
				sawLocked bool
			)
			aRead := make(chan struct{})   // closed by the first run of A, after its read
			bDone := make(chan struct{})   // closed when B has returned, committed or not
			aResult := make(chan error, 1) // the error of A's WithTx
			go func() {
				aResult <- e.s.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
					got, err := r.Collectives.ByID(ctx, c.ID)
					if err != nil {
						return err
					}
					mu.Lock()
					runs++
					first := runs == 1
					sawLocked = sawLocked || got.Locked
					mu.Unlock()
					if first { // a run after a retry must not wait again
						close(aRead)
						select {
						case <-bDone:
						case <-ctx.Done():
							return fmt.Errorf("waiting for the writer: %w", ctx.Err())
						}
					}
					got.EventsBlocked = true
					return r.Collectives.Update(ctx, got)
				})
			}()

			select {
			case <-aRead:
			case err := <-aResult:
				t.Fatalf("transaction A ended before it could be overtaken: %v", err)
			case <-ctx.Done():
				t.Fatalf("transaction A never read the collective: %v", ctx.Err())
			}
			errB := e.s.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
				got, err := r.Collectives.ByID(ctx, c.ID)
				if err != nil {
					return err
				}
				got.Locked = true
				return r.Collectives.Update(ctx, got)
			})
			close(bDone)
			var errA error
			select {
			case errA = <-aResult:
			case <-ctx.Done():
				t.Fatalf("transaction A did not finish: %v", ctx.Err())
			}
			noErr(t, "writer B", errB)
			noErr(t, "transaction A", errA)

			mu.Lock()
			gotRuns, saw := runs, sawLocked
			mu.Unlock()
			t.Logf("transaction A ran %d time(s)", gotRuns)
			if !saw {
				t.Errorf("no run of transaction A saw Locked=true, so A committed a copy that B had already replaced")
			}
			got := e.storedCollective(c.ID)
			if !got.Locked || !got.EventsBlocked {
				t.Errorf("stored collective has Locked=%v and EventsBlocked=%v, want both true: one change was lost", got.Locked, got.EventsBlocked)
			}
		}},
	})
}
