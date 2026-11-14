package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/approvals"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The ?msg= codes of /admin, one for each decision: the card a notice is about
// is gone from the queue by the time the notice shows.
const (
	adminCodePerson          = "pessoa-aprovada"
	adminCodeAccountApproved = "conta-aprovada"
	adminCodeAccountRejected = "conta-recusada"
	adminCodeCollApproved    = "coletivo-aprovado"
	adminCodeCollRejected    = "coletivo-recusado"
	adminCodeJoinApproved    = "pedido-aprovado"
	adminCodeJoinRejected    = "pedido-recusado"
)

// adminNotices maps the ?msg= codes of /admin to their fixed text: the URL never
// supplies text. A code that is not here shows nothing.
var adminNotices = map[string]string{
	adminCodePerson:          "tudo aprovado para esta pessoa.",
	adminCodeAccountApproved: "conta aprovada.",
	adminCodeAccountRejected: "cadastro recusado.",
	adminCodeCollApproved:    "coletivo aprovado.",
	adminCodeCollRejected:    "coletivo recusado.",
	adminCodeJoinApproved:    "pedido aprovado.",
	adminCodeJoinRejected:    "pedido recusado.",
}

// The pt-BR texts of a card and of its rows.
const (
	adminStatusPending = "aguardando aprovação"
	adminStatusActive  = "conta ativa"
	adminJoinOwnerDone = "um Administrador já aprovou"
	adminJoinOwnerOpen = "aguardando um Administrador"
)

// adminQueuePage is the Data of app/admin. Notice is the status above the cards
// (a decision was made), Alert the alert (a decision was refused); either is
// empty for none.
type adminQueuePage struct {
	Cards  []adminCard
	Notice string
	Alert  string
}

// adminCard is one person of the queue, ready for partials/admin-person.html.
// Registered is the sign-up date, formatted here (dd/mm/aaaa, APP_TZ). Pending is
// true while the account awaits the admin: such a card offers no approval of one
// request alone. HeadingID and ReasonID are ids unique in the page (the card's
// heading, and the reason field of its reject account form); they are counted,
// never taken from the store.
type adminCard struct {
	UserID     string
	Name       string
	Email      string
	Phone      string
	WhatsApp   bool
	Pending    bool
	Status     string
	Registered string
	HeadingID  string
	ReasonID   string
	Items      []adminItem
}

// adminItem is one request of a card. A new collective (NewCollective) is decided
// with /admin/coletivo/*, which names only the collective; a request to join one
// with /admin/pedido/*, which names the person too. ReasonID is the id of the
// row's reason field, unique in the page.
type adminItem struct {
	UserID        string
	CollectiveID  string
	Title         string
	Meta          string
	NewCollective bool
	ReasonID      string
}

// adminRoutes registers the site admin's approval queue (GET /admin) and the
// decisions it offers. admin is the /admin group, already behind requireAuth,
// requireFreshPassword and requireAdmin. Every id travels in a hidden field of its
// form, never in the path.
func (a *App) adminRoutes(admin *gin.RouterGroup) {
	admin.GET("", a.adminShow)
	admin.POST("/pessoa/aprovar-tudo", a.adminApprovePerson)
	admin.POST("/conta/aprovar", a.adminApproveAccount)
	admin.POST("/conta/recusar", a.adminRejectAccount)
	admin.POST("/coletivo/aprovar", a.adminApproveCollective)
	admin.POST("/coletivo/recusar", a.adminRejectCollective)
	admin.POST("/pedido/aprovar", a.adminApproveJoin)
	admin.POST("/pedido/recusar", a.adminRejectJoin)
}

// adminUser is the signed-in admin. requireAdmin guarantees there is one, so a
// miss is a bug: it is logged and answered with a 500.
func (a *App) adminUser(c *gin.Context) (domain.User, bool) {
	u, ok := a.currentUser(c)
	if !ok {
		a.Log.Error("admin: no signed-in user on an admin route")
		c.String(http.StatusInternalServerError, "erro interno")
	}
	return u, ok
}

// adminRender answers with the queue as it is now, plus what the answer carries (a
// notice or an alert). A queue that cannot be read is a logged 500.
func (a *App) adminRender(c *gin.Context, u domain.User, status int, notice, alert string) {
	queue, err := approvals.AdminQueue(c.Request.Context(), a.Store, u)
	if err != nil {
		a.Log.Error("admin: read the queue", "user", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	a.page(c, status, "app/admin", PageData{
		Title: "Admin", Nav: "admin",
		Data: adminQueuePage{Cards: a.adminCards(queue), Notice: notice, Alert: alert},
	})
}

// adminCards turns the queue into the cards of the page. A request is either a
// collective the person asked to create (its collective is still pending) or a
// request to join an approved one.
func (a *App) adminCards(queue []approvals.PersonCard) []adminCard {
	n := 0 // counts the ids that must be unique in the page
	next := func(prefix string) string {
		n++
		return prefix + "-" + strconv.Itoa(n)
	}
	cards := make([]adminCard, len(queue))
	for i, p := range queue {
		card := adminCard{
			UserID: p.User.ID, Name: p.User.Name, Email: p.User.Email,
			Phone: p.User.Phone, WhatsApp: p.User.PhoneIsWhatsApp,
			Pending: p.AccountPending(), Status: adminStatusActive,
			Registered: p.User.CreatedAt.In(a.Cfg.Location).Format("02/01/2006"),
			HeadingID:  next("pessoa"), ReasonID: next("motivo"),
			Items: make([]adminItem, len(p.Items)),
		}
		if card.Pending {
			card.Status = adminStatusPending
		}
		for j, v := range p.Items {
			item := adminItem{UserID: p.User.ID, CollectiveID: v.CollectiveID, ReasonID: next("motivo")}
			if v.CollectiveStatus == domain.CollectiveStatusPending {
				item.NewCollective = true
				item.Title = "coletivo novo: " + v.CollectiveName
				item.Meta = kindLabel(v.CollectiveKind) + " · " + roleLabelMaster
			} else {
				item.Title = v.CollectiveName + " · " + approvals.RoleLabel(v.Role)
				item.Meta = adminJoinOwnerOpen
				if v.OwnerApprovedAt != nil {
					item.Meta = adminJoinOwnerDone
				}
			}
			card.Items[j] = item
		}
		cards[i] = card
	}
	return cards
}

// adminShow lists the people who wait for the admin, and what they ask for. Only
// the fixed text a known ?msg= code stands for is shown.
func (a *App) adminShow(c *gin.Context) {
	u, ok := a.adminUser(c)
	if !ok {
		return
	}
	a.adminRender(c, u, http.StatusOK, adminNotices[c.Query("msg")], "")
}

// adminDecide is the flow every decision shares. It reads the form, runs do, and
// on success redirects to the queue with the notice code. A reason that is not
// plain text is a 422; any other refusal of internal/approvals (a second decision,
// an unknown id, one request of a pending person approved alone) answers with the
// fresh queue and the fixed text of the refusal. The log names the admin, the
// action and the error, never the form: its ids and its reason are what the admin
// typed.
func (a *App) adminDecide(c *gin.Context, action, code string, do func(ctx context.Context, actor domain.User, form url.Values, now time.Time) error) {
	u, ok := a.adminUser(c)
	if !ok {
		return
	}
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	var fieldErrs domain.FieldErrors
	switch err := do(c.Request.Context(), u, form, a.Now()); {
	case err == nil:
		a.redirect(c, "/admin?msg="+code)
	case errors.As(err, &fieldErrs):
		a.adminRender(c, u, http.StatusUnprocessableEntity, "", fieldErrs["reason"])
	default:
		status, msg, answered := approvalsAnswer(err)
		if !answered {
			a.Log.Error("admin: "+action, "user", u.ID, "err", err)
			c.String(http.StatusInternalServerError, "erro interno")
			return
		}
		a.adminRender(c, u, status, "", msg)
	}
}

// adminApprovePerson approves the account and every request of a person.
func (a *App) adminApprovePerson(c *gin.Context) {
	a.adminDecide(c, "approve person", adminCodePerson, func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
		return approvals.ApprovePerson(ctx, a.Store, a.Cfg.Origin, u, form.Get("user_id"), now)
	})
}

// adminApproveAccount approves only the account of a person.
func (a *App) adminApproveAccount(c *gin.Context) {
	a.adminDecide(c, "approve account", adminCodeAccountApproved, func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
		return approvals.ApproveAccount(ctx, a.Store, a.Cfg.Origin, u, form.Get("user_id"), now)
	})
}

// adminRejectAccount rejects a sign-up, with an optional reason.
func (a *App) adminRejectAccount(c *gin.Context) {
	a.adminDecide(c, "reject account", adminCodeAccountRejected, func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
		return approvals.RejectAccount(ctx, a.Store, a.Cfg.Origin, u, form.Get("user_id"), form.Get("reason"), now)
	})
}

// adminApproveCollective approves a collective an active person asked for.
func (a *App) adminApproveCollective(c *gin.Context) {
	a.adminDecide(c, "approve collective", adminCodeCollApproved, func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
		return approvals.ApproveCollective(ctx, a.Store, a.Cfg.Origin, u, form.Get("collective_id"), now)
	})
}

// adminRejectCollective rejects a collective someone asked for, with an optional reason.
func (a *App) adminRejectCollective(c *gin.Context) {
	a.adminDecide(c, "reject collective", adminCodeCollRejected, func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
		return approvals.RejectCollective(ctx, a.Store, a.Cfg.Origin, u, form.Get("collective_id"), form.Get("reason"), now)
	})
}

// adminApproveJoin gives the admin's side of a request to join a collective.
func (a *App) adminApproveJoin(c *gin.Context) {
	a.adminDecide(c, "approve join", adminCodeJoinApproved, func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
		return approvals.AdminApproveJoin(ctx, a.Store, a.Cfg.Origin, u, form.Get("user_id"), form.Get("collective_id"), now)
	})
}

// adminRejectJoin rejects a request to join a collective, with an optional reason.
func (a *App) adminRejectJoin(c *gin.Context) {
	a.adminDecide(c, "reject join", adminCodeJoinRejected, func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
		return approvals.RejectJoin(ctx, a.Store, a.Cfg.Origin, u, form.Get("user_id"), form.Get("collective_id"), form.Get("reason"), now)
	})
}
