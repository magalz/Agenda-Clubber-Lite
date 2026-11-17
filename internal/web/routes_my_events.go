package web

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/approvals"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/events"
)

// The note of a Meus Eventos row the person may not edit: the reason, in the same
// order the event page tells it. A Membro is a Membro whatever the collective's
// state; the block of the collective's events is told only to the Administrador
// it stops. myEventsNoteOther is for the rest (an Administrador of a collective
// that is not approved).
const (
	myEventsNoteMember  = "só leitura (Membro)"
	myEventsNoteBlocked = "eventos bloqueados pelo admin"
	myEventsNoteOther   = "só leitura"
)

// myEventsPage is the Data of app/meus-eventos: the rows under Próximos (those
// that have not ended, by start time) and under Anteriores (the ended ones, latest
// first), as events.Mine orders them.
type myEventsPage struct {
	Upcoming, Past []myEventRow
}

// myEventRow is an event of Meus Eventos with what the row shows besides it: the
// label of the person's role in the collective, the escaped paths of the event and
// of its edit page, and, when the person may not edit it (CanEdit false), Note,
// the reason.
type myEventRow struct {
	events.MineItem
	RoleLabel string
	Href      string
	EditURL   string
	Note      string
}

// myEventsRoutes registers Meus Eventos: GET /meus-eventos, for a signed-in user.
// What each row allows is decided by internal/events (Mine).
func (a *App) myEventsRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/meus-eventos", a.myEventsShow)
}

// myEventsShow lists the events of the collectives where the person has an
// approved membership. Events that cannot be read are a logged 500.
func (a *App) myEventsShow(c *gin.Context) {
	u, ok := a.signedInUser(c, "my events")
	if !ok {
		return
	}
	v, err := events.Mine(c.Request.Context(), a.Store, u, a.Now())
	if err != nil {
		a.Log.Error("my events", "user", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	a.page(c, http.StatusOK, "app/meus-eventos", PageData{
		Title: "Meus Eventos", Nav: "meus-eventos",
		Data: myEventsPage{Upcoming: myEventRows(v.Upcoming), Past: myEventRows(v.Past)},
	})
}

// myEventRows turns the items of events.Mine into the rows of the page, in order.
func myEventRows(items []events.MineItem) []myEventRow {
	rows := make([]myEventRow, len(items))
	for i, it := range items {
		rows[i] = myEventRow{
			MineItem:  it,
			RoleLabel: approvals.RoleLabel(it.Role),
			Href:      eventPath(it.ID),
			EditURL:   eventURL(it.ID, "editar"),
			Note:      myEventsNote(it),
		}
	}
	return rows
}

// myEventsNote is the reason a row is read-only, or "" for a row the person may
// edit.
func myEventsNote(it events.MineItem) string {
	switch {
	case it.CanEdit:
		return ""
	case it.Role == domain.RoleMember:
		return myEventsNoteMember
	case it.Blocked:
		return myEventsNoteBlocked
	}
	return myEventsNoteOther
}
