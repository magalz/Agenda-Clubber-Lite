package web

import (
	"bytes"
	"context"
	"errors"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/events"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

// The month calendar: GET / (the page) and GET /calendario (only the month, for
// HTMX). The tests read the markup the way a person without JavaScript uses it:
// the links and the cells.

// --- helpers (prefix cal) ---

// calEnv is the shared cast and a router. Everyone signed in sees the same
// calendar, so the requests go out as the Outsider, who owns nothing.
type calEnv struct {
	evWorld
	r http.Handler
}

func newCalEnv(t *testing.T) calEnv {
	t.Helper()
	w := newEvWorld(t)
	return calEnv{evWorld: w, r: w.App.Router()}
}

// at is the instant of a day of 2026 at the hour, in the app's zone.
func (e calEnv) at(month time.Month, day, hour int) time.Time {
	return time.Date(2026, month, day, hour, 0, 0, 0, e.App.Cfg.Location).UTC()
}

// event stores an event of Alfa called name that starts at the instant.
func (e calEnv) event(t *testing.T, name string, starts time.Time, mutate ...func(*domain.Event)) domain.Event {
	t.Helper()
	set := func(ev *domain.Event) { ev.Name, ev.StartsAt = name, starts }
	return evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID, append([]func(*domain.Event){set}, mutate...)...)
}

// calPlace puts the event in a place.
func calPlace(state, city string) func(*domain.Event) {
	return func(ev *domain.Event) { ev.State, ev.City = state, city }
}

func (e calEnv) get(target string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	return serve(e.r, http.MethodGet, target, append([]func(*http.Request){withCookie(e.Cookie[e.Outsider.ID])}, mod...)...)
}

// body answers the GET and fails unless it is a 200.
func (e calEnv) body(t *testing.T, target string, mod ...func(*http.Request)) string {
	t.Helper()
	rec := e.get(target, mod...)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %q; want 200", target, rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// calCell is the <td> of a day, its opening tag included, or a failed test.
func calCell(t *testing.T, body, day string) string {
	t.Helper()
	m := regexp.MustCompile(`(?s)<td [^>]*data-day="` + regexp.QuoteMeta(day) + `"[^>]*>.*?</td>`).FindString(body)
	if m == "" {
		t.Fatalf("no cell for %s in:\n%s", day, body)
	}
	return m
}

// calOpenTag is the opening tag of a cell.
func calOpenTag(cell string) string { return cell[:strings.Index(cell, ">")+1] }

// calNames are the names of the chips in a cell (or in a page), in order.
func calNames(s string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<span class="calendar__chip-name">([^<]*)</span>`).FindAllStringSubmatch(s, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

// calChips are the opening tags of the chips (the links to an event) in s.
func calChips(s string) []string {
	return regexp.MustCompile(`<a [^>]*class="calendar__chip"[^>]*>`).FindAllString(s, -1)
}

// calHref is the href of the first link in s whose text starts with prefix,
// read the way a browser does: unescaped, then parsed.
func calHref(t *testing.T, s, prefix string) *url.URL {
	t.Helper()
	m := regexp.MustCompile(`<a ([^>]*)>\s*` + regexp.QuoteMeta(prefix)).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no link whose text starts with %q in:\n%s", prefix, s)
	}
	return calAttrURL(t, m[1], "href")
}

// calAttrURL reads an attribute of a tag as a URL.
func calAttrURL(t *testing.T, tag, name string) *url.URL {
	t.Helper()
	m := regexp.MustCompile(`(?:^|\s)` + name + `="([^"]*)"`).FindStringSubmatch(tag)
	if m == nil {
		t.Fatalf("no %s in %q", name, tag)
	}
	u, err := url.Parse(html.UnescapeString(m[1]))
	if err != nil {
		t.Fatalf("%s=%q: %v", name, m[1], err)
	}
	return u
}

// calMonthView is the grid of November 2026 (Sunday 01/11 to Saturday 05/12)
// with no events and 12/11 as today, as events.Month builds it, so a template
// can be rendered without a store.
func calMonthView() events.MonthView {
	loc := time.FixedZone("BRT", -3*60*60)
	first := time.Date(2026, time.November, 1, 0, 0, 0, 0, loc)
	today := time.Date(2026, time.November, 12, 0, 0, 0, 0, loc)
	v := events.MonthView{Month: first, Key: "2026-11", PrevKey: "2026-10", NextKey: "2026-12", TodayKey: "2026-11"}
	d := first
	for range 5 {
		var week events.Week
		for i := range week {
			week[i] = events.GridDay{Date: d, Key: d.Format("2006-01-02"), InMonth: d.Month() == time.November, Today: d.Equal(today), Past: d.Before(today)}
			d = d.AddDate(0, 0, 1)
		}
		v.Weeks = append(v.Weeks, week)
	}
	return v
}

// calHomeData is the Data the layout tests give app/home.
func calHomeData() calendarView { return newCalendarView(calMonthView()) }

// calRender renders app/home with a view.
func calRender(t *testing.T, app *App, v calendarView) string {
	t.Helper()
	rec := renderPage(t, app.Render, "app/home", PageData{Title: "Calendário", Nav: "calendario", Data: v})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// --- the month ---

func TestCalendarShowsTheMonth(t *testing.T) {
	e := newCalEnv(t)
	past := e.event(t, "Ensaio Aberto", e.at(time.November, 5, 20), func(ev *domain.Event) { ev.Type = domain.EventTypeWorkshop })
	night := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID) // Saturday 14/11 at 22:00, a Festa
	next := e.event(t, "Feira de Natal", e.at(time.December, 2, 9), func(ev *domain.Event) { ev.Type = domain.EventTypeFeira })

	rec := e.get("/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /: %d %q", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	body := rec.Body.String()

	// The bar: [‹] MÊS AAAA [›] and Hoje, each a link to a month.
	inOrder(t, body, "[‹]", ">NOVEMBRO 2026</h2>", "[›]")
	for prefix, want := range map[string]string{"[‹]": "2026-10", "[›]": "2026-12", "Hoje": "2026-11"} {
		u := calHref(t, body, prefix)
		if u.Path != "/" || u.Query().Get("mes") != want {
			t.Errorf("%s links to %q, want / with mes=%s", prefix, u, want)
		}
	}

	// A Sunday-to-Saturday grid of whole weeks: 01/11 to 05/12.
	head := regexp.MustCompile(`(?s)<thead>(.*?)</thead>`).FindString(body)
	if n := strings.Count(head, "<th "); n != 7 {
		t.Errorf("%d column headers, want 7", n)
	}
	inOrder(t, head, ">DOM<", ">SEG<", ">TER<", ">QUA<", ">QUI<", ">SEX<", ">SÁB<")
	days := regexp.MustCompile(`<td [^>]*data-day="([0-9-]+)"`).FindAllStringSubmatch(body, -1)
	if len(days) != 35 || days[0][1] != "2026-11-01" || days[34][1] != "2026-12-05" {
		t.Errorf("%d cells from %v to %v; want 35 from 2026-11-01 to 2026-12-05", len(days), days[0][1], days[len(days)-1][1])
	}
	if n := strings.Count(regexp.MustCompile(`(?s)<tbody>.*</tbody>`).FindString(body), "<tr>"); n != 5 {
		t.Errorf("%d week rows, want 5", n)
	}

	// A chip is a real link to the event, with its time, name and type.
	cell := calCell(t, body, "2026-11-14")
	chips := calChips(cell)
	if len(chips) != 1 {
		t.Fatalf("the 14th has %d chips, want 1: %s", len(chips), cell)
	}
	if got := calAttrURL(t, chips[0], "href"); got.Path != "/eventos/"+night.ID || got.RawQuery != "" {
		t.Errorf("the chip links to %q, want /eventos/%s", got, night.ID)
	}
	wantAll(t, "the chip", cell, ">22:00<", ">Noite Verde<", ">Festa<")
	if got := calNames(calCell(t, body, "2026-11-05")); !slices.Equal(got, []string{past.Name}) {
		t.Errorf("the 5th lists %q, want %q", got, past.Name)
	}
	wantAll(t, "the past event", calCell(t, body, "2026-11-05"), ">20:00<", ">Workshop<", "/eventos/"+past.ID)

	// The days of the next month the last week shows list their events too.
	out := calCell(t, body, "2026-12-02")
	if !strings.Contains(calOpenTag(out), "calendar__cell--out") || !slices.Equal(calNames(out), []string{next.Name}) {
		t.Errorf("2026-12-02 = %s; want an out-of-month cell that lists %q", out, next.Name)
	}

	// Today and the past days are marked; the other days are not.
	for _, tc := range []struct {
		day                string
		out, today, isPast bool
	}{
		{"2026-11-01", false, false, true},
		{"2026-11-11", false, false, true},
		{"2026-11-12", false, true, false},
		{"2026-11-13", false, false, false},
		{"2026-11-30", false, false, false},
		{"2026-12-01", true, false, false},
	} {
		open := calOpenTag(calCell(t, body, tc.day))
		for class, want := range map[string]bool{"calendar__cell--out": tc.out, "calendar__cell--today": tc.today, "calendar__cell--past": tc.isPast} {
			if strings.Contains(open, class) != want {
				t.Errorf("%s: %s = %v, want %v in %s", tc.day, class, !want, want, open)
			}
		}
		if got := strings.Contains(open, `aria-current="date"`); got != tc.today {
			t.Errorf("%s: aria-current=date = %v, want %v", tc.day, got, tc.today)
		}
	}
}

func TestCalendarMonthParameter(t *testing.T) {
	e := newCalEnv(t)
	e.event(t, "Ensaio Aberto", e.at(time.November, 5, 20))
	e.event(t, "Noite Verde", e.at(time.November, 14, 22))
	e.event(t, "Feira de Natal", e.at(time.December, 2, 9))
	all := []string{"Ensaio Aberto", "Noite Verde", "Feira de Natal"}

	tests := []struct {
		name, query, title, prev, next string
		have                           []string
	}{
		{"none", "", "NOVEMBRO 2026", "2026-10", "2026-12", all},
		{"the month asked for", "?mes=2026-12", "DEZEMBRO 2026", "2026-11", "2027-01", []string{"Feira de Natal"}},
		{"a month with no events", "?mes=2026-10", "OUTUBRO 2026", "2026-09", "2026-11", nil},
		{"a month of another year", "?mes=2025-01", "JANEIRO 2025", "2024-12", "2025-02", nil},
		{"the first month of a year", "?mes=2027-01", "JANEIRO 2027", "2026-12", "2027-02", nil},
		{"not a month", "?mes=novembro", "NOVEMBRO 2026", "2026-10", "2026-12", all},
		{"month 13", "?mes=2026-13", "NOVEMBRO 2026", "2026-10", "2026-12", all},
		{"month 0", "?mes=2026-00", "NOVEMBRO 2026", "2026-10", "2026-12", all},
		{"one digit", "?mes=2026-1", "NOVEMBRO 2026", "2026-10", "2026-12", all},
		{"a day", "?mes=2026-11-14", "NOVEMBRO 2026", "2026-10", "2026-12", all},
		{"empty", "?mes=", "NOVEMBRO 2026", "2026-10", "2026-12", all},
		{"a script", "?mes=%3Cscript%3E", "NOVEMBRO 2026", "2026-10", "2026-12", all},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := e.body(t, "/"+tt.query)
			wantAll(t, "title", body, ">"+tt.title+"</h2>")
			if got := calHref(t, body, "[‹]").Query().Get("mes"); got != tt.prev {
				t.Errorf("previous = %q, want %q", got, tt.prev)
			}
			if got := calHref(t, body, "[›]").Query().Get("mes"); got != tt.next {
				t.Errorf("next = %q, want %q", got, tt.next)
			}
			if got := calHref(t, body, "Hoje").Query().Get("mes"); got != "2026-11" {
				t.Errorf("Hoje = %q, want 2026-11", got)
			}
			names := calNames(body)
			slices.Sort(names)
			want := slices.Clone(tt.have)
			slices.Sort(want)
			if !slices.Equal(names, want) {
				t.Errorf("events = %q, want %q", names, want)
			}
		})
	}

	// A month far out of range still answers: the grid reaches the store with
	// dates it must accept.
	for _, q := range []string{"?mes=0001-01", "?mes=9999-12"} {
		if rec := e.get("/" + q); rec.Code != http.StatusOK {
			t.Errorf("GET /%s: %d, want 200", q, rec.Code)
		}
	}
}

func TestCalendarListsEachDayInOrderAndCapsTheCell(t *testing.T) {
	e := newCalEnv(t)
	// Five events on Saturday 21/11, stored out of order; three on the 22nd.
	for _, h := range []int{16, 10, 18, 12, 14} {
		e.event(t, "Ev "+string(rune('0'+h/10))+string(rune('0'+h%10)), e.at(time.November, 21, h))
	}
	for _, h := range []int{9, 10, 11} {
		e.event(t, "Dom "+string(rune('0'+h/10))+string(rune('0'+h%10)), e.at(time.November, 22, h))
	}
	body := e.body(t, "/")

	full := calCell(t, body, "2026-11-21")
	if got, want := calNames(full), []string{"Ev 10", "Ev 12", "Ev 14"}; !slices.Equal(got, want) {
		t.Errorf("the 21st lists %q, want %q (the first three by start time)", got, want)
	}
	// +N counts the rest and links to that day's list.
	more := calHref(t, full, "+2")
	if more.Path != "/eventos/dia" || more.Query().Get("data") != "2026-11-21" {
		t.Errorf("+2 links to %q, want /eventos/dia?data=2026-11-21", more)
	}
	// The compact grid shows one dot for each event that has a chip.
	if n := strings.Count(full, `<span class="calendar__dot"`); n != 3 {
		t.Errorf("the 21st has %d dots, want 3", n)
	}

	three := calCell(t, body, "2026-11-22")
	if got := calNames(three); len(got) != 3 || strings.Contains(three, "calendar__more") {
		t.Errorf("the 22nd = %q with more = %v; want 3 chips and no +N", got, strings.Contains(three, "calendar__more"))
	}
	if cell := calCell(t, body, "2026-11-23"); len(calChips(cell)) != 0 || strings.Contains(cell, "calendar__more") || strings.Contains(cell, "calendar__dot\"") {
		t.Errorf("an empty day shows events: %s", cell)
	}

	// Each day number links to that day's list, for the compact grid.
	for _, day := range []string{"2026-11-21", "2026-11-23", "2026-12-05"} {
		u := calHref(t, calCell(t, body, day), "<")
		if u.Path != "/eventos/dia" || u.Query().Get("data") != day {
			t.Errorf("the number of %s links to %q", day, u)
		}
	}
}

// The links of a day (its number and +N) open that day's list with the filter of
// the grid, so the counts of the grid and of the list agree. The filter is the
// one the page applied: a city of another state is dropped, as it is in the grid.
func TestCalendarDayLinksKeepTheFilter(t *testing.T) {
	e := newCalEnv(t)
	for _, h := range []int{10, 12, 14, 16, 18} {
		e.event(t, "Ev "+string(rune('0'+h/10))+string(rune('0'+h%10)), e.at(time.November, 21, h))
	}

	tests := []struct {
		name, query string
		keep        url.Values // what follows data= in each link
		more        bool       // the 21st still has events beyond the cell
	}{
		{"no filter", "", url.Values{}, true},
		{"a state", "?uf=CE", url.Values{"uf": {"CE"}}, true},
		{"a state and a city", "?uf=CE&cidade=Fortaleza", url.Values{"uf": {"CE"}, "cidade": {"Fortaleza"}}, true},
		{"a state with no events", "?uf=PE", url.Values{"uf": {"PE"}}, false},
		{"a city of another state", "?uf=PE&cidade=Fortaleza", url.Values{"uf": {"PE"}}, false},
		{"an unknown state", "?uf=XX&cidade=Fortaleza", url.Values{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := e.body(t, "/"+tt.query)
			links := map[string]*url.URL{
				"the number of the 21st": calHref(t, calCell(t, body, "2026-11-21"), "<"),
				"the number of the 23rd": calHref(t, calCell(t, body, "2026-11-23"), "<"),
			}
			if tt.more {
				links["+2"] = calHref(t, calCell(t, body, "2026-11-21"), "+2")
			} else if strings.Contains(calCell(t, body, "2026-11-21"), "calendar__more") {
				t.Errorf("the 21st shows +N under %s", tt.query)
			}
			for name, u := range links {
				day := "2026-11-23"
				if strings.Contains(name, "21st") || name == "+2" {
					day = "2026-11-21"
				}
				want := url.Values{"data": {day}}
				for k, v := range tt.keep {
					want[k] = v
				}
				if u.Path != "/eventos/dia" || u.RawQuery != want.Encode() {
					t.Errorf("%s links to %q, want /eventos/dia?%s", name, u, want.Encode())
				}
			}
		})
	}
}

func TestCalendarFilter(t *testing.T) {
	e := newCalEnv(t)
	e.event(t, "Noite Verde", e.at(time.November, 14, 22))
	e.event(t, "Sobral Fest", e.at(time.November, 15, 20), calPlace("CE", "Sobral"))
	e.event(t, "Recife Rave", e.at(time.November, 16, 20), calPlace("PE", "Recife"))
	all := []string{"Noite Verde", "Recife Rave", "Sobral Fest"}

	both, withSP := []string{"CE", "PE"}, []string{"CE", "PE", "SP"}
	tests := []struct {
		name, query string
		have        []string
		state, city string   // the options marked selected ("" is todos)
		states      []string // the state options: the states with events, and the one being filtered
		cities      []string
	}{
		{"todos by default", "", all, "", "", both, nil},
		{"a state", "?uf=CE", []string{"Noite Verde", "Sobral Fest"}, "CE", "", both, []string{"Fortaleza", "Sobral"}},
		{"a state and a city", "?uf=CE&cidade=Sobral", []string{"Sobral Fest"}, "CE", "Sobral", both, []string{"Fortaleza", "Sobral"}},
		{"another state", "?uf=PE", []string{"Recife Rave"}, "PE", "", both, []string{"Recife"}},
		{"a state with no events", "?uf=SP", nil, "SP", "", withSP, nil},
		{"an unknown state", "?uf=XX", all, "", "", both, nil},
		{"a lowercase state", "?uf=ce", all, "", "", both, nil},
		{"a state that is an operator", "?uf=%24ne", all, "", "", both, nil},
		{"a city with no state", "?cidade=Sobral", all, "", "", both, nil},
		{"a city of another state", "?uf=PE&cidade=Sobral", []string{"Recife Rave"}, "PE", "", both, []string{"Recife"}},
		{"an unknown city", "?uf=CE&cidade=Atlantis", []string{"Noite Verde", "Sobral Fest"}, "CE", "", both, []string{"Fortaleza", "Sobral"}},
		{"a city that is an operator", "?uf=CE&cidade=%24ne", []string{"Noite Verde", "Sobral Fest"}, "CE", "", both, []string{"Fortaleza", "Sobral"}},
		{"a city in another case", "?uf=CE&cidade=sobral", []string{"Noite Verde", "Sobral Fest"}, "CE", "", both, []string{"Fortaleza", "Sobral"}},
		{"a state key that is not uf", "?uf%5B%24ne%5D=", all, "", "", both, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := e.body(t, "/"+tt.query)
			names := calNames(body)
			slices.Sort(names)
			if !slices.Equal(names, tt.have) {
				t.Errorf("events = %q, want %q", names, tt.have)
			}
			form := regexp.MustCompile(`(?s)<form class="calendar__filter".*?</form>`).FindString(body)
			if form == "" {
				t.Fatalf("no filter form in:\n%s", body)
			}
			state := regexp.MustCompile(`(?s)<select [^>]*name="uf".*?</select>`).FindString(form)
			city := regexp.MustCompile(`(?s)<select [^>]*name="cidade".*?</select>`).FindString(form)

			// The state options: todos first, then exactly the states that have events
			// (the state being filtered is kept, so the select shows what the grid shows).
			wantAll(t, "state options", state, `<option value="">todos os estados</option>`)
			if got := calOptions(state); !slices.Equal(got, tt.states) {
				t.Errorf("state options = %q, want %q", got, tt.states)
			}
			wantAll(t, "city options", city, `<option value="">todas as cidades</option>`)
			if got := calSelected(state); got != tt.state {
				t.Errorf("selected state = %q, want %q", got, tt.state)
			}
			if got := calSelected(city); got != tt.city {
				t.Errorf("selected city = %q, want %q", got, tt.city)
			}
			// The city options are the cities with events in the chosen state; with no
			// state the select is disabled, so the browser does not send it.
			if tt.state == "" {
				if !strings.Contains(calOpenTag(city), "disabled") {
					t.Errorf("the city select is enabled with no state: %s", city)
				}
				// The hint that says why is a row of the form of its own, and the select points at it.
				wantAll(t, "city hint", form, `aria-describedby="calendar-cidade-dica"`, `class="hint calendar__filter-hint" id="calendar-cidade-dica"`)
				inOrder(t, form, "</select>", "</button>", `id="calendar-cidade-dica"`)
			} else {
				if strings.Contains(calOpenTag(city), "disabled") {
					t.Errorf("the city select is disabled for state %s", tt.state)
				}
				if strings.Contains(form, "calendar-cidade-dica") {
					t.Errorf("the hint about choosing a state shows with state %s", tt.state)
				}
			}
			if got := calOptions(city); !slices.Equal(got, tt.cities) {
				t.Errorf("city options = %q, want %q", got, tt.cities)
			}
		})
	}
}

// calSelected is the value of the option marked selected in a select, "" for none
// (todos has the empty value).
func calSelected(sel string) string {
	m := regexp.MustCompile(`<option value="([^"]*)" selected`).FindStringSubmatch(sel)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

// calOptions are the values of a select's options other than todos.
func calOptions(sel string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`<option value="([^"]+)"`).FindAllStringSubmatch(sel, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

// The month links and Hoje keep the filter, and the form keeps the month, so
// changing one never drops the other.
func TestCalendarLinksKeepTheFilterAndTheMonth(t *testing.T) {
	e := newCalEnv(t)
	e.event(t, "Sobral Fest", e.at(time.November, 15, 20), calPlace("CE", "Sobral"))
	body := e.body(t, "/?mes=2026-12&uf=CE&cidade=Sobral")

	for prefix, mes := range map[string]string{"[‹]": "2026-11", "[›]": "2027-01", "Hoje": "2026-11"} {
		u := calHref(t, body, prefix)
		q := u.Query()
		if u.Path != "/" || q.Get("mes") != mes || q.Get("uf") != "CE" || q.Get("cidade") != "Sobral" || len(q) != 3 {
			t.Errorf("%s links to %q, want / with mes=%s&uf=CE&cidade=Sobral", prefix, u, mes)
		}
	}
	form := regexp.MustCompile(`(?s)<form class="calendar__filter".*?</form>`).FindString(body)
	wantAll(t, "form", form, `method="get"`, `action="/"`, `<input type="hidden" name="mes" value="2026-12">`)
	if !strings.Contains(form, `type="submit"`) {
		t.Errorf("the filter has no submit button: %s", form)
	}

	// Without a filter the links carry only the month.
	body = e.body(t, "/")
	if got := calHref(t, body, "[›]"); got.RawQuery != "mes=2026-12" {
		t.Errorf("[›] without a filter links to %q, want /?mes=2026-12", got)
	}
}

// Everyone signed in sees every event: the calendar does not depend on who asks,
// not on a membership and not on the admin flag.
func TestCalendarEveryoneSignedInSeesEveryEvent(t *testing.T) {
	e := newCalEnv(t)
	alfa := e.event(t, "Do Alfa", e.at(time.November, 14, 22))
	// Delta is a collective the Membro, the Admin and the Outsider do not belong to.
	delta := evEvent(t, e.Store, e.Delta.ID, e.Owner.ID, func(ev *domain.Event) {
		ev.Name, ev.StartsAt = "Do Delta", e.at(time.November, 15, 20)
	})
	wantNames := []string{"Do Alfa", "Do Delta"}
	wantLinks := []string{"/eventos/" + alfa.ID, "/eventos/" + delta.ID}

	for _, who := range []struct {
		name string
		user domain.User
	}{{"admin", e.Admin}, {"owner", e.Owner}, {"member", e.Member}, {"outsider", e.Outsider}} {
		t.Run(who.name, func(t *testing.T) {
			rec := serve(e.r, http.MethodGet, "/", withCookie(e.Cookie[who.user.ID]))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /: %d %q; want 200", rec.Code, rec.Body)
			}
			body := rec.Body.String()
			if got := calNames(body); !slices.Equal(got, wantNames) {
				t.Errorf("events = %q, want %q", got, wantNames)
			}
			var links []string
			for _, chip := range calChips(body) {
				links = append(links, calAttrURL(t, chip, "href").Path)
			}
			if !slices.Equal(links, wantLinks) {
				t.Errorf("chip links = %q, want %q", links, wantLinks)
			}
			// The month swap answers the same.
			rec = serve(e.r, http.MethodGet, "/calendario", withCookie(e.Cookie[who.user.ID]))
			if got := calNames(rec.Body.String()); rec.Code != http.StatusOK || !slices.Equal(got, wantNames) {
				t.Errorf("GET /calendario: %d, events %q; want 200 and %q", rec.Code, got, wantNames)
			}
		})
	}
}

// --- the partial ---

func TestCalendarPartial(t *testing.T) {
	e := newCalEnv(t)
	e.event(t, "Noite Verde", e.at(time.November, 14, 22))
	e.event(t, "Sobral Fest", e.at(time.December, 2, 20), calPlace("CE", "Sobral"))

	for _, query := range []string{"", "?mes=2026-12", "?mes=2026-12&uf=CE", "?uf=CE&cidade=Sobral", "?mes=lixo&uf=XX"} {
		t.Run(query, func(t *testing.T) {
			page := e.body(t, "/"+query)
			for _, mod := range [][]func(*http.Request){nil, {hx}} {
				rec := e.get("/calendario"+query, mod...)
				if rec.Code != http.StatusOK {
					t.Fatalf("GET /calendario%s: %d %q", query, rec.Code, rec.Body)
				}
				if got := rec.Header().Get("Cache-Control"); got != "no-store" {
					t.Errorf("Cache-Control = %q, want no-store", got)
				}
				if got := rec.Header().Get("Content-Type"); got != htmlContentType {
					t.Errorf("Content-Type = %q, want %q", got, htmlContentType)
				}
				part := strings.TrimSpace(rec.Body.String())
				if !strings.HasPrefix(part, `<div class="calendar" id="calendar-month"`) {
					t.Errorf("the partial does not start with the month: %.80s", part)
				}
				for _, bad := range []string{"<html", "<main", "<aside", "<head", "x-data", "feito_por"} {
					if strings.Contains(part, bad) {
						t.Errorf("the partial contains %q", bad)
					}
				}
				// What the page shows for the month is exactly what the partial answers.
				if !strings.Contains(page, part) {
					t.Errorf("the page does not contain the partial.\npartial:\n%s", part)
				}
			}
		})
	}
}

func TestCalendarNeedsASession(t *testing.T) {
	store := &countingStore{}
	app := NewApp(testConfig(t), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), store)
	r := app.Router()

	rec := do(r, http.MethodGet, "/calendario", nil)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/entrar") {
		t.Errorf("got %d Location %q, want 303 to /entrar", rec.Code, rec.Header().Get("Location"))
	}
	rec = do(r, http.MethodGet, "/calendario", nil, hx)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("HX-Redirect") == "" {
		t.Errorf("htmx: %d HX-Redirect %q, want 401 and a redirect to the login", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if store.repos != 0 {
		t.Errorf("a request without a cookie asked the store for its repositories %d times, want 0", store.repos)
	}
}

// --- notices and notes ---

func TestCalendarNotice(t *testing.T) {
	e := newCalEnv(t)

	body := e.body(t, "/?msg=evento-excluido")
	wantAll(t, "notice", body, `role="status"`, ">evento excluído.</p>")
	if m := regexp.MustCompile(`<p ([^>]*)>evento excluído\.</p>`).FindStringSubmatch(body); m == nil || !strings.Contains(m[1], `role="status"`) {
		t.Errorf("the notice is not a role=status paragraph in:\n%s", body)
	}
	inOrder(t, body, "evento excluído.", `id="calendar-month"`) // above the month

	// Only the fixed codes show a text, and the code itself is never echoed.
	for _, q := range []string{"", "?msg=", "?msg=outro", "?msg=%3Cscript%3Ealert(1)%3C/script%3E", "?msg=Evento-Excluido", "?msg=evento-excluido%20", "?msg%5B%5D=evento-excluido"} {
		body := e.body(t, "/"+q)
		for _, bad := range []string{"evento excluído", "status--ok", "<script>alert", "outro"} {
			if strings.Contains(body, bad) {
				t.Errorf("GET /%s shows %q", q, bad)
			}
		}
	}

	// The notice belongs to the page: the partial a swap brings never has it.
	if body := e.body(t, "/calendario?msg=evento-excluido"); strings.Contains(body, "evento excluído") {
		t.Errorf("the partial shows the notice: %s", body)
	}
}

func TestCalendarTruncatedNote(t *testing.T) {
	app := newTestApp(t)
	const note = "mostrando só os primeiros 500 eventos deste mês"

	v := calMonthView()
	if body := calRender(t, app, newCalendarView(v)); strings.Contains(body, "mostrando só") {
		t.Errorf("a month under the limit shows the note: %s", body)
	}
	v.Truncated = true
	body := calRender(t, app, newCalendarView(v))
	m := regexp.MustCompile(`<p ([^>]*)>` + note + `</p>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no note %q in:\n%s", note, body)
	}
	wantAll(t, "note", m[1], `role="status"`)
	inOrder(t, body, note, "<table") // above the grid
}

// --- markup ---

func TestCalendarMarkup(t *testing.T) {
	e := newCalEnv(t)
	e.event(t, "Noite Verde", e.at(time.November, 14, 22))
	for _, h := range []int{10, 11, 12, 13} {
		e.event(t, "Muitos", e.at(time.November, 21, h))
	}
	body := e.body(t, "/")

	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}
	// The day list T4.19 fills: empty and announced politely.
	wantAll(t, "day list", body, `<section class="calendar__day" id="calendar-day" aria-live="polite"></section>`)
	// The grid is a table, with column headers and a name.
	wantAll(t, "grid", body, `<table class="calendar__grid" aria-labelledby="calendar-title">`, `id="calendar-title"`, `scope="col"`)
	// The compact grid (below 640px) hides the chips and shows the dots; both are in the markup.
	wantAll(t, "compact grid", body, `class="calendar__events"`, `class="calendar__dots"`, `class="calendar__dot"`)
	// Every control has its label.
	for _, id := range []string{"calendar-uf", "calendar-cidade"} {
		if !regexp.MustCompile(`<label [^>]*for="`+id+`"`).MatchString(body) || !strings.Contains(body, `id="`+id+`"`) {
			t.Errorf("the control %s has no label", id)
		}
	}
	// The sign-in shell is still there around it.
	wantAll(t, "shell", body, `<main id="conteudo"`, `aria-current="page"`)
}

// A person's text is escaped and an ID is a single path segment, whatever it holds.
func TestCalendarEscapesWhatItShows(t *testing.T) {
	e := newCalEnv(t)
	const name = `<script>alert(1)</script> "Fest" & Co`
	e.event(t, name, e.at(time.November, 14, 22))
	body := e.body(t, "/")
	if strings.Contains(body, "<script>alert") {
		t.Errorf("the event name is not escaped:\n%s", body)
	}
	if got := calNames(body); !slices.Equal(got, []string{name}) {
		t.Errorf("chip names = %q, want %q (escaped in the markup, equal once read)", got, name)
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}

	// An ID with characters that mean something in a URL stays in its segment.
	app := newTestApp(t)
	v := calMonthView()
	ev := domain.EventView{Event: domain.Event{ID: "a/b?c=d&e#f", Name: "x", Type: domain.EventTypeFesta, StartsAt: time.Date(2026, 11, 14, 22, 0, 0, 0, time.UTC)}}
	day := &v.Weeks[1][6] // Saturday 14/11
	day.Events, day.Shown = []domain.EventView{ev}, []domain.EventView{ev}
	cell := calCell(t, calRender(t, app, newCalendarView(v)), "2026-11-14")
	chips := calChips(cell)
	if len(chips) != 1 {
		t.Fatalf("%d chips, want 1: %s", len(chips), cell)
	}
	u := calAttrURL(t, chips[0], "href")
	if u.EscapedPath() != "/eventos/a%2Fb%3Fc=d&e%23f" || u.RawQuery != "" || u.Fragment != "" {
		t.Errorf("the chip links to %q, want the ID escaped as one path segment", u)
	}
}

// --- failures ---

type calFailingEvents struct{ domain.Events }

func (calFailingEvents) ListBetween(context.Context, domain.EventFilter, int) ([]domain.EventView, error) {
	return nil, errors.New("the events are down: secret-detail")
}

func TestCalendarServerError(t *testing.T) {
	e := newCalEnv(t)
	e.App.Store = repoOverride{Store: e.Store, mutate: func(r *domain.Repos) { r.Events = calFailingEvents{r.Events} }}
	logs := captureLogs(e.App)

	for _, target := range []string{"/", "/calendario", "/?mes=2026-12&uf=CE"} {
		t.Run(target, func(t *testing.T) {
			logs.Reset()
			rec := e.get(target)
			if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
				t.Fatalf("GET %s: %d %q; want 500 erro interno", target, rec.Code, rec.Body)
			}
			var logged bool
			for _, l := range logLines(t, logs) {
				if l["level"] == "ERROR" && l["msg"] != "request" {
					logged = true
				}
			}
			if !logged {
				t.Errorf("the failure was not logged: %s", logs)
			}
		})
	}
}

// --- style ---

// Every class the calendar uses has a rule in components.css, and so does the
// utility the event page shares.
func TestCalendarComponentsAreStyled(t *testing.T) {
	css, err := fs.ReadFile(webfs.Static, "static/css/components.css")
	if err != nil {
		t.Fatal(err)
	}
	e := newCalEnv(t)
	for i := range 4 {
		e.event(t, "Muitos", e.at(time.November, 21, 10+i))
	}
	e.event(t, "Antes", e.at(time.November, 5, 10))
	e.event(t, "Depois", e.at(time.December, 2, 10))
	body := e.body(t, "/")

	classes := map[string]bool{"text-pre-line": true}
	for _, m := range regexp.MustCompile(`class="([^"]*)"`).FindAllStringSubmatch(body, -1) {
		for _, c := range strings.Fields(m[1]) {
			if strings.HasPrefix(c, "calendar") {
				classes[c] = true
			}
		}
	}
	for _, want := range []string{"calendar", "calendar__grid", "calendar__cell", "calendar__cell--today", "calendar__cell--past", "calendar__cell--out",
		"calendar__chip", "calendar__more", "calendar__dot", "calendar__day"} {
		if !classes[want] {
			t.Errorf("the page shows no %q, so the check below would not cover it", want)
		}
	}
	for c := range classes {
		if !strings.Contains(string(css), "."+c+" {") {
			t.Errorf("components.css has no rule for .%s", c)
		}
	}
}
