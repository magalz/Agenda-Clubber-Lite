package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The day list (Analisar Data): GET /eventos/dia is a page, and with HX-Request
// only the list, which the event form's side panel loads. The tests read the
// markup the way a person without JavaScript uses it.

const (
	dayInvalid  = "escolha uma data válida (dd/mm/aaaa)"
	dayEmpty    = "Nenhum evento nesta data."
	daySameCity = "mesma cidade"
)

// --- helpers (prefix day) ---

// dayEnv is the shared cast with the day list's own way to name an instant.
// Everyone signed in sees the same list, so the requests go out as the Outsider
// unless a test says otherwise.
type dayEnv struct{ evWorld }

func newDayEnv(t *testing.T) dayEnv {
	t.Helper()
	return dayEnv{newEvWorld(t)}
}

// at is the instant of a time on a day of November 2026, in the app's zone.
func (e dayEnv) at(day, hour, minute int) time.Time {
	return time.Date(2026, time.November, day, hour, minute, 0, 0, e.App.Cfg.Location).UTC()
}

var (
	dayRowRE   = regexp.MustCompile(`(?s)<li class="item-list__item">.*?</li>`)
	dayTitleRE = regexp.MustCompile(`<p class="item-list__title"><a [^>]*>([^<]*)</a></p>`)
	dayLinkRE  = regexp.MustCompile(`<a ([^>]*)>`)
)

// dayRows are the rows of the list in s, in order.
func dayRows(s string) []string { return dayRowRE.FindAllString(s, -1) }

// dayNames are the event names of the rows in s, in order.
func dayNames(s string) []string {
	var out []string
	for _, row := range dayRows(s) {
		out = append(out, html.UnescapeString(dayTitleRE.FindStringSubmatch(row)[1]))
	}
	return out
}

// --- the list ---

func TestDayListsTheEventsOfTheDay(t *testing.T) {
	e := newDayEnv(t)
	night := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID) // Saturday 14/11 at 22:00 for six hours, a Festa in Fortaleza/CE
	// Not on the 14th: its first minute is in, the last of the 13th and the first of the 15th are out.
	first := e.event(t, "Madrugada", e.at(14, 0, 0), func(ev *domain.Event) { ev.Type = domain.EventTypeWorkshop; ev.DurationMin = 60 })
	e.event(t, "Antes", e.at(13, 23, 59))
	e.event(t, "Depois", e.at(15, 0, 0))

	for _, q := range []string{"2026-11-14", "14%2F11%2F2026"} {
		t.Run(q, func(t *testing.T) {
			body := e.body(t, "/eventos/dia?data="+q)

			rows := dayRows(body)
			if got, want := dayNames(body), []string{first.Name, night.Name}; !slices.Equal(got, want) {
				t.Fatalf("events = %q, want %q", got, want)
			}
			// The name is a real link to the event.
			if got := evAttrURL(t, dayLinkRE.FindString(rows[1]), "href"); got.Path != "/eventos/"+night.ID || got.RawQuery != "" {
				t.Errorf("the name links to %q, want /eventos/%s", got, night.ID)
			}
			wantAll(t, "the Festa", rows[1], ">Festa<", "techno", "Coletivo Alfa", "Fortaleza/CE", "22:00, até 04:00 (&#43;1)")
			wantAll(t, "the Workshop", rows[0], ">Workshop<", "00:00, até 01:00")
			if strings.Contains(rows[0]+rows[1], daySameCity) {
				t.Errorf("a list with no highlight says %q: %s", daySameCity, body)
			}
			for _, name := range []string{"Antes", "Depois"} {
				if strings.Contains(body, name) {
					t.Errorf("the page lists %q, which is not on the 14th", name)
				}
			}

			// The page: the long date in the heading and the date in the title.
			wantAll(t, "the page", body, "<title>Eventos de 14/11/2026 · AGENDA CLUBBER</title>", "sábado, 14/11/2026", `id="day-list"`)
			inOrder(t, body, "<h1", "sábado, 14/11/2026", `id="day-list"`)
			if !strings.Contains(body, `aria-current="page">Calendário`) {
				t.Errorf("the sidebar does not mark the calendar: %s", body)
			}
		})
	}
}

func TestDayOrdersByStartTime(t *testing.T) {
	e := newDayEnv(t)
	for _, h := range []int{23, 10, 18, 2} { // out of order
		e.event(t, fmt.Sprintf("Ev %02d", h), e.at(14, h, 0))
	}
	want := []string{"Ev 02", "Ev 10", "Ev 18", "Ev 23"}
	if got := dayNames(e.body(t, "/eventos/dia?data=2026-11-14")); !slices.Equal(got, want) {
		t.Errorf("events = %q, want %q", got, want)
	}
}

func TestDayFilterRestrictsToThePlace(t *testing.T) {
	e := newDayEnv(t)
	e.event(t, "Sobral Tarde", e.at(14, 15, 0), calPlace("CE", "Sobral"))
	e.event(t, "Fortaleza Noite", e.at(14, 20, 0))
	e.event(t, "Recife Noite", e.at(14, 21, 0), calPlace("PE", "Recife"))
	all := []string{"Sobral Tarde", "Fortaleza Noite", "Recife Noite"}

	tests := []struct {
		name, query string
		want        []string
	}{
		{"none", "", all},
		{"a state", "&uf=PE", []string{"Recife Noite"}},
		{"a state with several cities", "&uf=CE", []string{"Sobral Tarde", "Fortaleza Noite"}},
		{"a state and a city", "&uf=CE&cidade=Fortaleza", []string{"Fortaleza Noite"}},
		{"a state and a city with no event", "&uf=CE&cidade=Juazeiro%20do%20Norte", nil},
		{"a city of another state is no city", "&uf=PE&cidade=Fortaleza", []string{"Recife Noite"}},
		{"a city with no state", "&cidade=Recife", all},
		{"a state that does not exist", "&uf=XX", all},
		{"a script", "&uf=%3Cscript%3E&cidade=%3Cb%3E", all},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dayNames(e.body(t, "/eventos/dia?data=2026-11-14"+tt.query)); !slices.Equal(got, tt.want) {
				t.Errorf("events = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDayHighlightsTheCityOfTheForm(t *testing.T) {
	e := newDayEnv(t)
	e.event(t, "Recife Manhã", e.at(14, 10, 0), calPlace("PE", "Recife"))
	e.event(t, "Sobral Meio-dia", e.at(14, 12, 0), calPlace("CE", "Sobral"))
	e.event(t, "Fortaleza Noite", e.at(14, 22, 0))
	e.event(t, "Fortaleza Tarde", e.at(14, 18, 0))
	byTime := []string{"Recife Manhã", "Sobral Meio-dia", "Fortaleza Tarde", "Fortaleza Noite"}

	// The events held in the city come first, each with the badge, and the others follow
	// without it; both groups stay in order of start time.
	body := e.body(t, "/eventos/dia?data=2026-11-14&destaque_uf=CE&destaque_cidade=Fortaleza")
	if got, want := dayNames(body), []string{"Fortaleza Tarde", "Fortaleza Noite", "Recife Manhã", "Sobral Meio-dia"}; !slices.Equal(got, want) {
		t.Fatalf("events = %q, want %q", got, want)
	}
	for i, row := range dayRows(body) {
		if got, want := strings.Contains(row, daySameCity), i < 2; got != want {
			t.Errorf("row %d (%s) has %q = %v, want %v", i, dayNames(body)[i], daySameCity, got, want)
		}
	}

	// The highlight does not restrict: the calendar's filter does, and the two combine.
	body = e.body(t, "/eventos/dia?data=2026-11-14&uf=PE&destaque_uf=CE&destaque_cidade=Fortaleza")
	if got, want := dayNames(body), []string{"Recife Manhã"}; !slices.Equal(got, want) || strings.Contains(body, daySameCity) {
		t.Errorf("filter PE with the highlight Fortaleza: %q (badge %v), want %q with no badge", got, strings.Contains(body, daySameCity), want)
	}

	// Only a state and a city of that state make a highlight.
	for _, q := range []string{
		"&destaque_uf=CE",
		"&destaque_cidade=Fortaleza",
		"&destaque_uf=PE&destaque_cidade=Fortaleza",
		"&destaque_uf=XX&destaque_cidade=Fortaleza",
		"&destaque_uf=CE&destaque_cidade=%3Cb%3E",
		"&destaque_uf=&destaque_cidade=",
		"&uf=CE&cidade=Fortaleza", // the calendar's filter is not a highlight
	} {
		body := e.body(t, "/eventos/dia?data=2026-11-14"+q)
		got := dayNames(body)
		want := byTime
		if strings.Contains(q, "&uf=CE&cidade=Fortaleza") {
			want = []string{"Fortaleza Tarde", "Fortaleza Noite"}
		}
		if !slices.Equal(got, want) || strings.Contains(body, daySameCity) {
			t.Errorf("%s: events %q (badge %v), want %q with no badge", q, got, strings.Contains(body, daySameCity), want)
		}
	}
}

// --- the answers ---

func TestDayRefusesAMissingOrInvalidDate(t *testing.T) {
	e := newDayEnv(t)
	e.event(t, "Noite Verde", e.at(14, 22, 0))

	for _, q := range []string{
		"",
		"?data=",
		"?data=%20",
		"?data=amanha",
		"?data=2026-13-40",
		"?data=2026-11-31",
		"?data=31%2F02%2F2026",
		"?data=14-11-2026",
		"?data=%3Cscript%3Ealert(1)%3C%2Fscript%3E",
		"?destaque_uf=CE&destaque_cidade=Fortaleza", // the other parameters do not stand in for it
	} {
		for _, htmx := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s hx=%v", q, htmx), func(t *testing.T) {
				var mod []func(*http.Request)
				if htmx {
					mod = append(mod, hx)
				}
				rec := e.get(e.Outsider, "/eventos/dia"+q, mod...)
				if rec.Code != http.StatusUnprocessableEntity {
					t.Fatalf("status = %d %q, want 422", rec.Code, rec.Body)
				}
				body := rec.Body.String()
				wantAll(t, "the message", body, dayInvalid)
				if strings.Contains(body, "Noite Verde") || strings.Contains(body, dayEmpty) || strings.Contains(body, "<ul") {
					t.Errorf("an invalid date shows a list: %s", body)
				}
				if strings.Contains(body, "alert(1)") {
					t.Errorf("the date asked for is echoed: %s", body)
				}
				if got := rec.Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("Cache-Control = %q, want no-store", got)
				}
				if got := strings.Contains(body, "<html"); got == htmx {
					t.Errorf("<html> in the answer = %v, want %v", got, !htmx)
				}
				if v := cspViolations(body); len(v) > 0 {
					t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
				}
			})
		}
	}
}

// A date far out of range still answers: the range reaches the store with dates it
// must accept.
func TestDayAnswersAnyRealDate(t *testing.T) {
	e := newDayEnv(t)
	for _, d := range []string{"0001-01-01", "9999-12-31", "29/02/2028"} {
		if body := e.body(t, "/eventos/dia?data="+url.QueryEscape(d)); !strings.Contains(body, dayEmpty) {
			t.Errorf("data=%s: no empty text in %s", d, body)
		}
	}
}

func TestDayHTMXAnswersOnlyTheList(t *testing.T) {
	e := newDayEnv(t)
	e.event(t, "Noite Verde", e.at(14, 22, 0))
	const target = "/eventos/dia?data=2026-11-14&destaque_uf=CE&destaque_cidade=Fortaleza"

	rec := e.get(e.Outsider, target, hx)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s with HX-Request: %d %q", target, rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Content-Type"); got != htmlContentType {
		t.Errorf("Content-Type = %q, want %q", got, htmlContentType)
	}
	partial := rec.Body.String()
	for _, layout := range []string{"<html", "<body", "<header", "<nav", "<h1", "<title", "<footer"} {
		if strings.Contains(partial, layout) {
			t.Errorf("the partial has %s: %s", layout, partial)
		}
	}
	wantAll(t, "the partial", partial, `id="day-list"`, "Noite Verde", daySameCity)

	// The page holds the very same list, and the partial has no way back to the calendar
	// (the page does).
	page := e.body(t, target)
	if !strings.Contains(page, strings.TrimSpace(partial)) {
		t.Errorf("the page does not hold the partial.\npartial: %s\npage: %s", partial, page)
	}
	if strings.Contains(partial, "[←]") || !strings.Contains(page, "[←]") {
		t.Errorf("the back link: partial %v, page %v; want only the page", strings.Contains(partial, "[←]"), strings.Contains(page, "[←]"))
	}
}

func TestDayAnEmptyDayShowsTheEmptyText(t *testing.T) {
	e := newDayEnv(t)
	e.event(t, "Noite Verde", e.at(14, 22, 0))
	e.event(t, "Recife", e.at(16, 22, 0), calPlace("PE", "Recife"))

	for _, tc := range []struct{ name, target string }{
		{"a day with no event", "/eventos/dia?data=2026-11-15"},
		{"a day with events elsewhere", "/eventos/dia?data=2026-11-16&uf=CE"},
		{"a day with a highlight", "/eventos/dia?data=2026-11-15&destaque_uf=CE&destaque_cidade=Fortaleza"},
	} {
		for _, htmx := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s hx=%v", tc.name, htmx), func(t *testing.T) {
				var mod []func(*http.Request)
				if htmx {
					mod = append(mod, hx)
				}
				body := e.body(t, tc.target, mod...)
				wantAll(t, "the empty day", body, dayEmpty, `id="day-list"`)
				if strings.Contains(body, "<ul") || strings.Contains(body, dayInvalid) || len(dayRows(body)) != 0 {
					t.Errorf("an empty day shows a list or an error: %s", body)
				}
			})
		}
	}
	// The page still says which day it is.
	wantAll(t, "the page", e.body(t, "/eventos/dia?data=2026-11-15"), "domingo, 15/11/2026", "Eventos de 15/11/2026")
}

func TestDayBackLinkKeepsTheMonthAndTheFilter(t *testing.T) {
	e := newDayEnv(t)
	tests := []struct {
		name, query, uf, city string
	}{
		{"no filter", "?data=2026-12-02", "", ""},
		{"a day written dd/mm/aaaa", "?data=02%2F12%2F2026", "", ""},
		{"a state", "?data=2026-12-02&uf=PE", "PE", ""},
		{"a state and a city", "?data=2026-12-02&uf=CE&cidade=Fortaleza", "CE", "Fortaleza"},
		{"a city that is not the state's", "?data=2026-12-02&uf=CE&cidade=Recife", "CE", ""},
		{"a state that does not exist", "?data=2026-12-02&uf=XX&cidade=Fortaleza", "", ""},
		{"the highlight is not the filter", "?data=2026-12-02&destaque_uf=CE&destaque_cidade=Fortaleza", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := e.body(t, "/eventos/dia"+tt.query)
			u := evHref(t, body, "[←] calendário")
			want := url.Values{"mes": {"2026-12"}}
			if tt.uf != "" {
				want.Set("uf", tt.uf)
			}
			if tt.city != "" {
				want.Set("cidade", tt.city)
			}
			if u.Path != "/" || u.Query().Encode() != want.Encode() {
				t.Errorf("the back link is %q, want / with %s", u, want.Encode())
			}
		})
	}
	// An invalid date has no month of its own: the way back is the current one,
	// with the filter kept. Compute the month the way the calendar route does.
	currentMonth := e.App.Now().In(e.App.Cfg.Location).Format("2006-01")
	invalid := []struct {
		name, query, uf, city string
	}{
		{"no filter", "?data=ontem", "", ""},
		{"a state", "?data=ontem&uf=PE", "PE", ""},
		{"a state and a city", "?data=ontem&uf=CE&cidade=Fortaleza", "CE", "Fortaleza"},
		{"a city that is not the state's", "?data=ontem&uf=CE&cidade=Recife", "CE", ""},
		{"a state that does not exist", "?data=ontem&uf=XX&cidade=Fortaleza", "", ""},
		{"the highlight is not the filter", "?data=ontem&destaque_uf=CE&destaque_cidade=Fortaleza", "", ""},
	}
	for _, tt := range invalid {
		t.Run("invalid/"+tt.name, func(t *testing.T) {
			u := evHref(t, e.get(e.Outsider, "/eventos/dia"+tt.query).Body.String(), "[←] calendário")
			want := url.Values{"mes": {currentMonth}}
			if tt.uf != "" {
				want.Set("uf", tt.uf)
			}
			if tt.city != "" {
				want.Set("cidade", tt.city)
			}
			if u.Path != "/" || u.Query().Encode() != want.Encode() {
				t.Errorf("the back link of an invalid date is %q, want / with %s", u, want.Encode())
			}
		})
	}
}

func TestDayIsWhereTheCalendarLinks(t *testing.T) {
	e := newDayEnv(t)
	for _, h := range []int{10, 12, 14, 16, 18} {
		e.event(t, fmt.Sprintf("Ev %02d", h), e.at(21, h, 0), calPlace("PE", "Recife"))
	}
	e.event(t, "Fortaleza", e.at(21, 20, 0))

	// +2 is the link of the cell, with the filter kept: the list shows what the grid counted.
	cal := e.body(t, "/?mes=2026-11&uf=PE")
	u := evHref(t, cal, "+2")
	if u.Path != "/eventos/dia" {
		t.Fatalf("+2 links to %q, want /eventos/dia", u)
	}
	body := e.body(t, u.RequestURI())
	want := []string{"Ev 10", "Ev 12", "Ev 14", "Ev 16", "Ev 18"}
	if got := dayNames(body); !slices.Equal(got, want) {
		t.Errorf("the list behind +2 = %q, want %q", got, want)
	}
}

func TestDayIsForEverySignedInUser(t *testing.T) {
	e := newDayEnv(t)
	e.event(t, "Noite Verde", e.at(14, 22, 0))
	for name, u := range map[string]domain.User{"admin": e.Admin, "owner": e.Owner, "member": e.Member, "outsider": e.Outsider} {
		rec := e.get(u, "/eventos/dia?data=2026-11-14")
		if rec.Code != http.StatusOK || !slices.Equal(dayNames(rec.Body.String()), []string{"Noite Verde"}) {
			t.Errorf("%s: %d, events %q; want 200 and Noite Verde", name, rec.Code, dayNames(rec.Body.String()))
		}
	}

	rec := serve(e.r, http.MethodGet, "/eventos/dia?data=2026-11-14")
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, "/entrar") {
		t.Errorf("anonymous: %d to %q, want 303 to /entrar", rec.Code, loc)
	}
}

// --- what the people wrote, and the limit ---

func TestDayEscapesWhatPeopleWrote(t *testing.T) {
	e := newDayEnv(t)
	other := evCollective(t, e.Store, `<i>Beta</i> & "Cia"`, e.Owner.ID)
	evEvent(t, e.Store, other.ID, e.Owner.ID, func(ev *domain.Event) {
		ev.Name = `<script>alert(1)</script> & "B"`
		ev.StartsAt = e.at(14, 20, 0)
	})

	for _, htmx := range []bool{false, true} {
		var mod []func(*http.Request)
		if htmx {
			mod = append(mod, hx)
		}
		body := e.body(t, "/eventos/dia?data=2026-11-14", mod...)
		if got, want := dayNames(body), []string{`<script>alert(1)</script> & "B"`}; !slices.Equal(got, want) {
			t.Errorf("hx=%v: events = %q, want %q", htmx, got, want)
		}
		wantAll(t, "the row", body, "&lt;script&gt;alert(1)&lt;/script&gt; &amp; &#34;B&#34;", "&lt;i&gt;Beta&lt;/i&gt; &amp; &#34;Cia&#34;")
		for _, raw := range []string{"<script>alert", "<i>Beta"} {
			if strings.Contains(body, raw) {
				t.Errorf("hx=%v: %q is not escaped in:\n%s", htmx, raw, body)
			}
		}
	}
}

func TestDayIsCSPClean(t *testing.T) {
	e := newDayEnv(t)
	e.event(t, "Noite Verde", e.at(14, 22, 0))
	e.event(t, "Recife", e.at(14, 12, 0), calPlace("PE", "Recife"))
	for _, target := range []string{
		"/eventos/dia?data=2026-11-14&destaque_uf=CE&destaque_cidade=Fortaleza",
		"/eventos/dia?data=2026-11-15",
		"/eventos/dia",
	} {
		for _, htmx := range []bool{false, true} {
			var mod []func(*http.Request)
			if htmx {
				mod = append(mod, hx)
			}
			body := e.get(e.Outsider, target, mod...).Body.String()
			if v := cspViolations(body); len(v) > 0 {
				t.Errorf("%s hx=%v: %s in:\n%s", target, htmx, strings.Join(v, "; "), body)
			}
		}
	}
}

func TestDayTruncatedNote(t *testing.T) {
	e := newDayEnv(t)
	const note = "mostrando só os primeiros 500 eventos deste dia"
	start := e.at(14, 0, 0)
	for i := range domain.ListLimitMax - 1 {
		e.event(t, fmt.Sprintf("e%03d", i), start.Add(time.Duration(i)*time.Minute))
	}
	const target = "/eventos/dia?data=2026-11-14"

	// One event under the limit may show every event of the day, so it has no note.
	if body := e.body(t, target); strings.Contains(body, "mostrando só") || len(dayRows(body)) != domain.ListLimitMax-1 {
		t.Fatalf("%d events under the limit: %d rows, note %v; want all of them and no note", domain.ListLimitMax-1, len(dayRows(body)), strings.Contains(body, "mostrando só"))
	}
	e.event(t, "e499", start.Add(time.Duration(domain.ListLimitMax-1)*time.Minute))
	for _, htmx := range []bool{false, true} {
		var mod []func(*http.Request)
		if htmx {
			mod = append(mod, hx)
		}
		body := e.body(t, target, mod...)
		m := regexp.MustCompile(`<p ([^>]*)>` + note + `</p>`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("hx=%v: no note %q in the list of %d rows", htmx, note, len(dayRows(body)))
		}
		wantAll(t, "the note", m[1], `role="status"`)
		if n := len(dayRows(body)); n != domain.ListLimitMax {
			t.Errorf("hx=%v: %d rows, want %d", htmx, n, domain.ListLimitMax)
		}
	}
}
