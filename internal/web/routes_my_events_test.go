package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The fixed texts of Meus Eventos (see routes_my_events.go and the template
// pages/app/meus-eventos.html). A row is read-only for one reason, which it
// says; a section with no rows and the page with no events say so too.
const (
	mineReadOnlyMember = "só leitura (Membro)"
	mineReadOnlyBlock  = "eventos bloqueados pelo admin"
	mineReadOnlyOther  = "só leitura"
	mineNoUpcoming     = "nenhum evento próximo"
	mineNoPast         = "nenhum evento anterior"
	mineEmpty          = "nenhum evento nos seus coletivos ainda"
	mineCreateLink     = "[→] criar evento"
)

// mineStart is the instant of an hour on a day of November 2026 in Fortaleza
// (UTC-3), which is how the clock of the tests (webTestNow, 12/11 13:00) reads.
func mineStart(day, hour int) time.Time {
	return time.Date(2026, 11, day, hour+3, 0, 0, 0, time.UTC)
}

// mineEvent stores an event of the collective, created by the world's Owner.
func mineEvent(t *testing.T, w evWorld, c domain.Collective, name string, startsAt time.Time, durationMin int) domain.Event {
	t.Helper()
	return evEvent(t, w.Store, c.ID, w.Owner.ID, func(e *domain.Event) {
		e.Name, e.StartsAt, e.DurationMin = name, startsAt, durationMin
	})
}

// mineGet asks for the page as u and returns its body, after a 200.
func mineGet(t *testing.T, w evWorld, u domain.User) string {
	t.Helper()
	rec := serve(w.App.Router(), http.MethodGet, "/meus-eventos", withCookie(w.Cookie[u.ID]))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200; body %q", u.Name, rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("%s: Content-Type = %q, want HTML", u.Name, ct)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("%s: Cache-Control = %q, want no-store", u.Name, got)
	}
	return rec.Body.String()
}

// mineRow returns the markup of the list row whose title link reads name (the
// name as it appears escaped in the page), or fails the test.
func mineRow(t *testing.T, body, name string) string {
	t.Helper()
	for _, chunk := range strings.Split(body, `<li class="item-list__item">`)[1:] {
		row, _, _ := strings.Cut(chunk, "</li>")
		if strings.Contains(row, ">"+name+"</a>") {
			return row
		}
	}
	t.Fatalf("no row for %q in:\n%s", name, body)
	return ""
}

// mineSections splits the page at the headings "Próximos" and "Anteriores",
// which come in that order, and returns what follows each one.
func mineSections(t *testing.T, body string) (upcoming, past string) {
	t.Helper()
	i := strings.Index(body, ">Próximos</h2>")
	j := strings.Index(body, ">Anteriores</h2>")
	if i < 0 || j < 0 || i > j {
		t.Fatalf("want the headings Próximos then Anteriores (at %d and %d) in:\n%s", i, j, body)
	}
	return body[i:j], body[j:]
}

// mineWantOrder fails unless every name appears in the section, in that order.
func mineWantOrder(t *testing.T, what, section string, names ...string) {
	t.Helper()
	pos := 0
	for _, n := range names {
		i := strings.Index(section[pos:], ">"+n+"</a>")
		if i < 0 {
			t.Errorf("%s: %q not found (in order after %q) in:\n%s", what, n, section[:pos], section)
			return
		}
		pos += i + len(n)
	}
}

// An Administrador sees the events of their collectives with the edit link,
// except where the admin blocked the collective's events: that row is read-only
// and says why.
func TestMineOwnerRows(t *testing.T) {
	w := newEvWorld(t)
	alfa := mineEvent(t, w, w.Alfa, "Noite Verde", mineStart(14, 22), 360)
	delta := mineEvent(t, w, w.Delta, "Festa Delta", mineStart(15, 20), 240)
	body := mineGet(t, w, w.Owner)

	wantAll(t, "the page", body,
		`<title>Meus Eventos · AGENDA CLUBBER</title>`,
		`<h1 `, `meus eventos`,
		`href="/meus-eventos" aria-current="page"`,
	)

	row := mineRow(t, body, "Noite Verde")
	wantAll(t, "Alfa's row", row,
		`href="/eventos/`+alfa.ID+`"`,
		`sábado, 14/11/2026 · 22:00`,
		`Coletivo Alfa`, `Fortaleza/CE`,
		`>Festa</span>`,
		`Administrador`,
		`href="/eventos/`+alfa.ID+`/editar"`,
		`[→] editar<span class="sr-only"> Noite Verde</span>`,
	)
	for _, absent := range []string{mineReadOnlyMember, mineReadOnlyBlock, mineReadOnlyOther} {
		if strings.Contains(row, absent) {
			t.Errorf("Alfa's row says %q, but it is editable", absent)
		}
	}

	row = mineRow(t, body, "Festa Delta")
	wantAll(t, "Delta's row", row,
		`href="/eventos/`+delta.ID+`"`,
		`domingo, 15/11/2026 · 20:00`,
		`Coletivo Delta`, `Administrador`,
		`>`+mineReadOnlyBlock+`</span>`,
	)
	for _, absent := range []string{"/editar", mineReadOnlyMember} {
		if strings.Contains(row, absent) {
			t.Errorf("Delta's row carries %q, but its events are blocked", absent)
		}
	}
	if n := strings.Count(body, "/editar"); n != 1 {
		t.Errorf("the page has %d edit links, want 1", n)
	}
}

// A Membro sees the events of the collectives they belong to, read-only, and
// none of a collective they are not in. In a blocked collective a Membro is told
// they are a Membro, not that the events are blocked: that note is for the
// Administrador it stops, like the event page.
func TestMineMemberRowsAreReadOnly(t *testing.T) {
	w := newEvWorld(t)
	dora := insertWebUser(t, w.Store, "dora@agenda.test", func(u *domain.User) { u.Name = "Dora Teste" })
	evMember(t, w.Store, dora.ID, w.Delta.ID, domain.RoleMember)
	w.Cookie[dora.ID] = loginAs(t, w.App, dora)
	alfa := mineEvent(t, w, w.Alfa, "Noite Verde", mineStart(14, 22), 360)
	mineEvent(t, w, w.Delta, "Festa Delta", mineStart(15, 20), 240)

	body := mineGet(t, w, w.Member)
	row := mineRow(t, body, "Noite Verde")
	wantAll(t, "the Membro's row", row,
		`href="/eventos/`+alfa.ID+`"`, `Coletivo Alfa`, `Membro`,
		`>`+mineReadOnlyMember+`</span>`,
	)
	for _, absent := range []string{"/editar", mineReadOnlyBlock, "Administrador"} {
		if strings.Contains(row, absent) {
			t.Errorf("the Membro's row carries %q", absent)
		}
	}
	if strings.Contains(body, "Festa Delta") || strings.Contains(body, "Coletivo Delta") {
		t.Error("a Membro of Alfa sees an event of Delta")
	}
	if strings.Contains(body, mineEmpty) || strings.Contains(body, mineCreateLink) {
		t.Error("a Membro with events gets the empty state or the create link")
	}

	row = mineRow(t, mineGet(t, w, dora), "Festa Delta")
	wantAll(t, "the Membro's row in a blocked collective", row, `>`+mineReadOnlyMember+`</span>`)
	for _, absent := range []string{"/editar", mineReadOnlyBlock} {
		if strings.Contains(row, absent) {
			t.Errorf("the Membro's row in a blocked collective carries %q", absent)
		}
	}
}

// An Administrador of a collective that is no longer approved has no right to
// edit, and neither a Membro nor a block explains it: the row still says it is
// read-only.
func TestMineUnapprovedCollectiveRowIsReadOnly(t *testing.T) {
	w := newEvWorld(t)
	gone := evCollective(t, w.Store, "Coletivo Extinto", w.Owner.ID, func(c *domain.Collective) {
		c.Status, c.ApprovedAt = domain.CollectiveStatusRejected, nil
	})
	evMember(t, w.Store, w.Owner.ID, gone.ID, domain.RoleOwner)
	mineEvent(t, w, gone, "Festa Extinta", mineStart(14, 22), 360)

	row := mineRow(t, mineGet(t, w, w.Owner), "Festa Extinta")
	wantAll(t, "the row", row, `Coletivo Extinto`, `Administrador`, `>`+mineReadOnlyOther+`</span>`)
	for _, absent := range []string{"/editar", mineReadOnlyMember, mineReadOnlyBlock} {
		if strings.Contains(row, absent) {
			t.Errorf("the row carries %q", absent)
		}
	}
}

// A person with no events in their collectives gets the empty state, with the
// way to create one only when they may. Someone with no membership, the site
// admin included, sees none of the events that exist.
func TestMineEmptyState(t *testing.T) {
	w := newEvWorld(t)
	mineEvent(t, w, w.Alfa, "Noite Verde", mineStart(14, 22), 360)
	for name, u := range map[string]domain.User{"outsider": w.Outsider, "admin": w.Admin} {
		t.Run(name, func(t *testing.T) {
			body := mineGet(t, w, u)
			wantAll(t, "the page", body, mineEmpty)
			for _, absent := range []string{"Noite Verde", ">Próximos<", ">Anteriores<", mineCreateLink} {
				if strings.Contains(body, absent) {
					t.Errorf("the page carries %q", absent)
				}
			}
		})
	}

	t.Run("an owner with no events", func(t *testing.T) {
		w := newEvWorld(t)
		body := mineGet(t, w, w.Owner)
		wantAll(t, "the page", body, mineEmpty)
		if attrs := linkAttrs(t, body, mineCreateLink); !strings.Contains(attrs, `href="/eventos/novo"`) {
			t.Errorf("the create link = %q, want it to point at /eventos/novo", attrs)
		}
		for _, absent := range []string{">Próximos<", ">Anteriores<"} {
			if strings.Contains(body, absent) {
				t.Errorf("the empty page carries the heading %q", absent)
			}
		}
	})
}

// An event that has not ended (one in progress included) is under Próximos, by
// start time; one that has ended is under Anteriores, latest first. A section
// with no row says so.
func TestMineSplitsUpcomingAndPast(t *testing.T) {
	w := newEvWorld(t)
	// Stored out of order on purpose. Now is Thursday 12/11 at 13:00.
	mineEvent(t, w, w.Alfa, "Depois Longe", mineStart(20, 22), 360)
	mineEvent(t, w, w.Alfa, "Antes Antigo", mineStart(5, 22), 360)
	mineEvent(t, w, w.Alfa, "Em Andamento", mineStart(12, 11), 360) // ends 17:00
	mineEvent(t, w, w.Alfa, "Depois Perto", mineStart(14, 22), 360)
	mineEvent(t, w, w.Alfa, "Antes Recente", mineStart(10, 22), 60)

	upcoming, past := mineSections(t, mineGet(t, w, w.Owner))
	mineWantOrder(t, "Próximos", upcoming, "Em Andamento", "Depois Perto", "Depois Longe")
	mineWantOrder(t, "Anteriores", past, "Antes Recente", "Antes Antigo")
	for _, n := range []string{"Antes Recente", "Antes Antigo"} {
		if strings.Contains(upcoming, n) {
			t.Errorf("%q, which ended, is under Próximos", n)
		}
	}
	for _, n := range []string{"Em Andamento", "Depois Perto", "Depois Longe"} {
		if strings.Contains(past, n) {
			t.Errorf("%q, which has not ended, is under Anteriores", n)
		}
	}
	for _, absent := range []string{mineNoUpcoming, mineNoPast, mineEmpty} {
		if strings.Contains(upcoming+past, absent) {
			t.Errorf("a page with both sections full says %q", absent)
		}
	}

	t.Run("only past events", func(t *testing.T) {
		w := newEvWorld(t)
		mineEvent(t, w, w.Alfa, "Antes Antigo", mineStart(5, 22), 360)
		upcoming, past := mineSections(t, mineGet(t, w, w.Owner))
		wantAll(t, "Próximos", upcoming, mineNoUpcoming)
		mineWantOrder(t, "Anteriores", past, "Antes Antigo")
		if strings.Contains(past, mineNoPast) {
			t.Errorf("Anteriores has a row and says %q", mineNoPast)
		}
	})
	t.Run("only upcoming events", func(t *testing.T) {
		w := newEvWorld(t)
		mineEvent(t, w, w.Alfa, "Depois Perto", mineStart(14, 22), 360)
		upcoming, past := mineSections(t, mineGet(t, w, w.Owner))
		mineWantOrder(t, "Próximos", upcoming, "Depois Perto")
		wantAll(t, "Anteriores", past, mineNoPast)
		if strings.Contains(upcoming, mineNoUpcoming) {
			t.Errorf("Próximos has a row and says %q", mineNoUpcoming)
		}
	})
}

// What people typed is escaped, and the markup carries nothing the CSP blocks.
func TestMineEscapesAndIsCSPClean(t *testing.T) {
	w := newEvWorld(t)
	odd := evCollective(t, w.Store, "<i>Casa</i>", w.Owner.ID)
	evMember(t, w.Store, w.Owner.ID, odd.ID, domain.RoleOwner)
	mineEvent(t, w, odd, "<b>negrito</b> & cia", mineStart(14, 22), 360)
	mineEvent(t, w, w.Alfa, "Noite Antiga", mineStart(5, 22), 360)
	mineEvent(t, w, w.Delta, "Festa Delta", mineStart(15, 20), 240)

	body := mineGet(t, w, w.Owner)
	wantAll(t, "escaped text", body, `&lt;b&gt;negrito&lt;/b&gt; &amp; cia`, `&lt;i&gt;Casa&lt;/i&gt;`)
	for _, raw := range []string{"<b>negrito", "<i>Casa"} {
		if strings.Contains(body, raw) {
			t.Errorf("the page carries %q unescaped", raw)
		}
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("the page breaks the CSP: %v", v)
	}
	for _, u := range []domain.User{w.Member, w.Outsider} {
		if v := cspViolations(mineGet(t, w, u)); len(v) > 0 {
			t.Errorf("%s: the page breaks the CSP: %v", u.Name, v)
		}
	}
}

// Anonymous, the page sends the visitor to the login and brings them back.
func TestMineNeedsALogin(t *testing.T) {
	w := newEvWorld(t)
	rec := serve(w.App.Router(), http.MethodGet, "/meus-eventos")
	if want := "/entrar?next=%2Fmeus-eventos"; rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
		t.Errorf("anonymous: %d, Location %q; want 303 to %s", rec.Code, rec.Header().Get("Location"), want)
	}
}

type mineFailingEvents struct{ domain.Events }

func (mineFailingEvents) ListByCollectives(context.Context, []string, int) ([]domain.EventView, error) {
	return nil, errors.New("the database is down")
}

// A store that fails answers 500 "erro interno" and logs it, with nothing of the
// events in the log.
func TestMineStoreFailureIs500(t *testing.T) {
	w := newEvWorld(t)
	mineEvent(t, w, w.Alfa, "Noite Verde", mineStart(14, 22), 360)
	w.App.Store = repoOverride{w.Store, func(r *domain.Repos) { r.Events = mineFailingEvents{r.Events} }}
	logs := captureLogs(w.App)

	rec := serve(w.App.Router(), http.MethodGet, "/meus-eventos", withCookie(w.Cookie[w.Owner.ID]))
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
		t.Fatalf("%d %q, want 500 \"erro interno\"", rec.Code, rec.Body)
	}
	var logged bool
	for _, l := range logLines(t, logs) {
		if l["level"] == "ERROR" && l["msg"] != "request" {
			logged = true
		}
	}
	if !logged {
		t.Errorf("no error was logged: %s", logs)
	}
	if strings.Contains(logs.String(), "Noite Verde") {
		t.Errorf("the log carries the event's name: %s", logs)
	}
}
