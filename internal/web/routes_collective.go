package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/approvals"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// collectiveNotices maps the ?msg= codes of /coletivo/:id to their text. A code
// that is not here shows nothing: the URL never supplies text.
var collectiveNotices = map[string]string{
	"pedido-aprovado": "aprovação registrada.",
	"pedido-recusado": "pedido recusado. a pessoa recebe um e-mail.",
	"promovido":       "membro promovido a administrador.",
	"rebaixado":       "administrador rebaixado a membro.",
	"nome-alterado":   "nome alterado.",
}

// collectivePage is the Data of app/coletivo. Notice is the status above the page
// (an action went through), Alert the alert (a refused or invalid action, the rate
// limit); either is empty for none. RenameValue is what the rename field holds:
// the name the person typed, or the collective's own.
type collectivePage struct {
	ID            string
	Name          string
	Kind          string
	Locked        bool
	EventsBlocked bool
	Members       []collectiveMember
	Requests      []collectiveRequest
	RenameValue   string
	Notice        string
	Alert         string
}

// collectiveMember is one approved member. Role is the label shown (Master for the
// creator); Badge is the badge variant, empty for the muted default. CanPromote and
// CanDemote say which action the row offers, none on a locked collective: only a
// Membro is promoted, and only an Administrador who is not the Master is demoted.
type collectiveMember struct {
	UserID     string
	Name       string
	Role       string
	Badge      string
	CanPromote bool
	CanDemote  bool
}

// collectiveRequest is a request to join that an Administrador can still decide.
// Role is the label of the role the person asks for.
type collectiveRequest struct {
	UserID string
	Name   string
	Role   string
}

// collectiveRoutes registers a collective's management page (/coletivo/:id),
// open only to its approved Administradores, and its actions.
func (a *App) collectiveRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/coletivo/:id", a.collectiveShow)
	authed.POST("/coletivo/:id/pedido/aprovar", a.collectiveAct("approve join", "pedido-aprovado", "", false,
		func(ctx context.Context, u domain.User, id string, form url.Values, now time.Time) error {
			return approvals.OwnerApproveJoin(ctx, a.Store, a.Cfg.Origin, u, form.Get("user_id"), id, now)
		}))
	authed.POST("/coletivo/:id/pedido/recusar", a.collectiveAct("reject join", "pedido-recusado", "", false,
		func(ctx context.Context, u domain.User, id string, form url.Values, now time.Time) error {
			return approvals.RejectJoin(ctx, a.Store, a.Cfg.Origin, u, form.Get("user_id"), id, form.Get("reason"), now)
		}))
	authed.POST("/coletivo/:id/membro/promover", a.collectiveAct("promote member", "promovido", "", false,
		func(ctx context.Context, u domain.User, id string, form url.Values, now time.Time) error {
			return approvals.Promote(ctx, a.Store, u, form.Get("user_id"), id, now)
		}))
	authed.POST("/coletivo/:id/membro/rebaixar", a.collectiveAct("demote member", "rebaixado", "voce-rebaixado", false,
		func(ctx context.Context, u domain.User, id string, form url.Values, now time.Time) error {
			return approvals.Demote(ctx, a.Store, u, form.Get("user_id"), id, now)
		}))
	authed.POST("/coletivo/:id/nome", a.collectiveAct("rename collective", "nome-alterado", "", true,
		func(ctx context.Context, u domain.User, id string, form url.Values, now time.Time) error {
			return approvals.RenameCollective(ctx, a.Store, a.Cfg.Origin, u, id, form.Get("name"), now)
		}))
}

// collectiveShow is the page: the members, the requests to decide and the rename
// form. Only the fixed text a known ?msg= code stands for is shown.
func (a *App) collectiveShow(c *gin.Context) {
	u, ok := a.collectivesUser(c)
	if !ok {
		return
	}
	a.collectiveRender(c, u, c.Param("id"), http.StatusOK, nil, nil, collectiveNotices[c.Query("msg")], "")
}

// collectiveAct is the flow the five actions share. do runs the action of
// internal/approvals, which decides for itself whether the person may take it; the
// handler checks the person's tie to the collective first, so that somebody who is
// not in it gets the 404 of the page, and not a 403 that says it exists. limited
// counts the post against the person's budget (Limits.Requests) before the body is
// read. A refused or failed post answers with the page, so the person sees the
// current state. On success it redirects to the page with the notice code, or, when
// the form's user_id is the person's own and selfCode is not empty, to Meus Coletivos
// with selfCode: an action on oneself can take the page away (a demotion), and the
// page then answers the person with a 404. Once the pre-check above has passed, a
// domain.ErrNotFound from do means the request or the member is gone (another
// Administrador decided it first), so it is answered like a decided request, not
// like a collective the person may not see. The log names the person and the error,
// never what was typed.
func (a *App) collectiveAct(logMsg, code, selfCode string, limited bool, do func(context.Context, domain.User, string, url.Values, time.Time) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		u, ok := a.collectivesUser(c)
		if !ok {
			return
		}
		ctx, id := c.Request.Context(), c.Param("id")
		switch m, err := a.Store.Repos().Memberships.Get(ctx, u.ID, id); {
		case errors.Is(err, domain.ErrNotFound):
			collectiveNotFound(c)
			return
		case err != nil:
			a.Log.Error(logMsg+": read membership", "user", u.ID, "collective", id, "err", err)
			c.String(http.StatusInternalServerError, "erro interno")
			return
		case m.Status != domain.MembershipStatusApproved || m.Role != domain.RoleOwner:
			c.String(http.StatusForbidden, approvalsMsgNotAllowed)
			return
		}
		now := a.Now()
		allowed := !limited || a.Limits.Requests.Allow("request:"+u.ID, now)
		form, err := a.parseForm(c)
		if err != nil {
			c.String(http.StatusBadRequest, "requisição inválida")
			return
		}
		if !allowed {
			a.collectiveRender(c, u, id, http.StatusTooManyRequests, form, nil, "", msgTooMany)
			return
		}
		var fieldErrs domain.FieldErrors
		switch err := do(ctx, u, id, form, now); {
		case err == nil && selfCode != "" && form.Get("user_id") == u.ID:
			a.redirect(c, "/coletivos?msg="+selfCode)
		case err == nil:
			a.redirect(c, "/coletivo/"+url.PathEscape(id)+"?msg="+code)
		case errors.As(err, &fieldErrs):
			// The name's message goes under the rename field; the reason's is an alert.
			a.collectiveRender(c, u, id, http.StatusUnprocessableEntity, form, fieldErrs, "", fieldErrs["reason"])
		default:
			status, msg, ok := approvalsAnswer(err)
			switch {
			case !ok:
				a.Log.Error(logMsg, "user", u.ID, "collective", id, "err", err)
				c.String(http.StatusInternalServerError, "erro interno")
			case status == http.StatusNotFound:
				// The request or the member is gone: the page, read afresh, without it. If
				// the collective is gone too, collectiveRender answers with the 404.
				a.collectiveRender(c, u, id, http.StatusConflict, form, nil, "", approvalsMsgDecided)
			case status == http.StatusForbidden:
				c.String(status, msg)
			default:
				a.collectiveRender(c, u, id, status, form, nil, "", msg)
			}
		}
	}
}

// collectiveNotFound answers like a path no route serves, the way Gin does it for
// one (serveError): its status, its body and a Content-Type of exactly text/plain,
// with no other header of its own, so nobody can tell a collective they may not
// manage from one that does not exist. Gin ignores a failed write too: the client
// is gone and there is nothing left to answer.
func collectiveNotFound(c *gin.Context) {
	c.Header("Content-Type", "text/plain")
	c.Status(http.StatusNotFound)
	_, _ = c.Writer.WriteString("404 page not found")
}

// collectiveRender answers with the page, read afresh for the person: the members
// and the requests, plus what the answer carries (the form to refill, its field
// errors, a notice or an alert). A collective the person may not manage is the
// 404; one that cannot be read is a logged 500.
func (a *App) collectiveRender(c *gin.Context, u domain.User, id string, status int, form url.Values, errs domain.FieldErrors, notice, alert string) {
	view, err := approvals.CollectiveView(c.Request.Context(), a.Store, u, id)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		collectiveNotFound(c)
		return
	case err != nil:
		a.Log.Error("collective view", "user", u.ID, "collective", id, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	col := view.Collective
	data := collectivePage{
		ID: col.ID, Name: col.Name, Kind: kindLabel(col.Kind),
		Locked: col.Locked, EventsBlocked: col.EventsBlocked,
		RenameValue: col.Name, Notice: notice, Alert: alert,
	}
	if form.Has("name") {
		data.RenameValue = form.Get("name")
	}
	for _, m := range view.Members {
		item := collectiveMember{UserID: m.UserID, Name: m.UserName, Role: approvals.RoleLabel(m.Role)}
		switch {
		case view.IsMaster(m.UserID):
			item.Role, item.Badge = roleLabelMaster, "accent"
		case m.Role == domain.RoleOwner:
			item.CanDemote = !col.Locked
		default:
			item.CanPromote = !col.Locked
		}
		data.Members = append(data.Members, item)
	}
	for _, r := range view.Requests {
		data.Requests = append(data.Requests, collectiveRequest{UserID: r.UserID, Name: r.UserName, Role: approvals.RoleLabel(r.Role)})
	}
	a.page(c, status, "app/coletivo", PageData{
		Title: col.Name, Nav: "coletivos", Form: form, Errors: errs, Data: data,
	})
}
