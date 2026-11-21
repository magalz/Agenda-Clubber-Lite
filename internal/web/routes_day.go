package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/events"
)

// dayDateMsg is the answer to a missing or malformed ?data=. It never echoes
// what was asked for.
const dayDateMsg = "escolha uma data válida (dd/mm/aaaa)"

// dayRow is an event of the list with the path of its page.
type dayRow struct {
	events.DayItem
	Href string
}

// dayList is the Data of the day-list partial and of app/dia: the events of a day
// (Rows, in order of start time, the events held in the highlighted city first
// with SameCity set), or Error and no rows when the date is not valid. Date is the
// day, zero when it is not valid; Back is the calendar's month of that day with
// the filter kept, which only the page shows.
type dayList struct {
	Date      time.Time
	Rows      []dayRow
	Truncated bool
	Limit     int
	Error     string
	Back      string
}

// dayRoutes registers the day list (Analisar Data): GET /eventos/dia, the page
// or, for HTMX, only the list. Everyone signed in sees every event, so it needs
// no rights of its own.
func (a *App) dayRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/eventos/dia", a.dayShow)
}

// dayShow lists the events of ?data= (AAAA-MM-DD or dd/mm/aaaa). ?uf= and
// ?cidade= are the calendar's filter, kept so the way back and the list agree
// with what the grid counted. ?destaque_uf= and ?destaque_cidade= are the city of
// the event form: they restrict nothing, they put its events first. A state or
// city that is not in the catalog counts as none. A date that does not parse is a
// 422 with its message and no list, never a 500.
func (a *App) dayShow(c *gin.Context) {
	day, ok := events.ParseDay(c.Query("data"), a.Cfg.Location)
	if !ok {
		a.dayRender(c, http.StatusUnprocessableEntity, dayList{Error: dayDateMsg, Back: "/"})
		return
	}
	filter := events.Filter(c.Query("uf"), c.Query("cidade"))
	view, err := events.Day(c.Request.Context(), a.Store, a.Cfg.Location, day, filter, events.Filter(c.Query("destaque_uf"), c.Query("destaque_cidade")))
	if err != nil {
		a.Log.Error("day list", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	list := dayList{
		Date:      view.Date,
		Rows:      make([]dayRow, len(view.Items)),
		Truncated: view.Truncated,
		Limit:     domain.ListLimitMax,
		Back:      calendarHref(view.Date.Format("2006-01"), filter),
	}
	for i, item := range view.Items {
		list.Rows[i] = dayRow{DayItem: item, Href: eventPath(item.ID)}
	}
	a.dayRender(c, http.StatusOK, list)
}

// dayRender answers with the day-list partial for an HTMX request and the page
// otherwise.
func (a *App) dayRender(c *gin.Context, status int, list dayList) {
	if c.GetHeader("HX-Request") == "true" {
		c.Header("Cache-Control", "no-store")
		a.Render.Partial(c, status, "day-list", list)
		return
	}
	title := "Analisar data"
	if !list.Date.IsZero() {
		title = "Eventos de " + fmtDate(list.Date, a.Cfg.Location)
	}
	a.page(c, status, "app/dia", PageData{Title: title, Nav: "calendario", Data: list})
}
