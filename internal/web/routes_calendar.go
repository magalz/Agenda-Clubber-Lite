package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/events"
)

// calendarView is the Data of app/home and of the calendar-month partial.
type calendarView struct {
	Notice string
}

// newCalendarView turns a month grid into what the templates show.
func newCalendarView(v events.MonthView) calendarView {
	return calendarView{}
}

// calendarRoutes registers the month calendar: GET / (the page) and GET
// /calendario (only the month, for HTMX). Task T4.10 (ACL-81) writes them; until
// then / keeps the placeholder page.
func (a *App) calendarRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/", a.home)
}

// home renders the calendar page. It is a placeholder until the calendar
// lands.
func (a *App) home(c *gin.Context) {
	a.page(c, http.StatusOK, "app/home", PageData{Title: "Calendário", Nav: "calendario"})
}
