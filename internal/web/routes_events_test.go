package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The fixed texts of /eventos/:id (see routes_events.go and the templates
// pages/app/evento.html and partials/event-detail.html). The ?msg= codes are the
// only way a notice appears: the URL never supplies text.
const (
	evdBlockedNote = "o admin bloqueou os eventos deste coletivo: este evento não pode ser editado nem excluído por enquanto."
	evdConfirmText = "isto apaga o evento para todos. não dá para desfazer."
	evdNotFoundMsg = "404 page not found"
	evdNoID        = "000000000000000000000000"
)

// evdEnv is the shared cast (evWorld) behind a router, plus Dora, a Membro of
// Delta (evWorld has no Membro there): Delta's events are blocked, so she is the
// one who must not see the Administrador's note.
type evdEnv struct {
	evWorld
	r    http.Handler
	dora domain.User
}

func newEvdEnv(t *testing.T) evdEnv {
	t.Helper()
	w := newEvWorld(t)
	dora := insertWebUser(t, w.Store, "dora@agenda.test", func(u *domain.User) { u.Name = "Dora Teste" })
	evMember(t, w.Store, dora.ID, w.Delta.ID, domain.RoleMember)
	w.Cookie[dora.ID] = loginAs(t, w.App, dora)
	return evdEnv{evWorld: w, r: w.App.Router(), dora: dora}
}

func (e evdEnv) get(u domain.User, target string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	return serve(e.r, http.MethodGet, target, append([]func(*http.Request){withCookie(e.Cookie[u.ID])}, mod...)...)
}

// del posts the deletion of the event id as u, from the app's own origin.
func (e evdEnv) del(u domain.User, id string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	return post(e.r, "/eventos/"+id+"/excluir", "", append([]func(*http.Request){withCookie(e.Cookie[u.ID])}, mod...)...)
}

// exists reports whether the store still holds the event id.
func (e evdEnv) exists(t *testing.T, id string) bool {
	t.Helper()
	_, err := e.Store.Repos().Events.ByID(context.Background(), id)
	switch {
	case err == nil:
		return true
	case errors.Is(err, domain.ErrNotFound):
		return false
	}
	t.Fatalf("read event %s: %v", id, err)
	return false
}

// full is an event with every optional field set.
func (e evdEnv) full(t *testing.T, mutate ...func(*domain.Event)) domain.Event {
	t.Helper()
	return evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID, append([]func(*domain.Event){func(ev *domain.Event) {
		ev.Venue = "Casa da Praia"
		ev.TicketURL = "https://ingressos.example/noite-verde?a=1&b=2"
		ev.Artists = "DJ Um\nDJ Dois"
		ev.Description = "Primeira linha\nSegunda linha"
	}}, mutate...)...)
}

// evdOK fails unless rec is a 200 and returns its body.
func evdOK(t *testing.T, who string, rec *httptest.ResponseRecorder) string {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status = %d, want 200; body %q", who, rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// evdWantUnregistered fails unless rec is, header for header, the answer of a
// path no route serves (want): nobody can tell an event that does not exist from
// a route that does not.
func evdWantUnregistered(t *testing.T, who string, rec, want *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != want.Code || rec.Body.String() != want.Body.String() {
		t.Errorf("%s: %d %q, want %d %q like an unregistered path", who, rec.Code, rec.Body, want.Code, want.Body)
	}
	for _, k := range []string{"Content-Type", "Cache-Control", "Set-Cookie", "HX-Redirect", "Location"} {
		if g, w := rec.Header().Values(k), want.Header().Values(k); !slices.Equal(g, w) {
			t.Errorf("%s: header %s = %q, an unregistered path has %q", who, k, g, w)
		}
	}
}

func TestEvdDetailPageShowsTheEvent(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t)
	rec := e.get(e.Owner, "/eventos/"+ev.ID)
	body := evdOK(t, "owner", rec)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want HTML", ct)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	wantAll(t, "the page", body,
		`<title>Noite Verde · AGENDA CLUBBER</title>`,
		`<h1 `,
		`>Noite Verde</h2>`,
		`id="evento-titulo"`,
		`>Festa</span>`, `>techno</span>`,
		`sábado, 14/11/2026 · 22:00, até 04:00 (&#43;1)`,
		`6h`, `R$ 25,00`,
		`Coletivo Alfa`, `Fortaleza/CE`, `Casa da Praia`,
		`DJ Um`, `DJ Dois`, `Primeira linha`, `Segunda linha`,
	)
	// The sidebar marks the calendar, which the page came from.
	wantAll(t, "the nav", body, `href="/" aria-current="page">Calendário</a>`)
	if attrs := linkAttrs(t, body, "[←] calendário"); !strings.Contains(attrs, `href="/"`) {
		t.Errorf("the way back = %q, want it to point at /", attrs)
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("the page breaks the CSP: %v", v)
	}

	// A free event says so, and one with no optional field shows none of them.
	bare := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID, func(ev *domain.Event) {
		ev.Name = "Roda Simples"
		ev.Free, ev.PriceCents = true, 0
		ev.DurationMin = 90
	})
	body = evdOK(t, "bare event", e.get(e.Owner, "/eventos/"+bare.ID))
	wantAll(t, "the free event", body, `>Free<`, `até 23:30`, `1h30`)
	for _, absent := range []string{"Casa da Praia", "INGRESSOS", "nofollow", "text-pre-line"} {
		if strings.Contains(body, absent) {
			t.Errorf("an event with no optional field shows %q", absent)
		}
	}
}

// An event whose type or style the organizer named shows that text, not "outros".
func TestEvdDetailShowsTheTypeAndStyleTheOrganizerNamed(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t, func(ev *domain.Event) {
		ev.Type, ev.TypeOther = domain.EventTypeOutros, "Sarau"
		ev.Style, ev.StyleOther = domain.StyleOther, "Forró eletrônico"
	})
	body := evdOK(t, "owner", e.get(e.Owner, "/eventos/"+ev.ID))
	wantAll(t, "the page", body, `>Sarau</span>`, `>Forró eletrônico</span>`)
	if strings.Contains(body, ">outros<") || strings.Contains(body, ">Outros<") {
		t.Error("the page shows the word outros instead of the text the organizer typed")
	}
}

// The sheet and the page show the same content: the partial is the whole answer
// to an HTMX request, with no layout around it.
func TestEvdDetailPartialForHTMX(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t)
	rec := e.get(e.Owner, "/eventos/"+ev.ID, hx)
	body := evdOK(t, "htmx", rec)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	wantAll(t, "the partial", body, `id="evento-titulo"`, `>Noite Verde</h2>`, `R$ 25,00`, `Coletivo Alfa`, `[→] EDITAR`)
	for _, absent := range []string{"<html", "<body", "<h1", "<title", "sidebar"} {
		if strings.Contains(body, absent) {
			t.Errorf("the partial carries layout markup %q", absent)
		}
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("the partial breaks the CSP: %v", v)
	}
}

// The text of an event is the person's, shown with its line breaks and escaped.
func TestEvdDetailEscapesWhatPeopleWrote(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t, func(ev *domain.Event) {
		ev.Name = "Noite <i>Verde</i>"
		ev.Venue = "Casa <u>da Praia</u>"
		ev.Artists = "<b>DJ Um</b>\n& DJ Dois"
		ev.Description = "<script>alert(1)</script>\nsegunda linha"
	})
	for who, mod := range map[string][]func(*http.Request){"page": nil, "partial": {hx}} {
		body := evdOK(t, who, e.get(e.Owner, "/eventos/"+ev.ID, mod...))
		wantAll(t, who, body,
			`Noite &lt;i&gt;Verde&lt;/i&gt;`, `Casa &lt;u&gt;da Praia&lt;/u&gt;`,
			`&lt;b&gt;DJ Um&lt;/b&gt;`, `&amp; DJ Dois`,
			`&lt;script&gt;alert(1)&lt;/script&gt;`,
			`class="text-pre-line"`,
		)
		for _, raw := range []string{"<i>", "<u>", "<b>", "<script>alert"} {
			if strings.Contains(body, raw) {
				t.Errorf("%s: %q reaches the page unescaped", who, raw)
			}
		}
		if strings.Count(body, `class="text-pre-line"`) != 2 {
			t.Errorf("%s: want the artists and the description in a text-pre-line block each", who)
		}
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("%s breaks the CSP: %v", who, v)
		}
	}
}

func TestEvdTicketLink(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t)
	body := evdOK(t, "event with a link", e.get(e.Owner, "/eventos/"+ev.ID))
	attrs := linkAttrs(t, body, "[→] INGRESSOS")
	wantAll(t, "the ticket link", attrs,
		`href="https://ingressos.example/noite-verde?a=1&amp;b=2"`,
		`rel="noopener noreferrer nofollow ugc"`, `target="_blank"`)

	// The store refuses a link that is not http or https, so a stored one can
	// never reach the page; the template still checks, with ticketHref.
	none := e.full(t, func(ev *domain.Event) { ev.TicketURL = "" })
	if body := evdOK(t, "event with no link", e.get(e.Owner, "/eventos/"+none.ID)); strings.Contains(body, "INGRESSOS") {
		t.Error("an event with no ticket link shows an INGRESSOS link")
	}
}

// The links to an event's pages carry its ID as one escaped path segment: an
// opaque ID never changes the path it ends up in.
func TestEvdEventURL(t *testing.T) {
	for _, tc := range []struct{ id, action, want string }{
		{"65f0c0ffee65f0c0ffee0001", "editar", "/eventos/65f0c0ffee65f0c0ffee0001/editar"},
		{"65f0c0ffee65f0c0ffee0001", "excluir", "/eventos/65f0c0ffee65f0c0ffee0001/excluir"},
		{"a/b?c#d e", "editar", "/eventos/a%2Fb%3Fc%23d%20e/editar"},
		{"../x", "excluir", "/eventos/..%2Fx/excluir"},
	} {
		if got := eventURL(tc.id, tc.action); got != tc.want {
			t.Errorf("eventURL(%q, %q) = %q, want %q", tc.id, tc.action, got, tc.want)
		}
	}
}

func TestEvdTicketHref(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://ingressos.example/x?a=1&b=2", "https://ingressos.example/x?a=1&b=2"},
		{"http://ingressos.example/x", "http://ingressos.example/x"},
		{"HTTPS://INGRESSOS.example/x", "HTTPS://INGRESSOS.example/x"},
		{"", ""},
		{"javascript:alert(1)", ""},
		{"JavaScript:alert(1)", ""},
		{"java\tscript:alert(1)", ""},
		{" https://ingressos.example/x", ""},
		{"data:text/html,x", ""},
		{"mailto:a@ingressos.example", ""},
		{"//ingressos.example/x", ""},
		{"ingressos.example/x", ""},
		{"https:///x", ""},
		{"https://", ""},
		{"ftp://ingressos.example/x", ""},
	} {
		if got := ticketHref(tc.in); got != tc.want {
			t.Errorf("ticketHref(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// What the page offers follows what the write flows allow (events.Detail).
func TestEvdDetailOffersWhatTheViewerMayDo(t *testing.T) {
	e := newEvdEnv(t)
	alfa := e.full(t)
	delta := evEvent(t, e.Store, e.Delta.ID, e.Owner.ID, func(ev *domain.Event) { ev.Name = "Noite Bloqueada" })

	tests := []struct {
		name        string
		who         domain.User
		ev          domain.Event
		offers      bool // the Editar link and the delete form
		blockedNote bool
	}{
		{"an Administrador of the collective", e.Owner, alfa, true, false},
		{"the site admin", e.Admin, alfa, true, false},
		{"a Membro", e.Member, alfa, false, false},
		{"somebody outside", e.Outsider, alfa, false, false},
		{"an Administrador, events blocked", e.Owner, delta, false, true},
		{"the site admin, events blocked", e.Admin, delta, true, false},
		{"a Membro, events blocked", e.dora, delta, false, false},
		{"somebody outside, events blocked", e.Outsider, delta, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for what, mod := range map[string][]func(*http.Request){"page": nil, "partial": {hx}} {
				body := evdOK(t, what, e.get(tt.who, "/eventos/"+tt.ev.ID, mod...))
				edit := `href="/eventos/` + tt.ev.ID + `/editar"`
				del := `action="/eventos/` + tt.ev.ID + `/excluir"`
				for _, c := range []struct {
					what, text string
					want       bool
				}{
					{"the Editar link", edit, tt.offers},
					{"the delete form", del, tt.offers},
					{"the confirmation", evdConfirmText, tt.offers},
					{"the blocked note", evdBlockedNote, tt.blockedNote},
				} {
					if got := strings.Contains(body, c.text); got != c.want {
						t.Errorf("%s: %s present = %v, want %v", what, c.what, got, c.want)
					}
				}
			}
		})
	}

	t.Run("the delete form is a confirmed, danger POST", func(t *testing.T) {
		body := evdOK(t, "owner", e.get(e.Owner, "/eventos/"+alfa.ID))
		form := tagAttrs(t, body, `<form ([^>]*action="/eventos/`+alfa.ID+`/excluir"[^>]*)>`)
		wantAll(t, "the delete form", form, `method="post"`)
		wantAll(t, "the page", body, `<details`, `excluir evento`, `btn btn--danger`)
		if strings.Index(body, "<details") > strings.Index(body, `/excluir"`) {
			t.Error("the delete form is not inside the <details>")
		}
	})
}

// A ?msg= code stands for fixed text; anything else shows nothing.
func TestEvdDetailNotices(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t)
	for code, text := range map[string]string{
		"evento-criado": "evento criado.",
		"evento-salvo":  "alterações salvas.",
	} {
		body := evdOK(t, code, e.get(e.Owner, "/eventos/"+ev.ID+"?msg="+code))
		wantAll(t, code, body, `<p class="status status--ok" role="status">`+text+`</p>`)
	}
	for _, code := range []string{"evento-excluido", "desconhecido", "%3Cb%3Ex%3C%2Fb%3E", ""} {
		body := evdOK(t, code, e.get(e.Owner, "/eventos/"+ev.ID+"?msg="+code))
		if strings.Contains(body, "status--ok") || strings.Contains(body, "evento criado.") || strings.Contains(body, "alterações salvas.") {
			t.Errorf("?msg=%q shows a notice", code)
		}
	}
}

// Every refusal to show or delete an event that is not there is the 404 of a path
// no route serves, and a visitor is sent to sign in.
func TestEvdUnknownEventIsAnUnregisteredPath(t *testing.T) {
	e := newEvdEnv(t)
	want := e.get(e.Owner, "/eventos-que-nao-existe")
	if want.Code != http.StatusNotFound || want.Body.String() != evdNotFoundMsg || want.Header().Get("Content-Type") != "text/plain" {
		t.Fatalf("an unregistered path answers %d %q (%s), want 404 %q (text/plain)", want.Code, want.Body, want.Header().Get("Content-Type"), evdNotFoundMsg)
	}
	for _, id := range []string{evdNoID, "nao-e-um-id"} {
		for _, who := range []domain.User{e.Owner, e.Admin} {
			evdWantUnregistered(t, "GET "+id, e.get(who, "/eventos/"+id), want)
			evdWantUnregistered(t, "GET "+id+" (htmx)", e.get(who, "/eventos/"+id, hx), want)
			evdWantUnregistered(t, "POST "+id, e.del(who, id), want)
			evdWantUnregistered(t, "POST "+id+" (htmx)", e.del(who, id, hx), want)
		}
	}
}

func TestEvdVisitor(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t)
	rec := serve(e.r, http.MethodGet, "/eventos/"+ev.ID)
	if want := "/entrar?next=%2Feventos%2F" + ev.ID; rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
		t.Errorf("anonymous GET: %d, Location %q; want 303 to %s", rec.Code, rec.Header().Get("Location"), want)
	}
	rec = post(e.r, "/eventos/"+ev.ID+"/excluir", "")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar" {
		t.Errorf("anonymous POST: %d, Location %q; want 303 to /entrar", rec.Code, rec.Header().Get("Location"))
	}
	if !e.exists(t, ev.ID) {
		t.Error("an anonymous POST deleted the event")
	}
}

func TestEvdDeleteNeedsTheAppsOrigin(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t)
	target := "/eventos/" + ev.ID + "/excluir"
	for name, mod := range map[string]func(*http.Request){
		"no Origin":     func(*http.Request) {},
		"a foreign one": header("Origin", "https://outro.example"),
	} {
		rec := do(e.r, http.MethodPost, target, nil, withCookie(e.Cookie[e.Owner.ID]), mod)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, rec.Code)
		}
	}
	if !e.exists(t, ev.ID) {
		t.Error("a post from another origin deleted the event")
	}
}

func TestEvdDeleteByAnAdministrador(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t)
	other := e.full(t, func(ev *domain.Event) { ev.Name = "Outro Evento" })
	rec := e.del(e.Owner, ev.ID)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?msg=evento-excluido" {
		t.Fatalf("status = %d, Location %q; want 303 to /?msg=evento-excluido", rec.Code, rec.Header().Get("Location"))
	}
	if e.exists(t, ev.ID) {
		t.Error("the event is still there")
	}
	if !e.exists(t, other.ID) {
		t.Error("another event went with it")
	}
	// One entry in the audit log, by the Administrador who deleted it.
	entries, err := e.Store.Repos().AuditEntries.List(context.Background(), 50)
	if err != nil {
		t.Fatalf("list the audit log: %v", err)
	}
	var deleted []domain.AuditEntry
	for _, en := range entries {
		if en.Action == domain.AuditEventDeleted {
			deleted = append(deleted, en)
		}
	}
	if len(deleted) != 1 || deleted[0].ActorID != e.Owner.ID || deleted[0].CollectiveID != e.Alfa.ID {
		t.Errorf("event_deleted entries = %+v, want one by the Administrador in Alfa", deleted)
	}
	// The page is gone, and the admin's history says who did what.
	evdWantUnregistered(t, "GET after the delete", e.get(e.Owner, "/eventos/"+ev.ID), e.get(e.Owner, "/eventos-que-nao-existe"))
	wantAll(t, "the history", evdOK(t, "history", e.get(e.Admin, "/admin/historico")), "evento excluído")
}

func TestEvdDeleteViaHTMXRedirects(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t)
	rec := e.del(e.Owner, ev.ID, hx)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/?msg=evento-excluido" {
		t.Errorf("status = %d, HX-Redirect %q; want 204 with HX-Redirect /?msg=evento-excluido", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if e.exists(t, ev.ID) {
		t.Error("the event is still there")
	}
}

func TestEvdDeleteRefusedWithoutTheRight(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t)
	for name, who := range map[string]domain.User{"a Membro": e.Member, "somebody outside": e.Outsider} {
		rec := e.del(who, ev.ID)
		if rec.Code != http.StatusForbidden || rec.Body.String() != approvalsMsgNotAllowed {
			t.Errorf("%s: %d %q, want 403 %q", name, rec.Code, rec.Body, approvalsMsgNotAllowed)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Errorf("%s: Content-Type = %q, want plain text", name, ct)
		}
		if !e.exists(t, ev.ID) {
			t.Errorf("%s: the event is gone", name)
		}
	}
}

// Events blocked by the admin cannot be deleted by an Administrador: the page
// answers 409 with the reason as the alert (once), and the event stays. The site
// admin is not stopped by the block.
func TestEvdDeleteWhileTheCollectivesEventsAreBlocked(t *testing.T) {
	e := newEvdEnv(t)
	ev := evEvent(t, e.Store, e.Delta.ID, e.Owner.ID)
	alert := `<p class="status status--error" role="alert">` + eventsMsgBlocked + `</p>`

	rec := e.del(e.Owner, ev.ID)
	body := rec.Body.String()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %q", rec.Code, body)
	}
	wantAll(t, "the 409 page", body, alert, `<h1 `, `id="evento-titulo"`, `>Noite Verde</h2>`)
	if n := strings.Count(body, eventsMsgBlocked); n != 1 {
		t.Errorf("the reason is in the page %d times, want once", n)
	}
	if strings.Contains(body, evdBlockedNote) {
		t.Error("the page repeats the block as a note next to the alert")
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("the 409 page breaks the CSP: %v", v)
	}
	if !e.exists(t, ev.ID) {
		t.Fatal("the event is gone")
	}

	// An HTMX post gets the content only.
	rec = e.del(e.Owner, ev.ID, hx)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), alert) || strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("htmx: %d %q, want a 409 partial with the alert", rec.Code, rec.Body)
	}

	rec = e.del(e.Admin, ev.ID)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/?msg=evento-excluido" {
		t.Errorf("site admin: %d, Location %q; want 303 to /?msg=evento-excluido", rec.Code, rec.Header().Get("Location"))
	}
	if e.exists(t, ev.ID) {
		t.Error("the site admin's delete left the event")
	}
}

// evdBrokenEvents is a store whose reads of an event fail, so a page that passed
// the session check ends in a server error.
type evdBrokenEvents struct{ domain.Store }

func (s evdBrokenEvents) Repos() domain.Repos {
	r := s.Store.Repos()
	r.Events = evdFailingEvents{r.Events}
	return r
}

type evdFailingEvents struct{ domain.Events }

func (evdFailingEvents) ByID(context.Context, string) (domain.EventView, error) {
	return domain.EventView{}, errors.New("the database is down")
}

// A store that fails answers 500 "erro interno" and logs it, with nothing of the
// event in the log.
func TestEvdStoreFailureIs500(t *testing.T) {
	e := newEvdEnv(t)
	ev := e.full(t)
	for name, tc := range map[string]struct {
		store domain.Store
		do    func(r http.Handler) *httptest.ResponseRecorder
	}{
		"GET": {evdBrokenEvents{e.Store}, func(r http.Handler) *httptest.ResponseRecorder {
			return serve(r, http.MethodGet, "/eventos/"+ev.ID, withCookie(e.Cookie[e.Owner.ID]))
		}},
		"POST": {colFailingStore{e.Store}, func(r http.Handler) *httptest.ResponseRecorder {
			return post(r, "/eventos/"+ev.ID+"/excluir", "", withCookie(e.Cookie[e.Owner.ID]))
		}},
	} {
		t.Run(name, func(t *testing.T) {
			e.App.Store = tc.store
			logs := captureLogs(e.App)
			rec := tc.do(e.App.Router())
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
			for _, leak := range []string{ev.Name, ev.Artists, ev.Description} {
				if strings.Contains(logs.String(), leak) {
					t.Errorf("the log carries %q: %s", leak, logs)
				}
			}
		})
	}
}
