package web

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/accounts"
	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The states of the change password page.
const (
	changeStateForm = "form" // the form (it may carry an alert above it)
	changeStateDone = "done" // the password was changed
)

// changeMsgDone is the status of the done page.
const changeMsgDone = "senha alterada. as outras sessões foram encerradas."

// changeMsgLocked is the alert shown when the account is locked by wrong current passwords.
const changeMsgLocked = "muitas tentativas de senha incorreta. a conta está bloqueada por 15 minutos."

// The verdicts of the history item, the State of the password-history partial.
const (
	historyPending = "pending" // not known to be met: nothing checked, or one of the last passwords
	historyOK      = "ok"      // not one of the last passwords
)

// changeView is the Data of app/senha-trocar. Expired says the password ran
// out, Email is the account's address (the checklist needs it), Message is the
// alert above the form (rate limit) or the status of the done page, and
// Checklist is the password rules list, history included.
type changeView struct {
	State     string
	Message   string
	Expired   bool
	Email     string
	Checklist []passwordRule
}

// passwordRoutes registers the password change for a signed-in person
// (/senha/trocar) and the live check of its history rule (/senha/verificar).
// Both are in authed, and the access rules let an expired password reach them.
// public needs no session; authed requires one (see Router).
func (a *App) passwordRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/senha/trocar", a.changeShow)
	authed.POST("/senha/trocar", a.changeSubmit)
	authed.POST("/senha/verificar", a.changeCheck)
}

// changePageUser is the signed-in person as the change pages show them: without
// the expiry warning. The banner links here, so on this page it would only point
// at itself, and after the change it would still warn, because the session's user
// is the one loaded before it.
func (a *App) changePageUser(c *gin.Context) *ViewUser {
	v := a.viewUser(c)
	v.PasswordDaysLeft = 0
	return v
}

// changeFormData is the change form page. message (empty for none) is an
// alert above the form, and no password is ever written back.
func (a *App) changeFormData(c *gin.Context, u domain.User, message string) PageData {
	return PageData{Title: "Trocar senha", Nav: "perfil", User: a.changePageUser(c), Data: changeView{
		State:     changeStateForm,
		Message:   message,
		Expired:   auth.PasswordExpired(u.PasswordChangedAt, a.Now()),
		Email:     u.Email,
		Checklist: passwordChecklist(true),
	}}
}

func (a *App) changeShow(c *gin.Context) {
	u, ok := a.signedInUser(c, "change password")
	if !ok {
		return
	}
	a.page(c, http.StatusOK, "app/senha-trocar", a.changeFormData(c, u, ""))
}

// changeSubmit changes the password. Every post counts against the account's
// budget (Limits.PasswordCheck) before anything is hashed, so a session in
// someone else's hands cannot use the form to guess the current password
// quickly. The person's other sessions end and this one stays, which is why the
// answer sets no cookie. No password reaches the page or the logs.
func (a *App) changeSubmit(c *gin.Context) {
	u, ok := a.signedInUser(c, "change password")
	if !ok {
		return
	}
	allowed := a.Limits.PasswordCheck.Allow("change:"+u.ID, a.Now())
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	if !allowed {
		a.page(c, http.StatusTooManyRequests, "app/senha-trocar", a.changeFormData(c, u, msgTooMany))
		return
	}
	err = accounts.ChangePassword(c.Request.Context(), a.Store, u.ID, a.sessionToken(c),
		form.Get("current_password"), form.Get("password"), form.Get("password_confirm"), a.Now())
	var fieldErrs domain.FieldErrors
	switch {
	case err == nil:
		a.page(c, http.StatusOK, "app/senha-trocar", PageData{Title: "Trocar senha", Nav: "perfil", User: a.changePageUser(c),
			Data: changeView{State: changeStateDone, Message: changeMsgDone}})
	case errors.Is(err, accounts.ErrLoginLocked):
		a.page(c, http.StatusTooManyRequests, "app/senha-trocar", a.changeFormData(c, u, changeMsgLocked))
	case errors.As(err, &fieldErrs):
		a.formPage(c, "app/senha-trocar", a.changeFormData(c, u, ""), fieldErrs)
	default:
		a.Log.Error("change password", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	}
}

// changeCheck answers the live check of the history rule with the one history
// item (the password-history partial), which htmx swaps into the checklist.
func (a *App) changeCheck(c *gin.Context) {
	u, ok := a.signedInUser(c, "change password")
	if !ok {
		return
	}
	if !a.Limits.PasswordCheck.Allow("check:"+u.ID, a.Now()) {
		c.Status(http.StatusTooManyRequests)
		return
	}
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	historyOKFlag, err := accounts.CheckPasswordHistory(c.Request.Context(), a.Store, u.ID, form.Get("current_password"), form.Get("password"), a.Now())
	if err != nil {
		a.Log.Error("check password history", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	state := historyPending
	if historyOKFlag {
		state = historyOK
	}
	a.Render.Partial(c, http.StatusOK, "password-history", map[string]any{
		"State": state,
		"Label": auth.RuleLabels[auth.RuleHistory],
	})
}
