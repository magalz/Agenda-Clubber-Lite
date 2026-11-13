package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/approvals"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The fixed messages of Meus Coletivos. The two notices are chosen by the ?msg=
// code of the redirect that follows a request (collectivesNotices): the URL never
// supplies text.
const (
	collectivesMsgJoin   = "pedido enviado. você recebe um e-mail quando ele for decidido."
	collectivesMsgNew    = "pedido de coletivo novo enviado ao admin."
	collectivesMsgLocked = "este coletivo não está aceitando pedidos agora."
	collectivesMsgPick   = "escolha um coletivo da lista"
)

// collectivesNotices maps the ?msg= codes of /coletivos to their text. A code
// that is not here shows nothing.
var collectivesNotices = map[string]string{
	"pedido-enviado":  collectivesMsgJoin,
	"coletivo-pedido": collectivesMsgNew,
}

// The pt-BR status of a membership row.
const (
	statusApproved   = "aprovado"
	statusRejected   = "recusado"
	statusNewCollect = "aguardando o admin (coletivo novo)"
	statusBothOpen   = "aguardando o admin e um administrador"
	statusOwnersOpen = "aguardando um administrador do coletivo"
	statusSiteAdmin  = "aguardando o admin"
)

// roleLabelMaster is the role the creator of a collective is shown with.
const roleLabelMaster = "Master"

// collectivesPage is the Data of app/coletivos. Notice is the status above the
// list (a request was sent), Alert the alert (a locked collective, the rate
// limit); either is empty for none.
type collectivesPage struct {
	Items  []collectiveItem
	Notice string
	Alert  string
}

// collectiveItem is one row of the list: a membership of the signed-in person.
// Badge is the status badge variant (ok, warn or accent, see components.css).
// Href is the collective's page, set only for an approved Administrador of an
// approved collective; it is empty for every other row.
type collectiveItem struct {
	Name   string
	Kind   string
	Role   string
	Status string
	Badge  string
	Href   string
}

// collectivesRoutes registers Meus Coletivos (/coletivos) and its join and new
// collective requests. GET /coletivos/sugestoes (public) stays with the signup
// flow.
func (a *App) collectivesRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/coletivos", a.collectivesShow)
	authed.POST("/coletivos/entrar", a.collectivesJoin)
	authed.POST("/coletivos/novo", a.collectivesNew)
}

// collectivesUser is the signed-in person. The authed group guarantees there is
// one, so a miss is a bug: it is logged and answered with a 500.
func (a *App) collectivesUser(c *gin.Context) (domain.User, bool) {
	u, ok := a.currentUser(c)
	if !ok {
		a.Log.Error("collectives: no signed-in user on an authed route")
		c.String(http.StatusInternalServerError, "erro interno")
	}
	return u, ok
}

// collectivesRender answers with the page: the person's memberships, plus what
// the answer carries (the form to refill, its field errors, a notice or an
// alert). Memberships that cannot be read are a logged 500.
func (a *App) collectivesRender(c *gin.Context, u domain.User, status int, form url.Values, errs domain.FieldErrors, notice, alert string) {
	items, err := a.collectiveItems(c.Request.Context(), u)
	if err != nil {
		a.Log.Error("list memberships", "user", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	a.page(c, status, "app/coletivos", PageData{
		Title: "Meus coletivos", Nav: "coletivos", Form: form, Errors: errs,
		Data: collectivesPage{Items: items, Notice: notice, Alert: alert},
	})
}

// collectiveItems lists the person's memberships as the rows of the page, in the
// order the store returns them (by the collective's name). It reads each
// collective for its creator, who is shown as the Master.
func (a *App) collectiveItems(ctx context.Context, u domain.User) ([]collectiveItem, error) {
	repos := a.Store.Repos()
	views, err := repos.Memberships.ListByUser(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("list memberships: %w", err)
	}
	items := make([]collectiveItem, len(views))
	for i, v := range views {
		col, err := repos.Collectives.ByID(ctx, v.CollectiveID)
		if err != nil {
			return nil, fmt.Errorf("read collective: %w", err)
		}
		role := approvals.RoleLabel(v.Role)
		if col.CreatedBy == u.ID {
			role = roleLabelMaster
		}
		item := collectiveItem{Name: v.CollectiveName, Kind: kindLabel(v.CollectiveKind), Role: role}
		item.Status, item.Badge = membershipStatus(v)
		if v.Status == domain.MembershipStatusApproved && v.CollectiveStatus == domain.CollectiveStatusApproved && v.Role == domain.RoleOwner {
			item.Href = "/coletivo/" + v.CollectiveID
		}
		items[i] = item
	}
	return items, nil
}

// kindLabel is the pt-BR name of a collective kind.
func kindLabel(k domain.CollectiveKind) string {
	if k == domain.CollectiveKindProdutora {
		return "Produtora"
	}
	return "Coletivo"
}

// membershipStatus is the status text of a membership row and its badge variant.
// A new collective comes before the sides of the membership: its creator's
// membership already carries the owner side's approval, yet what the person
// waits for is the site admin's decision about the collective itself.
func membershipStatus(v domain.MembershipView) (status, badge string) {
	switch {
	case v.Status == domain.MembershipStatusApproved:
		return statusApproved, "ok"
	case v.Status == domain.MembershipStatusRejected || v.CollectiveStatus == domain.CollectiveStatusRejected:
		return statusRejected, "accent"
	case v.CollectiveStatus == domain.CollectiveStatusPending:
		return statusNewCollect, "warn"
	case v.AdminApprovedAt == nil && v.OwnerApprovedAt == nil:
		return statusBothOpen, "warn"
	case v.OwnerApprovedAt != nil:
		return statusSiteAdmin, "warn"
	default:
		return statusOwnersOpen, "warn"
	}
}

// collectivesShow lists the person's collectives and offers the two request
// forms. Only the fixed text a known ?msg= code stands for is shown.
func (a *App) collectivesShow(c *gin.Context) {
	u, ok := a.collectivesUser(c)
	if !ok {
		return
	}
	a.collectivesRender(c, u, http.StatusOK, nil, nil, collectivesNotices[c.Query("msg")], "")
}

// collectivesJoin asks to join an approved collective. The form carries the id
// the picker filled in: no id is no pick, never a new collective.
func (a *App) collectivesJoin(c *gin.Context) {
	a.collectivesSubmit(c, "request join", "/coletivos?msg=pedido-enviado",
		func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
			id := strings.TrimSpace(form.Get("collective_id"))
			if id == "" {
				return domain.FieldErrors{"collective": collectivesMsgPick}
			}
			return approvals.RequestJoin(ctx, a.Store, a.Cfg.Origin, u, id, domain.Role(form.Get("role")), now)
		})
}

// collectivesNew asks the site admin to create a collective or produtora.
func (a *App) collectivesNew(c *gin.Context) {
	a.collectivesSubmit(c, "request collective", "/coletivos?msg=coletivo-pedido",
		func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
			return approvals.RequestCollective(ctx, a.Store, a.Cfg.Origin, u, form.Get("name"), domain.CollectiveKind(form.Get("kind")), now)
		})
}

// collectivesSubmit is the flow both request forms share. Every post counts
// against the person's budget (Limits.Requests, one for both forms) before it is
// read, and a refused or failed post answers with the page, so the person sees
// their list and what they typed. On success it redirects to to. The log names
// the person and the error, never what was typed.
func (a *App) collectivesSubmit(c *gin.Context, logMsg, to string, do func(context.Context, domain.User, url.Values, time.Time) error) {
	u, ok := a.collectivesUser(c)
	if !ok {
		return
	}
	now := a.Now()
	allowed := a.Limits.Requests.Allow("request:"+u.ID, now)
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	if !allowed {
		a.collectivesRender(c, u, http.StatusTooManyRequests, form, nil, "", msgTooMany)
		return
	}
	var fieldErrs domain.FieldErrors
	switch err := do(c.Request.Context(), u, form, now); {
	case err == nil:
		a.redirect(c, to)
	case errors.As(err, &fieldErrs):
		a.collectivesRender(c, u, http.StatusUnprocessableEntity, form, fieldErrs, "", "")
	case errors.Is(err, approvals.ErrLocked):
		a.collectivesRender(c, u, http.StatusConflict, form, nil, "", collectivesMsgLocked)
	case errors.Is(err, approvals.ErrNotAllowed):
		c.String(http.StatusForbidden, "acesso negado")
	default:
		a.Log.Error(logMsg, "user", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	}
}
