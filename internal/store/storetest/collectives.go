package storetest

import (
	"fmt"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

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
	})
}
