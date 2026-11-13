package web

import (
	"errors"
	"net/http"
	"unicode/utf8"

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

// changeUser is the signed-in person. The authed group guarantees there is one,
// so a miss is a bug: it is logged and answered with a 500.
func (a *App) changeUser(c *gin.Context) (domain.User, bool) {
	u, ok := a.currentUser(c)
	if !ok {
		a.Log.Error("change password: no signed-in user on an authed route")
		c.String(http.StatusInternalServerError, "erro interno")
	}
	return u, ok
}

// changeFormData is the change form page. message (empty for none) is an
// alert above the form, and no password is ever written back.
func (a *App) changeFormData(u domain.User, message string) PageData {
	return PageData{Title: "Trocar senha", Nav: "perfil", Data: changeView{
		State:     changeStateForm,
		Message:   message,
		Expired:   auth.PasswordExpired(u.PasswordChangedAt, a.Now()),
		Email:     u.Email,
		Checklist: passwordChecklist(true),
	}}
}

func (a *App) changeShow(c *gin.Context) {
	u, ok := a.changeUser(c)
	if !ok {
		return
	}
	a.page(c, http.StatusOK, "app/senha-trocar", a.changeFormData(u, ""))
}

// changeSubmit changes the password. Every post counts against the account's
// budget (Limits.PasswordCheck) before anything is hashed, so a session in
// someone else's hands cannot use the form to guess the current password
// quickly. The person's other sessions end and this one stays, which is why the
// answer sets no cookie. No password reaches the page or the logs.
func (a *App) changeSubmit(c *gin.Context) {
	u, ok := a.changeUser(c)
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
		a.page(c, http.StatusTooManyRequests, "app/senha-trocar", a.changeFormData(u, msgTooMany))
		return
	}
	err = accounts.ChangePassword(c.Request.Context(), a.Store, u.ID, a.sessionToken(c),
		form.Get("current_password"), form.Get("password"), form.Get("password_confirm"), a.Now())
	var fieldErrs domain.FieldErrors
	switch {
	case err == nil:
		a.page(c, http.StatusOK, "app/senha-trocar", PageData{Title: "Trocar senha", Nav: "perfil",
			Data: changeView{State: changeStateDone, Message: changeMsgDone}})
	case errors.As(err, &fieldErrs):
		a.formPage(c, "app/senha-trocar", a.changeFormData(u, ""), fieldErrs)
	default:
		a.Log.Error("change password", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	}
}

// changeCheck answers the live check of the history rule with the one history
// item (the password-history partial), which htmx swaps into the checklist. It
// checks nothing but that rule: the other rules are the script's and the
// submit's. An empty or too long password is "pending" without a lookup, which
// would only cost argon2 work for an answer the submit gives anyway. A history
// that cannot be read is a 500, which htmx does not swap, never "ok". The
// budget is per account and apart from the form's, and a refused check is a
// 429 for the same reason: the item on the page stays as it was.
func (a *App) changeCheck(c *gin.Context) {
	u, ok := a.changeUser(c)
	if !ok {
		return
	}
	if !a.Limits.PasswordCheck.Allow("check:"+u.ID, a.Now()) {
		c.String(http.StatusTooManyRequests, msgTooMany)
		return
	}
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	state := historyPending
	if password := form.Get("password"); password != "" && utf8.RuneCountInString(password) <= auth.MaxLength {
		reused, err := accounts.PasswordReused(c.Request.Context(), a.Store, u.ID, password)
		if err != nil {
			a.Log.Error("check password history", "err", err)
			c.String(http.StatusInternalServerError, "erro interno")
			return
		}
		if !reused {
			state = historyOK
		}
	}
	a.Render.Partial(c, http.StatusOK, "password-history", map[string]any{
		"State": state,
		"Label": auth.RuleLabels[auth.RuleHistory],
	})
}
