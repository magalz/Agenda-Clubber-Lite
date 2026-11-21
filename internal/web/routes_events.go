package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/events"
)

// eventNotices maps the ?msg= codes of /eventos/:id to their text. A code that is
// not here shows nothing: the URL never supplies text.
var eventNotices = map[string]string{
	"evento-criado": "evento criado.",
	"evento-salvo":  "alterações salvas.",
}

// eventDetailData is the Data of app/evento and the dot of the event-detail
// partial: the event with the viewer's rights (events.Detail), its ticket link
// when it is a safe one, the paths of its edit page and deletion, and what the
// answer carries. Notice is the status above the page, which only the page shows.
// Alert is the reason a refused deletion gives. BlockedNote says to tell an
// Administrador why the event is read-only: the admin blocked the collective's
// events (DetailView.EditBlocked, unless an alert already says it).
type eventDetailData struct {
	events.DetailView
	TicketHref  string
	EditURL     string
	DeleteURL   string
	BlockedNote bool
	Notice      string
	Alert       string
}

// eventRoutes registers an event's detail (GET /eventos/:id, the page or, for
// HTMX, the sheet's content) and its deletion (POST /eventos/:id/excluir). Both
// need a signed-in user; the event's own rights are checked by internal/events.
func (a *App) eventRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/eventos/:id", a.eventShow)
	authed.POST("/eventos/:id/excluir", a.eventDelete)
}

// eventShow is the detail of an event, for any signed-in user: the calendar shows
// every event. An unknown or malformed ID is the 404 of a path no route serves.
// Only the fixed text a known ?msg= code stands for is shown.
func (a *App) eventShow(c *gin.Context) {
	u, ok := a.signedInUser(c, "events")
	if !ok {
		return
	}
	a.eventRender(c, u, c.Param("id"), http.StatusOK, eventNotices[c.Query("msg")], "")
}

// eventDelete deletes the event for the signed-in user, who must be an
// Administrador of its collective or the admin; internal/events decides. It
// redirects to the calendar on success. A refusal for lack of rights is a plain
// 403, an unknown event the 404 of an unregistered path, and a block of the
// collective's events (or an edit that raced it) the detail, read afresh, with
// the reason as its alert. The log names the person and the error, never the
// event's text.
func (a *App) eventDelete(c *gin.Context) {
	u, ok := a.signedInUser(c, "events")
	if !ok {
		return
	}
	id := c.Param("id")
	err := events.Delete(c.Request.Context(), a.Store, u, id, a.Now(), a.Cfg.Location)
	if err == nil {
		a.redirect(c, "/?msg=evento-excluido")
		return
	}
	status, msg, ok := eventsAnswer(err)
	switch {
	case !ok:
		a.Log.Error("delete event", "user", u.ID, "event", id, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	case status == http.StatusNotFound:
		answerNotFound(c)
	case status == http.StatusForbidden:
		c.String(status, msg)
	default:
		a.eventRender(c, u, id, status, "", msg)
	}
}

// eventRender answers with the event, read afresh for u: the event-detail partial
// for an HTMX request, the page otherwise. An event that is not there is the 404
// of an unregistered path, and nothing is set on the response before it is read.
// One that cannot be read is a logged 500.
func (a *App) eventRender(c *gin.Context, u domain.User, id string, status int, notice, alert string) {
	view, err := events.Detail(c.Request.Context(), a.Store, u, id)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		answerNotFound(c)
		return
	case err != nil:
		a.Log.Error("event detail", "user", u.ID, "event", id, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	data := eventDetailData{
		DetailView: view,
		TicketHref: ticketHref(view.TicketURL),
		EditURL:    eventURL(view.ID, "editar"),
		DeleteURL:  eventURL(view.ID, "excluir"),
		// Only an Administrador is told the event is read-only because of the block
		// (events.Detail decides who). An alert already says the same.
		BlockedNote: view.EditBlocked && alert == "",
		Notice:      notice,
		Alert:       alert,
	}
	if c.GetHeader("HX-Request") == "true" {
		c.Header("Cache-Control", "no-store")
		a.Render.Partial(c, status, "event-detail", data)
		return
	}
	a.page(c, status, "app/evento", PageData{Title: view.Name, Nav: "calendario", Data: data})
}

// eventURL is the path of an event's page for action ("editar", "excluir"). The
// ID is opaque, so it goes in as one escaped path segment.
func eventURL(id, action string) string {
	return eventPath(id) + "/" + action
}

// eventPath is the path of an event's page, with the ID escaped. Every link to an
// event is built here, so a change of the route changes every link.
func eventPath(id string) string {
	return "/eventos/" + url.PathEscape(id)
}

// ticketHref is the ticket link of an event, or "" when domain.TicketURLProblem
// refuses it. The store refuses the same links, so this is the second check, at
// the place where the link becomes an href.
func ticketHref(raw string) string {
	if domain.TicketURLProblem(raw) != "" {
		return ""
	}
	return raw
}
