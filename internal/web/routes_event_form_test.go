package web

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

// Tests of the event form pages: GET and POST /eventos/novo and
// /eventos/:id/editar (routes_event_form.go, pages/app/evento-form.html and
// partials/event-fields.html). The forms are plain HTML posts: nothing here runs
// JavaScript, and the second submit button ("atualizar") is the way a browser
// without it loads the cities of a state and the substyles of a style.

// evfEnv is the cast of the detail tests (evdEnv: the shared world plus Dora, a
// Membro of the blocked Delta) with the helpers of the form tests.
type evfEnv struct{ evdEnv }

func newEvfEnv(t *testing.T) evfEnv { return evfEnv{newEvdEnv(t)} }

// send posts the values to target as u, from the app's own origin.
func (e evfEnv) send(u domain.User, target string, v url.Values, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	return post(e.r, target, v.Encode(), append([]func(*http.Request){withCookie(e.Cookie[u.ID])}, mod...)...)
}

// stored reads the event id from the store.
func (e evfEnv) stored(t *testing.T, id string) domain.EventView {
	t.Helper()
	ev, err := e.Store.Repos().Events.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("read event %s: %v", id, err)
	}
	return ev
}

// count is how many events the collective holds.
func (e evfEnv) count(t *testing.T, collectiveID string) int {
	t.Helper()
	evs, err := e.Store.Repos().Events.ListByCollectives(context.Background(), []string{collectiveID}, domain.ListLimitMax)
	if err != nil {
		t.Fatalf("list events of %s: %v", collectiveID, err)
	}
	return len(evs)
}

// evfBody returns the body of rec, failing unless its status is want.
func evfBody(t *testing.T, who string, rec *httptest.ResponseRecorder, want int) string {
	t.Helper()
	body := rec.Body.String()
	if rec.Code != want {
		t.Fatalf("%s: status = %d, want %d; body %.300q", who, rec.Code, want, body)
	}
	return body
}

// evfRedirect fails unless rec is a 303 to /eventos/<id>?msg=<msg> and returns the ID.
func evfRedirect(t *testing.T, who string, rec *httptest.ResponseRecorder, msg string) string {
	t.Helper()
	evfBody(t, who, rec, http.StatusSeeOther)
	loc := rec.Header().Get("Location")
	rest, ok := strings.CutPrefix(loc, "/eventos/")
	id, ok2 := strings.CutSuffix(rest, "?msg="+msg)
	if !ok || !ok2 || id == "" || strings.ContainsAny(id, "/?") {
		t.Fatalf("%s: Location = %q, want /eventos/<id>?msg=%s", who, loc, msg)
	}
	return id
}

// evfCreateForm is a valid form for a new event of the collective: Saturday
// 14/11/2026 at 22:00 in Fortaleza for six hours, as a browser posts it (CRLF in
// the textareas).
func evfCreateForm(collectiveID string) url.Values {
	return url.Values{
		"collective_id":    {collectiveID},
		"name":             {"Noite Neon"},
		"type":             {"festa"},
		"date":             {"14/11/2026"},
		"time":             {"22:00"},
		"duration_hours":   {"6"},
		"duration_minutes": {"0"},
		"price":            {"25,00"},
		"ticket_url":       {"https://ingressos.example/neon?a=1&b=2"},
		"style":            {"techno"},
		"substyle":         {"deep techno"},
		"state":            {"CE"},
		"city":             {"Fortaleza"},
		"venue":            {"Casa da Praia"},
		"artists":          {"DJ Um\r\nDJ Dois"},
		"description":      {"Primeira linha\r\nSegunda linha"},
	}
}

// evfEditForm is the form of the edit page for an event: what evfCreateForm
// holds, without the collective (the page shows it read-only), on the given
// date, with the version the page was read at.
func evfEditForm(date string, version int64) url.Values {
	v := evfCreateForm("")
	v.Del("collective_id")
	v.Set("name", "Noite Verde")
	v.Set("date", date)
	v.Set("version", strconv.FormatInt(version, 10))
	return v
}

var (
	evfOptionRe = regexp.MustCompile(`<option value="([^"]*)"( selected)?>`)
	evfLabelRe  = regexp.MustCompile(`<label\b([^>]*)>`)
	evfForRe    = regexp.MustCompile(`\sfor="([^"]*)"`)
	evfIDRe     = regexp.MustCompile(`\sid="([^"]*)"`)
	evfDescRe   = regexp.MustCompile(`\saria-describedby="([^"]*)"`)
	evfClassRe  = regexp.MustCompile(`\sclass="([^"]*)"`)
	evfControl  = regexp.MustCompile(`<(?:input|select|textarea)\b([^>]*)>`)
)

// evfSelect returns the <select name=...> element of the page.
func evfSelect(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<select\b[^>]*\bname="` + regexp.QuoteMeta(name) + `"[^>]*>.*?</select>`).FindString(body)
	if m == "" {
		t.Fatalf("no <select name=%q> in:\n%.600s", name, body)
	}
	return m
}

// evfOptions returns the option values of a select, in order, and the selected one.
func evfOptions(sel string) (values []string, selected string) {
	for _, m := range evfOptionRe.FindAllStringSubmatch(sel, -1) {
		values = append(values, m[1])
		if m[2] != "" {
			selected = m[1]
		}
	}
	return values, selected
}

// evfInput returns the attributes of the <input name=...> of the page.
func evfInput(t *testing.T, body, name string) string {
	t.Helper()
	return tagAttrs(t, body, `<input\b([^>]*\bname="`+regexp.QuoteMeta(name)+`"[^>]*)>`)
}

// evfTextarea returns the text of the <textarea name=...>.
func evfTextarea(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<textarea\b[^>]*\bname="` + regexp.QuoteMeta(name) + `"[^>]*>(.*?)</textarea>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no <textarea name=%q> in:\n%.600s", name, body)
	}
	return m[1]
}

// evfNoField fails when the page has a control called name.
func evfNoField(t *testing.T, what, body, name string) {
	t.Helper()
	if regexp.MustCompile(`<(?:input|select|textarea)\b[^>]*\bname="` + regexp.QuoteMeta(name) + `"`).MatchString(body) {
		t.Errorf("%s: the page has a control named %q", what, name)
	}
}

func TestEvfNewPage(t *testing.T) {
	e := newEvfEnv(t)

	rec := e.get(e.Owner, "/eventos/novo")
	body := evdOK(t, "owner", rec)
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want HTML", ct)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	wantAll(t, "the page", body,
		`<title>Criar Evento · AGENDA CLUBBER</title>`,
		`href="/eventos/novo" aria-current="page">Criar Evento</a>`,
		`[→] CRIAR`, `[↻] ATUALIZAR LISTAS`,
	)
	wantAll(t, "the form", tagAttrs(t, body, `<form\b([^>]*\baction="/eventos/novo"[^>]*)>`), `method="post"`)
	if attrs := linkAttrs(t, body, "Cancelar"); !strings.Contains(attrs, `href="/"`) {
		t.Errorf("Cancelar = %q, want it to point at /", attrs)
	}
	// The primary button comes first: Enter in a text field presses the first
	// submit button of the form, and that must be the one that saves.
	inOrder(t, body, `[→] CRIAR`, `[↻] ATUALIZAR LISTAS`)
	wantAll(t, "the refresh button", tagAttrs(t, body, `<button\b([^>]*)>\s*\[↻\] ATUALIZAR LISTAS\s*</button>`),
		`type="submit"`, `name="acao"`, `value="atualizar"`, `formnovalidate`)
	evfNoField(t, "a new event", body, "version")

	// Only Alfa is offered: Delta's events are blocked. With one collective, it is chosen.
	values, selected := evfOptions(evfSelect(t, body, "collective_id"))
	if !slices.Equal(values, []string{"", e.Alfa.ID}) || selected != e.Alfa.ID {
		t.Errorf("collective options = %q, selected %q; want only Alfa, selected", values, selected)
	}
	if strings.Contains(body, "Coletivo Delta") {
		t.Error("the blocked collective is offered")
	}
	wantAll(t, "the collective select", evfSelect(t, body, "collective_id"), `Coletivo Alfa`)

	// With no state or style chosen, the dependent selects have nothing to list.
	city, _ := evfOptions(evfSelect(t, body, "city"))
	if !slices.Equal(city, []string{""}) || !strings.Contains(evfSelect(t, body, "city"), "escolha o estado primeiro") {
		t.Errorf("city options = %q, want only the empty one, saying to choose the state first", city)
	}
	if sub, _ := evfOptions(evfSelect(t, body, "substyle")); !slices.Equal(sub, []string{""}) {
		t.Errorf("substyle options = %q, want only the empty one", sub)
	}
	state, _ := evfOptions(evfSelect(t, body, "state"))
	if len(state) != 28 || !slices.Contains(state, "CE") || !slices.Contains(state, "SP") {
		t.Errorf("state options = %d, want the empty one and the 27 UFs", len(state))
	}
	style, _ := evfOptions(evfSelect(t, body, "style"))
	if !slices.Contains(style, "techno") || style[len(style)-1] != "outros" {
		t.Errorf("style options = %q, want the catalog styles then outros", style)
	}
	typ, _ := evfOptions(evfSelect(t, body, "type"))
	if !slices.Equal(typ, []string{"", "festa", "rave", "festival", "workshop", "feira", "outros"}) {
		t.Errorf("type options = %q", typ)
	}
	wantAll(t, "the type select", evfSelect(t, body, "type"), `>Festa</option>`, `>Outros</option>`)

	// The fields a later task binds to, and how a browser should treat them.
	wantAll(t, "date", evfInput(t, body, "date"), `id="date"`, `type="text"`, `inputmode="numeric"`, `placeholder="dd/mm/aaaa"`)
	wantAll(t, "time", evfInput(t, body, "time"), `placeholder="HH:MM"`)
	wantAll(t, "hours", evfInput(t, body, "duration_hours"), `type="number"`, `min="0"`, `max="23"`)
	wantAll(t, "minutes", evfInput(t, body, "duration_minutes"), `type="number"`, `min="0"`, `max="59"`)
	wantAll(t, "price", evfInput(t, body, "price"), `id="price"`, `inputmode="decimal"`, `placeholder="0,00"`, `input--money`)
	wantAll(t, "free", evfInput(t, body, "free"), `id="free"`, `type="checkbox"`)
	wantAll(t, "ticket_url", evfInput(t, body, "ticket_url"), `type="url"`)
	for _, id := range []string{"style", "substyle", "state", "city"} {
		wantAll(t, id, evfSelect(t, body, id), `id="`+id+`"`)
	}
	wantAll(t, "the hints", body, `só se o tipo for Outros`, `só se o estilo for Outros`)

	// Required fields are marked, with the attribute the marker promises.
	for _, id := range []string{"collective_id", "name", "type", "date", "time", "duration_hours", "style", "state", "city"} {
		wantAll(t, "label of "+id, body, `class="label label--required" for="`+id+`"`)
	}
	for _, id := range []string{"type_other", "ticket_url", "substyle", "style_other", "venue", "artists", "description"} {
		if strings.Contains(body, `label--required" for="`+id+`"`) {
			t.Errorf("%s is optional, but its label is marked required", id)
		}
	}

	// Two collectives: nothing is chosen for the person.
	beta := evCollective(t, e.Store, "Coletivo Beta", e.Owner.ID)
	evMember(t, e.Store, e.Owner.ID, beta.ID, domain.RoleOwner)
	body = evdOK(t, "owner with two collectives", e.get(e.Owner, "/eventos/novo"))
	values, selected = evfOptions(evfSelect(t, body, "collective_id"))
	if !slices.Equal(values, []string{"", e.Alfa.ID, beta.ID}) || selected != "" {
		t.Errorf("collective options = %q, selected %q; want the empty one, Alfa and Beta, none chosen", values, selected)
	}

	// Everyone else has nothing to create in.
	for _, tc := range []struct {
		who  string
		user domain.User
	}{{"member", e.Member}, {"outsider", e.Outsider}, {"admin", e.Admin}} {
		rec := e.get(tc.user, "/eventos/novo")
		if got := evfBody(t, tc.who, rec, http.StatusForbidden); got != approvalsMsgNotAllowed {
			t.Errorf("%s: body = %q, want %q", tc.who, got, approvalsMsgNotAllowed)
		}
	}
	rec = serve(e.r, http.MethodGet, "/eventos/novo")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/entrar") {
		t.Errorf("anonymous: %d to %q, want 303 to the login", rec.Code, rec.Header().Get("Location"))
	}
}

func TestEvfCreate(t *testing.T) {
	e := newEvfEnv(t)
	ctx := context.Background()

	t.Run("a valid form creates the event", func(t *testing.T) {
		id := evfRedirect(t, "owner", e.send(e.Owner, "/eventos/novo", evfCreateForm(e.Alfa.ID)), "evento-criado")
		got := e.stored(t, id)
		if got.CollectiveID != e.Alfa.ID || got.CreatedBy != e.Owner.ID || got.Name != "Noite Neon" || got.Type != domain.EventTypeFesta {
			t.Errorf("stored = %+v", got.Event)
		}
		// 22:00 in Fortaleza is 01:00 UTC of the next day.
		if want := time.Date(2026, 11, 15, 1, 0, 0, 0, time.UTC); !got.StartsAt.Truncate(time.Millisecond).Equal(want) {
			t.Errorf("StartsAt = %s, want %s", got.StartsAt, want)
		}
		if got.DurationMin != 360 || got.PriceCents != 2500 || got.Free {
			t.Errorf("duration %d, price %d, free %v; want 360, 2500, false", got.DurationMin, got.PriceCents, got.Free)
		}
		if got.Style != "techno" || got.Substyle != "deep techno" || got.State != "CE" || got.City != "Fortaleza" || got.Venue != "Casa da Praia" {
			t.Errorf("stored genre and place = %+v", got.Event)
		}
		if got.TicketURL != "https://ingressos.example/neon?a=1&b=2" || got.Artists != "DJ Um\nDJ Dois" || got.Description != "Primeira linha\nSegunda linha" {
			t.Errorf("stored ticket and texts = %q, %q, %q", got.TicketURL, got.Artists, got.Description)
		}
		// The redirect lands on the event, with its notice.
		body := evdOK(t, "after the redirect", e.get(e.Owner, "/eventos/"+id+"?msg=evento-criado"))
		wantAll(t, "the page", body, "evento criado.", "Noite Neon")
	})

	t.Run("a free event and the Outros choices", func(t *testing.T) {
		f := evfCreateForm(e.Alfa.ID)
		f.Set("name", "Sarau Livre")
		f.Set("free", "on")
		f.Set("price", "")
		f.Set("type", "outros")
		f.Set("type_other", "Sarau")
		f.Set("style", "outros")
		f.Set("style_other", "Forró eletrônico")
		f.Set("substyle", "")
		got := e.stored(t, evfRedirect(t, "owner", e.send(e.Owner, "/eventos/novo", f), "evento-criado"))
		if !got.Free || got.PriceCents != 0 || got.Type != domain.EventTypeOutros || got.TypeOther != "Sarau" || got.Style != "outros" || got.StyleOther != "Forró eletrônico" {
			t.Errorf("stored = %+v", got.Event)
		}
	})

	t.Run("an invalid form comes back with the values, the errors and the lists of what was chosen", func(t *testing.T) {
		before := e.count(t, e.Alfa.ID)
		f := evfCreateForm(e.Alfa.ID)
		f.Set("name", "")
		f.Set("date", "31/02/2026")
		f.Set("city", "Recife") // a city of PE, with CE chosen
		f.Set("substyle", "acid house")
		f.Set("venue", `Casa <b>da</b> Praia`)
		f.Set("artists", "DJ Um\nDJ Dois")
		rec := e.send(e.Owner, "/eventos/novo", f)
		body := evfBody(t, "invalid", rec, http.StatusUnprocessableEntity)
		if got := rec.Header().Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
		// The errors sit under their fields, linked to them.
		wantAll(t, "the name error", body, `id="erro-name">campo obrigatório</p>`)
		wantAll(t, "the date error", body, `id="erro-date">use uma data válida, no formato dd/mm/aaaa</p>`)
		for field, control := range map[string]string{"name": evfInput(t, body, "name"), "date": evfInput(t, body, "date")} {
			wantAll(t, field, control, `aria-invalid="true"`, `erro-`+field)
		}
		wantAll(t, "the city", evfSelect(t, body, "city")[:strings.Index(evfSelect(t, body, "city"), ">")], `aria-invalid="true"`, `erro-city`)
		wantAll(t, "the substyle", evfSelect(t, body, "substyle")[:strings.Index(evfSelect(t, body, "substyle"), ">")], `aria-invalid="true"`, `erro-substyle`)
		for _, ok := range []string{"erro-time", "erro-price", "erro-state", "erro-style", "erro-type", "erro-duration"} {
			if strings.Contains(body, ok) {
				t.Errorf("the page reports %s, but that field was valid", ok)
			}
		}
		// What the person typed is kept, escaped.
		wantAll(t, "time", evfInput(t, body, "time"), `value="22:00"`)
		wantAll(t, "price", evfInput(t, body, "price"), `value="25,00"`)
		wantAll(t, "date", evfInput(t, body, "date"), `value="31/02/2026"`)
		wantAll(t, "venue", evfInput(t, body, "venue"), `value="Casa &lt;b&gt;da&lt;/b&gt; Praia"`)
		if got := evfTextarea(t, body, "artists"); got != "DJ Um\nDJ Dois" {
			t.Errorf("artists = %q", got)
		}
		if strings.Contains(body, "<b>da</b>") {
			t.Error("a typed tag reached the page unescaped")
		}
		_, chosen := evfOptions(evfSelect(t, body, "collective_id"))
		if chosen != e.Alfa.ID {
			t.Errorf("collective selected = %q, want Alfa", chosen)
		}
		// The lists are those of the state and the style that were submitted.
		cities, selectedCity := evfOptions(evfSelect(t, body, "city"))
		if !slices.Contains(cities, "Fortaleza") || slices.Contains(cities, "Recife") || selectedCity != "" {
			t.Errorf("cities = %d options, selected %q; want those of CE, with none selected", len(cities), selectedCity)
		}
		if _, state := evfOptions(evfSelect(t, body, "state")); state != "CE" {
			t.Errorf("state selected = %q, want CE", state)
		}
		subs, _ := evfOptions(evfSelect(t, body, "substyle"))
		if !slices.Contains(subs, "deep techno") || slices.Contains(subs, "acid house") || subs[len(subs)-1] != "outros" {
			t.Errorf("substyles = %q, want those of techno, then outros", subs)
		}
		if _, style := evfOptions(evfSelect(t, body, "style")); style != "techno" {
			t.Errorf("style selected = %q, want techno", style)
		}
		if n := e.count(t, e.Alfa.ID); n != before {
			t.Errorf("the collective holds %d events, want %d: an invalid form stored one", n, before)
		}
	})

	t.Run("a missing collective is a field error", func(t *testing.T) {
		f := evfCreateForm("")
		body := evfBody(t, "no collective", e.send(e.Owner, "/eventos/novo", f), http.StatusUnprocessableEntity)
		wantAll(t, "the page", body, `id="erro-collective_id">campo obrigatório</p>`)
	})

	t.Run("a forged collective", func(t *testing.T) {
		before := e.count(t, e.Delta.ID)
		// Blocked: the form comes back with the reason.
		rec := e.send(e.Owner, "/eventos/novo", evfCreateForm(e.Delta.ID))
		body := evfBody(t, "blocked", rec, http.StatusConflict)
		wantAll(t, "the alert", body, `role="alert"`, eventsMsgBlocked)
		wantAll(t, "the values", evfInput(t, body, "name"), `value="Noite Neon"`)
		// A collective the person is not an Administrador of, or that is not there.
		gama := evCollective(t, e.Store, "Coletivo Gama", e.Owner.ID)
		for _, tc := range []struct{ who, id string }{{"not an Administrador", gama.ID}, {"unknown", evdNoID}} {
			if got := evfBody(t, tc.who, e.send(e.Owner, "/eventos/novo", evfCreateForm(tc.id)), http.StatusForbidden); got != approvalsMsgNotAllowed {
				t.Errorf("%s: body = %q, want %q", tc.who, got, approvalsMsgNotAllowed)
			}
		}
		// Whoever has no collective to create in gets the refusal of the page, whatever they post.
		for _, tc := range []struct {
			who  string
			user domain.User
		}{{"member", e.Member}, {"outsider", e.Outsider}, {"admin", e.Admin}} {
			if got := evfBody(t, tc.who, e.send(tc.user, "/eventos/novo", evfCreateForm(e.Alfa.ID)), http.StatusForbidden); got != approvalsMsgNotAllowed {
				t.Errorf("%s: body = %q, want %q", tc.who, got, approvalsMsgNotAllowed)
			}
			if got := evfBody(t, tc.who+" posting nothing", e.send(tc.user, "/eventos/novo", url.Values{}), http.StatusForbidden); got != approvalsMsgNotAllowed {
				t.Errorf("%s posting nothing: body = %q, want %q", tc.who, got, approvalsMsgNotAllowed)
			}
		}
		if n := e.count(t, e.Delta.ID) + e.count(t, gama.ID); n != before {
			t.Errorf("a refused post stored %d events", n-before)
		}
	})

	t.Run("without an Origin nothing happens", func(t *testing.T) {
		before := e.count(t, e.Alfa.ID)
		rec := do(e.r, http.MethodPost, "/eventos/novo", strings.NewReader(evfCreateForm(e.Alfa.ID).Encode()),
			header("Content-Type", "application/x-www-form-urlencoded"), withCookie(e.Cookie[e.Owner.ID]))
		evfBody(t, "no Origin", rec, http.StatusForbidden)
		if n := e.count(t, e.Alfa.ID); n != before {
			t.Errorf("a post without Origin stored %d events", n-before)
		}
	})

	t.Run("ATUALIZAR LISTAS loads the lists and saves nothing", func(t *testing.T) {
		before := e.count(t, e.Alfa.ID)
		f := evfCreateForm(e.Alfa.ID)
		f.Set("name", "") // would be an error, but nothing is validated
		f.Set("state", "PE")
		f.Set("city", "Fortaleza")
		f.Set("style", "house")
		f.Set("substyle", "deep techno")
		f.Set("acao", "atualizar")
		body := evfBody(t, "atualizar", e.send(e.Owner, "/eventos/novo", f), http.StatusOK)
		if strings.Contains(body, `class="field-error"`) {
			t.Error("the refresh shows errors")
		}
		cities, _ := evfOptions(evfSelect(t, body, "city"))
		if !slices.Contains(cities, "Recife") || slices.Contains(cities, "Fortaleza") {
			t.Errorf("cities = %d options, want those of PE", len(cities))
		}
		subs, _ := evfOptions(evfSelect(t, body, "substyle"))
		if !slices.Contains(subs, "acid house") || slices.Contains(subs, "deep techno") {
			t.Errorf("substyles = %q, want those of house", subs)
		}
		wantAll(t, "the values", evfInput(t, body, "time"), `value="22:00"`)
		if _, state := evfOptions(evfSelect(t, body, "state")); state != "PE" {
			t.Errorf("state selected = %q, want PE", state)
		}
		if n := e.count(t, e.Alfa.ID); n != before {
			t.Errorf("the refresh stored %d events", n-before)
		}

		// Outros as the style leaves nothing to choose from.
		f.Set("style", "outros")
		body = evfBody(t, "atualizar with outros", e.send(e.Owner, "/eventos/novo", f), http.StatusOK)
		if subs, _ := evfOptions(evfSelect(t, body, "substyle")); !slices.Equal(subs, []string{""}) {
			t.Errorf("substyles of outros = %q, want only the empty one", subs)
		}
		// Neither does a style that does not exist, nor a state that does not.
		f.Set("style", "nope")
		f.Set("state", "XX")
		body = evfBody(t, "atualizar with nonsense", e.send(e.Owner, "/eventos/novo", f), http.StatusOK)
		if subs, _ := evfOptions(evfSelect(t, body, "substyle")); !slices.Equal(subs, []string{""}) {
			t.Errorf("substyles of an unknown style = %q, want only the empty one", subs)
		}
		if cities, _ := evfOptions(evfSelect(t, body, "city")); !slices.Equal(cities, []string{""}) {
			t.Errorf("cities of an unknown state = %q, want only the empty one", cities)
		}
	})

	if n := e.count(t, e.Alfa.ID); n != 2 {
		t.Errorf("the collective holds %d events at the end, want the 2 valid ones", n)
	}
	_ = ctx
}

func TestEvfEditPage(t *testing.T) {
	e := newEvfEnv(t)
	ev := e.full(t, func(ev *domain.Event) { ev.Substyle = "deep techno" })

	rec := e.get(e.Owner, "/eventos/"+ev.ID+"/editar")
	body := evdOK(t, "owner", rec)
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	wantAll(t, "the page", body,
		`<title>Editar Evento · AGENDA CLUBBER</title>`,
		`href="/" aria-current="page">Calendário</a>`,
		`[→] SALVAR`, `[↻] ATUALIZAR LISTAS`, `Coletivo Alfa`,
	)
	if strings.Contains(body, "[→] CRIAR") {
		t.Error("the edit page offers to create")
	}
	wantAll(t, "the form", tagAttrs(t, body, `<form\b([^>]*\baction="/eventos/`+ev.ID+`/editar"[^>]*)>`), `method="post"`)
	if attrs := linkAttrs(t, body, "Cancelar"); !strings.Contains(attrs, `href="/eventos/`+ev.ID+`"`) {
		t.Errorf("Cancelar = %q, want it to point at the event", attrs)
	}
	inOrder(t, body, `[→] SALVAR`, `[↻] ATUALIZAR LISTAS`)

	// Prefilled the way FormOf says: the date as dd/mm/aaaa and the time in Fortaleza,
	// the duration in hours and minutes, the price as the field reads it back.
	for name, want := range map[string]string{
		"name": "Noite Verde", "date": "14/11/2026", "time": "22:00", "duration_hours": "6", "duration_minutes": "0",
		"price": "25,00", "venue": "Casa da Praia", "ticket_url": "https://ingressos.example/noite-verde?a=1&amp;b=2",
	} {
		wantAll(t, name, evfInput(t, body, name), `value="`+want+`"`)
	}
	wantAll(t, "version", evfInput(t, body, "version"), `type="hidden"`, `value="1"`)
	if strings.Contains(evfInput(t, body, "free"), "checked") {
		t.Error("the free box is checked for a paid event")
	}
	if got := evfTextarea(t, body, "artists"); got != "DJ Um\nDJ Dois" {
		t.Errorf("artists = %q", got)
	}
	if got := evfTextarea(t, body, "description"); got != "Primeira linha\nSegunda linha" {
		t.Errorf("description = %q", got)
	}
	for name, want := range map[string]string{"type": "festa", "style": "techno", "substyle": "deep techno", "state": "CE", "city": "Fortaleza"} {
		if _, got := evfOptions(evfSelect(t, body, name)); got != want {
			t.Errorf("%s selected = %q, want %q", name, got, want)
		}
	}
	if cities, _ := evfOptions(evfSelect(t, body, "city")); !slices.Contains(cities, "Fortaleza") || slices.Contains(cities, "Recife") {
		t.Errorf("cities = %d options, want those of CE", len(cities))
	}
	// The collective is read-only: its name, and no control to change it.
	evfNoField(t, "the edit page", body, "collective_id")

	// A free event is shown free, with no price.
	free := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID, func(ev *domain.Event) { ev.Free, ev.PriceCents = true, 0 })
	body = evdOK(t, "free event", e.get(e.Owner, "/eventos/"+free.ID+"/editar"))
	wantAll(t, "free", evfInput(t, body, "free"), `checked`)
	wantAll(t, "price", evfInput(t, body, "price"), `value=""`)

	// A catalog style with no substyles of its own still takes the substyle outros, so the
	// select must offer it: saving the form unchanged must not drop it.
	pop := e.full(t, func(ev *domain.Event) { ev.Style, ev.Substyle = "pop", "outros" })
	body = evdOK(t, "style without substyles", e.get(e.Owner, "/eventos/"+pop.ID+"/editar"))
	if subs, got := evfOptions(evfSelect(t, body, "substyle")); !slices.Equal(subs, []string{"", "outros"}) || got != "outros" {
		t.Errorf("substyle options = %q, selected %q; want the empty one and outros, outros selected", subs, got)
	}

	// The admin edits an event of a collective they are no Administrador of, blocked or not.
	delta := evEvent(t, e.Store, e.Delta.ID, e.Owner.ID)
	evdOK(t, "admin on Alfa", e.get(e.Admin, "/eventos/"+ev.ID+"/editar"))
	evdOK(t, "admin on blocked Delta", e.get(e.Admin, "/eventos/"+delta.ID+"/editar"))

	// The permission table: the block stops an Administrador (409, with the reason),
	// and a Membro or a stranger is refused whatever the block says (403).
	if got := evfBody(t, "owner on Delta", e.get(e.Owner, "/eventos/"+delta.ID+"/editar"), http.StatusConflict); got != eventsMsgBlocked {
		t.Errorf("owner on Delta: body = %q, want %q", got, eventsMsgBlocked)
	}
	for _, tc := range []struct {
		who  string
		user domain.User
		id   string
	}{
		{"member on Alfa", e.Member, ev.ID},
		{"outsider on Alfa", e.Outsider, ev.ID},
		{"outsider on Delta", e.Outsider, delta.ID},
		{"member of Delta", e.dora, delta.ID},
	} {
		if got := evfBody(t, tc.who, e.get(tc.user, "/eventos/"+tc.id+"/editar"), http.StatusForbidden); got != approvalsMsgNotAllowed {
			t.Errorf("%s: body = %q, want %q", tc.who, got, approvalsMsgNotAllowed)
		}
	}

	// An event that is not there answers like a path no route serves.
	want := serve(e.r, http.MethodGet, "/nao-existe-aqui", withCookie(e.Cookie[e.Owner.ID]))
	for _, id := range []string{evdNoID, "nao-e-um-id"} {
		evdWantUnregistered(t, "unknown id "+id, e.get(e.Owner, "/eventos/"+id+"/editar"), want)
	}
	rec = serve(e.r, http.MethodGet, "/eventos/"+ev.ID+"/editar")
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/entrar") {
		t.Errorf("anonymous: %d to %q, want 303 to the login", rec.Code, rec.Header().Get("Location"))
	}
}

func TestEvfUpdate(t *testing.T) {
	e := newEvfEnv(t)

	t.Run("a valid form saves, and a stale version is refused until the person looks again", func(t *testing.T) {
		ev := e.full(t)
		target := "/eventos/" + ev.ID + "/editar"
		f := evfEditForm("14/11/2026", ev.Version)
		f.Set("name", "Noite Verde II")
		id := evfRedirect(t, "owner", e.send(e.Owner, target, f), "evento-salvo")
		if id != ev.ID {
			t.Errorf("redirected to %s, want the event %s", id, ev.ID)
		}
		saved := e.stored(t, ev.ID)
		if saved.Name != "Noite Verde II" || saved.Venue != "Casa da Praia" || saved.Version != ev.Version+1 || saved.UpdatedBy != e.Owner.ID {
			t.Errorf("saved = %+v", saved.Event)
		}
		wantAll(t, "the notice", evdOK(t, "after the redirect", e.get(e.Owner, "/eventos/"+ev.ID+"?msg=evento-salvo")), "alterações salvas.")

		// Another person read the page before that save: their version is stale.
		stale := evfEditForm("14/11/2026", ev.Version)
		stale.Set("name", "Segunda Pessoa")
		rec := e.send(e.Owner, target, stale)
		body := evfBody(t, "stale", rec, http.StatusConflict)
		wantAll(t, "the alert", body, `role="alert"`, eventsMsgChanged)
		wantAll(t, "the name kept", evfInput(t, body, "name"), `value="Segunda Pessoa"`)
		wantAll(t, "the current version", evfInput(t, body, "version"), `type="hidden"`, `value="`+strconv.FormatInt(saved.Version, 10)+`"`)
		if attrs := tagAttrs(t, body, `<a ([^>]*\bhref="/eventos/`+ev.ID+`"[^>]*)>[^<]*(?:atual|evento)[^<]*</a>`); attrs == "" {
			t.Error("no link to the current event")
		}
		if got := e.stored(t, ev.ID).Name; got != "Noite Verde II" {
			t.Errorf("name = %q after a stale post, want it unchanged", got)
		}
		// Saving again, with the version the form now carries, is the person's choice.
		stale.Set("version", strconv.FormatInt(saved.Version, 10))
		evfRedirect(t, "second try", e.send(e.Owner, target, stale), "evento-salvo")
		if got := e.stored(t, ev.ID).Name; got != "Segunda Pessoa" {
			t.Errorf("name = %q, want the second person's", got)
		}
	})

	t.Run("an invalid form comes back with the values", func(t *testing.T) {
		ev := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID)
		f := evfEditForm("14/11/2026", ev.Version)
		f.Set("name", "")
		f.Set("state", "PE")
		body := evfBody(t, "invalid", e.send(e.Owner, "/eventos/"+ev.ID+"/editar", f), http.StatusUnprocessableEntity)
		wantAll(t, "the page", body, `id="erro-name">campo obrigatório</p>`, `Coletivo Alfa`)
		wantAll(t, "the version kept", evfInput(t, body, "version"), `value="1"`)
		wantAll(t, "the time kept", evfInput(t, body, "time"), `value="22:00"`)
		if cities, _ := evfOptions(evfSelect(t, body, "city")); !slices.Contains(cities, "Recife") {
			t.Error("the cities are not those of the submitted state")
		}
		evfNoField(t, "the edit page", body, "collective_id")
		if got := e.stored(t, ev.ID).Name; got != "Noite Verde" {
			t.Errorf("name = %q, want it unchanged", got)
		}
		// A version nobody can see is still told to the person.
		f = evfEditForm("14/11/2026", ev.Version)
		f.Set("version", "abc")
		wantAll(t, "a bad version", evfBody(t, "bad version", e.send(e.Owner, "/eventos/"+ev.ID+"/editar", f), http.StatusUnprocessableEntity), `id="erro-version"`)
	})

	t.Run("a past event is saved without changing its date", func(t *testing.T) {
		past := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID, func(ev *domain.Event) {
			ev.Name = "Noite Antiga"
			ev.StartsAt = time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC) // 04/11/2026 22:00 in Fortaleza
		})
		target := "/eventos/" + past.ID + "/editar"
		body := evdOK(t, "past event page", e.get(e.Owner, target))
		wantAll(t, "the date", evfInput(t, body, "date"), `value="04/11/2026"`)
		f := evfEditForm("04/11/2026", past.Version)
		evfRedirect(t, "same past date", e.send(e.Owner, target, f), "evento-salvo")
		if got := e.stored(t, past.ID); got.Name != "Noite Verde" || !got.StartsAt.Truncate(time.Millisecond).Equal(past.StartsAt.Truncate(time.Millisecond)) {
			t.Errorf("saved = %+v", got.Event)
		}
		// Another day in the past is still refused.
		f = evfEditForm("03/11/2026", e.stored(t, past.ID).Version)
		wantAll(t, "another past date", evfBody(t, "other past date", e.send(e.Owner, target, f), http.StatusUnprocessableEntity), `a data não pode ser anterior a hoje`)
	})

	t.Run("the collective never changes", func(t *testing.T) {
		ev := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID)
		f := evfEditForm("14/11/2026", ev.Version)
		f.Set("collective_id", e.Delta.ID)
		evfRedirect(t, "owner", e.send(e.Owner, "/eventos/"+ev.ID+"/editar", f), "evento-salvo")
		if got := e.stored(t, ev.ID).CollectiveID; got != e.Alfa.ID {
			t.Errorf("collective = %s, want it to stay Alfa", got)
		}
	})

	t.Run("the permission table", func(t *testing.T) {
		alfa := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID)
		delta := evEvent(t, e.Store, e.Delta.ID, e.Owner.ID)
		f := func(ev domain.Event) url.Values { return evfEditForm("14/11/2026", ev.Version) }

		// A Membro, a stranger and a Membro of a blocked collective are refused, and nothing is saved.
		for _, tc := range []struct {
			who  string
			user domain.User
			ev   domain.Event
		}{
			{"member on Alfa", e.Member, alfa}, {"outsider on Alfa", e.Outsider, alfa},
			{"member of Delta", e.dora, delta}, {"outsider on Delta", e.Outsider, delta},
		} {
			got := evfBody(t, tc.who, e.send(tc.user, "/eventos/"+tc.ev.ID+"/editar", f(tc.ev)), http.StatusForbidden)
			if got != approvalsMsgNotAllowed {
				t.Errorf("%s: body = %q, want %q", tc.who, got, approvalsMsgNotAllowed)
			}
			if e.stored(t, tc.ev.ID).Name != "Noite Verde" && tc.ev.ID == alfa.ID {
				t.Errorf("%s: the event was saved", tc.who)
			}
		}
		// An Administrador of a blocked collective gets the form back with the reason.
		body := evfBody(t, "owner on Delta", e.send(e.Owner, "/eventos/"+delta.ID+"/editar", f(delta)), http.StatusConflict)
		wantAll(t, "the alert", body, `role="alert"`, eventsMsgBlocked)
		wantAll(t, "the values", evfInput(t, body, "name"), `value="Noite Verde"`)
		// The admin edits it anyway.
		g := f(delta)
		g.Set("name", "Pelo Admin")
		evfRedirect(t, "admin on Delta", e.send(e.Admin, "/eventos/"+delta.ID+"/editar", g), "evento-salvo")
		if got := e.stored(t, delta.ID).Name; got != "Pelo Admin" {
			t.Errorf("name = %q, want the admin's edit", got)
		}
	})

	t.Run("an event that is not there, and a post without an Origin", func(t *testing.T) {
		want := serve(e.r, http.MethodGet, "/nao-existe-aqui", withCookie(e.Cookie[e.Owner.ID]))
		rec := e.send(e.Owner, "/eventos/"+evdNoID+"/editar", evfEditForm("14/11/2026", 1))
		evdWantUnregistered(t, "unknown event", rec, want)

		ev := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID)
		rec = do(e.r, http.MethodPost, "/eventos/"+ev.ID+"/editar", strings.NewReader(evfEditForm("14/11/2026", ev.Version).Encode()),
			header("Content-Type", "application/x-www-form-urlencoded"), withCookie(e.Cookie[e.Owner.ID]))
		evfBody(t, "no Origin", rec, http.StatusForbidden)
		if got := e.stored(t, ev.ID).Version; got != ev.Version {
			t.Errorf("version = %d after a post without Origin, want %d", got, ev.Version)
		}
	})

	t.Run("ATUALIZAR LISTAS loads the lists and saves nothing, for whoever may edit", func(t *testing.T) {
		ev := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID)
		delta := evEvent(t, e.Store, e.Delta.ID, e.Owner.ID)
		f := evfEditForm("14/11/2026", ev.Version)
		f.Set("name", "Mudado")
		f.Set("state", "PE")
		f.Set("acao", "atualizar")
		body := evfBody(t, "owner", e.send(e.Owner, "/eventos/"+ev.ID+"/editar", f), http.StatusOK)
		if cities, _ := evfOptions(evfSelect(t, body, "city")); !slices.Contains(cities, "Recife") || slices.Contains(cities, "Fortaleza") {
			t.Errorf("cities = %d options, want those of PE", len(cities))
		}
		wantAll(t, "the values", evfInput(t, body, "name"), `value="Mudado"`)
		wantAll(t, "the version", evfInput(t, body, "version"), `value="1"`)
		wantAll(t, "the collective", body, `Coletivo Alfa`)
		if strings.Contains(body, `class="field-error"`) {
			t.Error("the refresh shows errors")
		}
		if got := e.stored(t, ev.ID); got.Name != "Noite Verde" || got.Version != ev.Version {
			t.Errorf("the refresh saved: %+v", got.Event)
		}
		// The refresh is no way to see the form of an event the person may not edit.
		for _, tc := range []struct {
			who  string
			user domain.User
			ev   domain.Event
			want int
		}{
			{"member", e.Member, ev, http.StatusForbidden},
			{"outsider", e.Outsider, ev, http.StatusForbidden},
			{"owner on Delta", e.Owner, delta, http.StatusConflict},
		} {
			rec := e.send(tc.user, "/eventos/"+tc.ev.ID+"/editar", f)
			if got := evfBody(t, tc.who, rec, tc.want); strings.Contains(got, `name="name"`) || strings.Contains(got, "Coletivo") {
				t.Errorf("%s: the refresh rendered the form: %.200q", tc.who, got)
			}
		}
	})
}

// evfAllRules fails for every class of the form's markup that components.css
// has no rule for.
func evfRules(t *testing.T, what, body string) {
	t.Helper()
	css, err := fs.ReadFile(webfs.Static, "static/css/components.css")
	if err != nil {
		t.Fatalf("read components.css: %v", err)
	}
	region := regexp.MustCompile(`(?s)<form\b[^>]*\baction="/eventos/[^"]*"[^>]*>.*?</form>`).FindString(body)
	if region == "" {
		t.Fatalf("%s: no event form in the page", what)
	}
	seen := map[string]bool{}
	for _, m := range evfClassRe.FindAllStringSubmatch(region, -1) {
		for _, class := range strings.Fields(m[1]) {
			if seen[class] {
				continue
			}
			seen[class] = true
			if !regexp.MustCompile(`\.` + regexp.QuoteMeta(class) + `\b`).Match(css) {
				t.Errorf("%s: components.css has no rule for .%s", what, class)
			}
		}
	}
}

// evfLinks checks what a screen reader and the browser rely on: every id is
// unique, every label and every aria-describedby points at an element that
// exists, an invalid control is described, and a control whose label says it is
// required carries the attribute.
func evfLinks(t *testing.T, what, body string) {
	t.Helper()
	ids := map[string]int{}
	for _, m := range evfIDRe.FindAllStringSubmatch(body, -1) {
		ids[m[1]]++
	}
	for id, n := range ids {
		if n > 1 {
			t.Errorf("%s: id %q appears %d times", what, id, n)
		}
	}
	for _, m := range evfLabelRe.FindAllStringSubmatch(body, -1) {
		attrs := m[1]
		f := evfForRe.FindStringSubmatch(attrs)
		if f == nil {
			continue // a label that wraps its control
		}
		if ids[f[1]] == 0 {
			t.Errorf("%s: <label for=%q> points at no element", what, f[1])
		}
		if strings.Contains(attrs, "label--required") {
			ctl := regexp.MustCompile(`<(?:input|select|textarea)\b([^>]*\sid="` + regexp.QuoteMeta(f[1]) + `"[^>]*)>`).FindStringSubmatch(body)
			if ctl == nil || !slices.Contains(strings.Fields(ctl[1]), "required") {
				t.Errorf("%s: the control %q of a required label lacks the required attribute", what, f[1])
			}
		}
	}
	for _, m := range evfDescRe.FindAllStringSubmatch(body, -1) {
		for _, id := range strings.Fields(m[1]) {
			if ids[id] == 0 {
				t.Errorf("%s: aria-describedby names %q, which does not exist", what, id)
			}
		}
	}
	for _, m := range evfControl.FindAllStringSubmatch(body, -1) {
		if strings.Contains(m[1], `aria-invalid="true"`) && !strings.Contains(m[1], "aria-describedby=") {
			t.Errorf("%s: an invalid control is not described: <%.120s>", what, m[1])
		}
	}
}

func TestEvfMarkup(t *testing.T) {
	e := newEvfEnv(t)
	ev := e.full(t)
	empty := url.Values{"collective_id": {e.Alfa.ID}}
	stale := evfEditForm("14/11/2026", ev.Version)
	stale.Set("version", "99")
	stale.Set("name", "")
	for _, tc := range []struct {
		name string
		rec  *httptest.ResponseRecorder
		want int
	}{
		{"new", e.get(e.Owner, "/eventos/novo"), http.StatusOK},
		{"new with every error", e.send(e.Owner, "/eventos/novo", empty), http.StatusUnprocessableEntity},
		{"edit", e.get(e.Owner, "/eventos/"+ev.ID+"/editar"), http.StatusOK},
		{"edit with errors", e.send(e.Owner, "/eventos/"+ev.ID+"/editar", stale), http.StatusUnprocessableEntity},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := evfBody(t, tc.name, tc.rec, tc.want)
			if v := cspViolations(body); len(v) > 0 {
				t.Errorf("the page breaks the CSP: %v", v)
			}
			evfLinks(t, tc.name, body)
			evfRules(t, tc.name, body)
		})
	}

	// With every field wrong, each one says so under itself.
	body := e.send(e.Owner, "/eventos/novo", empty).Body.String()
	for _, field := range []string{"name", "type", "date", "time", "duration", "price", "style", "state", "city"} {
		wantAll(t, field, body, `id="erro-`+field+`"`)
	}
	wantAll(t, "the duration", evfInput(t, body, "duration_hours"), `erro-duration`)
	wantAll(t, "the duration", evfInput(t, body, "duration_minutes"), `erro-duration`)
	wantAll(t, "the pair", body, `<legend`)
}
