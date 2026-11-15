package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/catalog"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/events"
)

// The month calendar. GET / is the page and GET /calendario is only its month
// (the filter and the grid), for the HTMX swap a later task adds. Both read
// ?mes=AAAA-MM, ?uf= and ?cidade=. Everything works as plain links and a GET
// form, so the page is usable without JavaScript.

// calendarNotices are the texts ?msg= can show on the page. Only a code in this
// list shows anything, and the code itself is never echoed.
var calendarNotices = map[string]string{
	"evento-excluido": "evento excluído.",
}

// calendarOption is one <option> of the state or city select.
type calendarOption struct {
	Value, Label string
	Selected     bool
}

// calendarChip is an event in a cell: a link to its page.
type calendarChip struct {
	Href  string
	Event domain.Event
}

// calendarDay is a grid cell with the links its template needs. The links are
// built here, with url.Values and url.PathEscape, and never by concatenating
// data in a template.
type calendarDay struct {
	events.GridDay
	DayHref string
	Chips   []calendarChip
}

// calendarView is the Data of app/home and of the calendar-month partial.
type calendarView struct {
	Month                         time.Time // the first day, for the title
	Key                           string
	PrevHref, NextHref, TodayHref string
	State                         string // the state being filtered, "" for todos
	States, Cities                []calendarOption
	Weeks                         [][7]calendarDay
	Truncated                     bool
	Limit                         int
	Notice                        string // set by the page only
}

// newCalendarView turns a month grid into what the templates show.
func newCalendarView(v events.MonthView) calendarView {
	out := calendarView{
		Month:     v.Month,
		Key:       v.Key,
		PrevHref:  calendarHref(v.PrevKey, v.Filter),
		NextHref:  calendarHref(v.NextKey, v.Filter),
		TodayHref: calendarHref(v.TodayKey, v.Filter),
		State:     v.Filter.State,
		States:    calendarStates(v),
		Cities:    calendarCities(v),
		Weeks:     make([][7]calendarDay, len(v.Weeks)),
		Truncated: v.Truncated,
		Limit:     domain.ListLimitMax,
	}
	for i, week := range v.Weeks {
		for j, day := range week {
			d := calendarDay{GridDay: day, DayHref: "/eventos/dia?" + url.Values{"data": {day.Key}}.Encode()}
			for _, e := range day.Shown {
				d.Chips = append(d.Chips, calendarChip{Href: "/eventos/" + url.PathEscape(e.ID), Event: e.Event})
			}
			out.Weeks[i][j] = d
		}
	}
	return out
}

// calendarHref is the page of a month with the filter kept.
func calendarHref(month string, filter domain.Place) string {
	q := url.Values{"mes": {month}}
	if filter.State != "" {
		q.Set("uf", filter.State)
	}
	if filter.City != "" {
		q.Set("cidade", filter.City)
	}
	return "/?" + q.Encode()
}

// calendarStates are the states that have events, in the catalog's order, plus
// the state being filtered, so the select always shows what the grid shows.
func calendarStates(v events.MonthView) []calendarOption {
	has := map[string]bool{v.Filter.State: v.Filter.State != ""}
	for _, p := range v.Places {
		has[p.State] = true
	}
	var out []calendarOption
	for _, s := range catalog.States() {
		if has[s.Code] {
			out = append(out, calendarOption{Value: s.Code, Label: s.Name + " (" + s.Code + ")", Selected: s.Code == v.Filter.State})
		}
	}
	return out
}

// calendarCities are the cities of the filtered state that have events, in the
// catalog's order, plus the city being filtered. It is empty with no state.
func calendarCities(v events.MonthView) []calendarOption {
	if v.Filter.State == "" {
		return nil
	}
	has := map[string]bool{v.Filter.City: v.Filter.City != ""}
	for _, p := range v.Places {
		if p.State == v.Filter.State {
			has[p.City] = true
		}
	}
	var out []calendarOption
	for _, city := range catalog.Cities(v.Filter.State) {
		if has[city] {
			out = append(out, calendarOption{Value: city, Label: city, Selected: city == v.Filter.City})
		}
	}
	return out
}

// calendarRoutes registers the month calendar: GET / (the page) and GET
// /calendario (only the month, for HTMX).
func (a *App) calendarRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/", a.home)
	authed.GET("/calendario", a.calendarMonth)
}

// loadCalendar reads the month and the place of the query string and builds the
// grid. A month that does not parse is the current one and a state or city that
// is not in the catalog means todos. On a store error it answers 500 itself and
// returns false.
func (a *App) loadCalendar(c *gin.Context) (calendarView, bool) {
	loc, now := a.Cfg.Location, a.Now()
	month, ok := events.ParseMonth(c.Query("mes"), loc)
	if !ok {
		month = now.In(loc)
	}
	v, err := events.Month(c.Request.Context(), a.Store, loc, month, events.Filter(c.Query("uf"), c.Query("cidade")), now)
	if err != nil {
		a.Log.Error("calendar: month", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return calendarView{}, false
	}
	return newCalendarView(v), true
}

// home renders the calendar page.
func (a *App) home(c *gin.Context) {
	v, ok := a.loadCalendar(c)
	if !ok {
		return
	}
	v.Notice = calendarNotices[c.Query("msg")]
	a.page(c, http.StatusOK, "app/home", PageData{Title: "Calendário", Nav: "calendario", Data: v})
}

// calendarMonth answers only the month, with no layout.
func (a *App) calendarMonth(c *gin.Context) {
	v, ok := a.loadCalendar(c)
	if !ok {
		return
	}
	c.Header("Cache-Control", "no-store")
	a.Render.Partial(c, http.StatusOK, "calendar-month", v)
}
