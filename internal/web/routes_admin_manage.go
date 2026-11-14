package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/approvals"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The ?msg= codes of the management pages. /admin/contas knows the first two and
// /admin/coletivos the third; /admin/historico only reads, so it has none.
const (
	manageCodeBlocked   = "conta-bloqueada"
	manageCodeUnblocked = "conta-desbloqueada"
	manageCodeBlocks    = "bloqueios-salvos"
)

// The fixed text of each code, by page: the URL never supplies text. A code that
// a page does not list shows nothing.
var (
	manageAccountNotices = map[string]string{
		manageCodeBlocked:   "conta bloqueada.",
		manageCodeUnblocked: "conta desbloqueada.",
	}
	manageCollectiveNotices = map[string]string{
		manageCodeBlocks: "bloqueios salvos.",
	}
)

// manageHistoryLimit is how many audit entries /admin/historico shows, newest first.
const manageHistoryLimit = 200

// auditLabels is the pt-BR label of each audit action.
var auditLabels = map[domain.AuditAction]string{
	domain.AuditAccountApproved:     "conta aprovada",
	domain.AuditAccountRejected:     "cadastro recusado",
	domain.AuditAccountBlocked:      "conta bloqueada",
	domain.AuditAccountUnblocked:    "conta desbloqueada",
	domain.AuditCollectiveRequested: "coletivo pedido",
	domain.AuditCollectiveApproved:  "coletivo aprovado",
	domain.AuditCollectiveRejected:  "coletivo recusado",
	domain.AuditCollectiveRenamed:   "coletivo renomeado",
	domain.AuditCollectiveBlocks:    "bloqueios do coletivo",
	domain.AuditJoinRequested:       "pedido de entrada",
	domain.AuditJoinAdminApproved:   "entrada aprovada pelo admin",
	domain.AuditJoinOwnerApproved:   "entrada aprovada por administrador",
	domain.AuditJoinRejected:        "entrada recusada",
	domain.AuditMemberPromoted:      "promovido a administrador",
	domain.AuditMemberDemoted:       "rebaixado a membro",
}

// auditActionLabel is the pt-BR label of an audit action; a value this code does
// not know shows as it is.
func auditActionLabel(action domain.AuditAction) string {
	if label, ok := auditLabels[action]; ok {
		return label
	}
	return string(action)
}

// manageAccountsPage is the Data of app/admin-contas. Notice is the status above the
// lists (an action went through), Alert the alert (it was refused); either is
// empty for none.
type manageAccountsPage struct {
	Active, Blocked []manageAccountRow
	Notice, Alert   string
}

// manageAccountRow is one account. Meta is the e-mail and the phone. CanBlock is false
// on the admin's own row. ReasonID is the id of the reason field of the block
// form, unique in the page (counted, never taken from the store); it is empty on
// a row with no such form.
type manageAccountRow struct {
	UserID   string
	Name     string
	Meta     string
	Admin    bool
	CanBlock bool
	ReasonID string
}

// manageCollectivesPage is the Data of app/admin-coletivos, with the same Notice and
// Alert as manageAccountsPage.
type manageCollectivesPage struct {
	Items         []manageCollectiveRow
	Notice, Alert string
}

// manageCollectiveRow is one approved collective and its two blocks. Base is the
// start of the ids of its form (reason field and the two hints), unique in the
// page.
type manageCollectiveRow struct {
	ID            string
	Name          string
	Kind          string
	EventsBlocked bool
	Locked        bool
	Base          string
}

// manageHistoryPage is the Data of app/admin-historico.
type manageHistoryPage struct {
	Entries []manageHistoryRow
}

// manageHistoryRow is one audit entry. Header is the time (dd/mm/aaaa HH:MM, APP_TZ),
// who did it and who or what it was about; Detail and Reason are empty for none.
// It never carries an e-mail address: the entry does not store one.
type manageHistoryRow struct {
	Label  string
	Header string
	Detail string
	Reason string
}

// adminManageRoutes registers the site admin's management pages: accounts
// (/admin/contas), collectives and their blocks (/admin/coletivos) and the
// decision history (/admin/historico). admin is the /admin group, already behind
// requireAuth, requireFreshPassword and requireAdmin. Every id travels in a hidden
// field of its form, never in the path.
func (a *App) adminManageRoutes(admin *gin.RouterGroup) {
	admin.GET("/contas", a.adminAccounts)
	admin.POST("/conta/bloquear", a.adminBlockAccount)
	admin.POST("/conta/desbloquear", a.adminUnblockAccount)
	admin.GET("/coletivos", a.adminCollectives)
	admin.POST("/coletivo/bloqueios", a.adminSetBlocks)
	admin.GET("/historico", a.adminHistory)
}

// manageDecide is adminDecide for the management pages: it reads the form, runs
// do, and on success redirects to back with the notice code. A reason that is not
// plain text is a 422, and any other refusal of internal/approvals answers with
// the page as it is now (render) and the fixed text of the refusal. The log names
// the admin, the action and the error, never the form.
func (a *App) manageDecide(c *gin.Context, action, back, code string,
	render func(c *gin.Context, u domain.User, status int, notice, alert string),
	do func(ctx context.Context, actor domain.User, form url.Values, now time.Time) error) {
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
		a.redirect(c, back+"?msg="+code)
	case errors.As(err, &fieldErrs):
		render(c, u, http.StatusUnprocessableEntity, "", fieldErrs["reason"])
	default:
		status, msg, answered := approvalsAnswer(err)
		if !answered {
			a.Log.Error("admin: "+action, "user", u.ID, "err", err)
			c.String(http.StatusInternalServerError, "erro interno")
			return
		}
		render(c, u, status, "", msg)
	}
}

// adminAccounts lists the active and the blocked accounts. Only the fixed text a
// known ?msg= code stands for is shown.
func (a *App) adminAccounts(c *gin.Context) {
	u, ok := a.adminUser(c)
	if !ok {
		return
	}
	a.manageAccountsRender(c, u, http.StatusOK, manageAccountNotices[c.Query("msg")], "")
}

// manageAccountsRender answers with the accounts as they are now, plus a notice or an
// alert. Accounts that cannot be read are a logged 500.
func (a *App) manageAccountsRender(c *gin.Context, u domain.User, status int, notice, alert string) {
	ctx, users := c.Request.Context(), a.Store.Repos().Users
	active, err := users.ListByStatus(ctx, domain.UserStatusActive, domain.ListLimitMax)
	var blocked []domain.User
	if err == nil {
		blocked, err = users.ListByStatus(ctx, domain.UserStatusDisabled, domain.ListLimitMax)
	}
	if err != nil {
		a.Log.Error("admin: read the accounts", "user", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	n := 0 // counts the ids that must be unique in the page
	row := func(v domain.User, blockForm bool) manageAccountRow {
		r := manageAccountRow{UserID: v.ID, Name: v.Name, Meta: v.Email, Admin: v.IsAdmin}
		if v.Phone != "" {
			r.Meta += " · " + v.Phone
			if v.PhoneIsWhatsApp {
				r.Meta += " (WhatsApp)"
			}
		}
		if blockForm && v.ID != u.ID {
			n++
			r.CanBlock, r.ReasonID = true, "motivo-"+strconv.Itoa(n)
		}
		return r
	}
	data := manageAccountsPage{Notice: notice, Alert: alert}
	for _, v := range active {
		data.Active = append(data.Active, row(v, true))
	}
	for _, v := range blocked {
		data.Blocked = append(data.Blocked, row(v, false))
	}
	a.page(c, status, "app/admin-contas", PageData{Title: "Admin · Contas", Nav: "admin", Data: data})
}

// adminBlockAccount blocks an active account, with an optional reason: its
// sessions end and nothing is e-mailed.
func (a *App) adminBlockAccount(c *gin.Context) {
	a.manageDecide(c, "block account", "/admin/contas", manageCodeBlocked, a.manageAccountsRender,
		func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
			return approvals.BlockAccount(ctx, a.Store, u, form.Get("user_id"), form.Get("reason"), now)
		})
}

// adminUnblockAccount makes a blocked account active again.
func (a *App) adminUnblockAccount(c *gin.Context) {
	a.manageDecide(c, "unblock account", "/admin/contas", manageCodeUnblocked, a.manageAccountsRender,
		func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
			return approvals.UnblockAccount(ctx, a.Store, u, form.Get("user_id"), now)
		})
}

// adminCollectives lists the approved collectives with their blocks. Only the
// fixed text a known ?msg= code stands for is shown.
func (a *App) adminCollectives(c *gin.Context) {
	u, ok := a.adminUser(c)
	if !ok {
		return
	}
	a.manageCollectivesRender(c, u, http.StatusOK, manageCollectiveNotices[c.Query("msg")], "")
}

// manageCollectivesRender answers with the approved collectives as they are now, plus a
// notice or an alert. Collectives that cannot be read are a logged 500.
func (a *App) manageCollectivesRender(c *gin.Context, u domain.User, status int, notice, alert string) {
	list, err := a.Store.Repos().Collectives.ListByStatus(c.Request.Context(), domain.CollectiveStatusApproved, domain.ListLimitMax)
	if err != nil {
		a.Log.Error("admin: read the collectives", "user", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	data := manageCollectivesPage{Notice: notice, Alert: alert, Items: make([]manageCollectiveRow, len(list))}
	for i, v := range list {
		data.Items[i] = manageCollectiveRow{
			ID: v.ID, Name: v.Name, Kind: kindLabel(v.Kind),
			EventsBlocked: v.EventsBlocked, Locked: v.Locked, Base: "bloqueio-" + strconv.Itoa(i+1),
		}
	}
	a.page(c, status, "app/admin-coletivos", PageData{Title: "Admin · Coletivos", Nav: "admin", Data: data})
}

// adminSetBlocks sets the two blocks of an approved collective from the checkboxes
// of its form (a box that is not sent is off), with an optional reason.
func (a *App) adminSetBlocks(c *gin.Context) {
	a.manageDecide(c, "set collective blocks", "/admin/coletivos", manageCodeBlocks, a.manageCollectivesRender,
		func(ctx context.Context, u domain.User, form url.Values, now time.Time) error {
			return approvals.SetCollectiveBlocks(ctx, a.Store, u, form.Get("collective_id"),
				form.Get("events_blocked") == "1", form.Get("locked") == "1", form.Get("reason"), now)
		})
}

// adminHistory lists the latest audit entries, newest first.
func (a *App) adminHistory(c *gin.Context) {
	u, ok := a.adminUser(c)
	if !ok {
		return
	}
	entries, err := a.Store.Repos().AuditEntries.List(c.Request.Context(), manageHistoryLimit)
	if err != nil {
		a.Log.Error("admin: read the history", "user", u.ID, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	data := manageHistoryPage{Entries: make([]manageHistoryRow, len(entries))}
	for i, e := range entries {
		parts := []string{e.At.In(a.Cfg.Location).Format("02/01/2006 15:04")}
		if e.ActorName != "" {
			parts = append(parts, "por "+e.ActorName)
		}
		if e.UserName != "" {
			parts = append(parts, "pessoa: "+e.UserName)
		}
		if e.CollectiveName != "" {
			parts = append(parts, "coletivo: "+e.CollectiveName)
		}
		data.Entries[i] = manageHistoryRow{
			Label: auditActionLabel(e.Action), Header: strings.Join(parts, " · "),
			Detail: e.Detail, Reason: e.Reason,
		}
	}
	a.page(c, http.StatusOK, "app/admin-historico", PageData{Title: "Admin · Histórico", Nav: "admin", Data: data})
}
