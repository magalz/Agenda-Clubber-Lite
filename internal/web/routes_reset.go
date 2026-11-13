package web

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/accounts"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The states of the two password recovery pages.
const (
	resetStateForm    = "form"    // both pages: the form (the forgot page may carry an alert above it)
	resetStateSent    = "sent"    // forgot page: a link was asked for
	resetStateDone    = "done"    // reset page: the password was changed
	resetStateInvalid = "invalid" // reset page: the link does not work
)

// Texts of the recovery pages (the rate-limit one is msgTooMany).
const (
	resetMsgForgotSent = "se houver uma conta ativa com este e-mail, enviamos um link para criar uma nova senha. ele vale por 1 hora."
	resetMsgInvalid    = "este link expirou ou já foi usado."
	resetMsgDone       = "senha alterada. entre com a nova senha."
)

// resetView is the Data of auth/senha-esqueci and auth/senha-redefinir. Token
// is the link's token, kept in the reset form's hidden field; Message is the
// line shown as an alert (forgot form, invalid link) or as a status (sent,
// done); Checklist is the password rules list of the reset form.
type resetView struct {
	State     string
	Token     string
	Message   string
	Checklist []passwordRule
}

// resetRoutes registers password recovery (/senha/esqueci, /senha/redefinir).
// public needs no session; authed requires one (see Router).
func (a *App) resetRoutes(public, authed *gin.RouterGroup) {
	public.GET("/senha/esqueci", a.resetForgotShow)
	public.POST("/senha/esqueci", a.resetForgotSubmit)
	public.GET("/senha/redefinir", a.resetShow)
	public.POST("/senha/redefinir", a.resetSubmit)
}

// resetForgotRender shows the forgot page. form refills the e-mail field (nil
// for none).
func (a *App) resetForgotRender(c *gin.Context, status int, form url.Values, v resetView) {
	a.page(c, status, "auth/senha-esqueci", PageData{Title: "Esqueci a senha", Form: form, Data: v})
}

// resetFormRender shows the reset form with a 200, or with a 422 and the
// messages of errs. The token stays in the hidden field and no password is
// ever written back.
func (a *App) resetFormRender(c *gin.Context, token string, errs domain.FieldErrors) {
	data := PageData{Title: "Nova senha", Data: resetView{State: resetStateForm, Token: token, Checklist: passwordChecklist(true)}}
	if errs == nil {
		a.page(c, http.StatusOK, "auth/senha-redefinir", data)
		return
	}
	a.formPage(c, "auth/senha-redefinir", data, errs)
}

// resetStateRender shows the reset page in a state that has no form.
func (a *App) resetStateRender(c *gin.Context, status int, v resetView) {
	a.page(c, status, "auth/senha-redefinir", PageData{Title: "Nova senha", Data: v})
}

func (a *App) resetForgotShow(c *gin.Context) {
	a.resetForgotRender(c, http.StatusOK, nil, resetView{State: resetStateForm})
}

// resetForgotSubmit mails a reset link. Every post counts against the client
// address and, when it names an e-mail, against that e-mail too (from any
// address), so one person cannot be mailed in bulk. A refused address does not
// spend the e-mail's budget. The answer is the same page whether or not the
// e-mail belongs to an active account, so the form cannot be used to find out
// which addresses are registered.
func (a *App) resetForgotSubmit(c *gin.Context) {
	now := a.Now()
	allowed := a.Limits.EmailAction.Allow("forgot:"+c.ClientIP(), now)
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	email := form.Get("email")
	if allowed && strings.TrimSpace(email) != "" {
		allowed = a.Limits.EmailAction.Allow("forgot-email:"+signupEmailKey(email), now)
	}
	if !allowed {
		a.resetForgotRender(c, http.StatusTooManyRequests, form, resetView{State: resetStateForm, Message: msgTooMany})
		return
	}
	if err := accounts.RequestPasswordReset(c.Request.Context(), a.Store, a.Cfg.Origin, email, now); err != nil {
		// The address is not logged: it says who asked.
		a.Log.Error("request password reset", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	a.resetForgotRender(c, http.StatusOK, nil, resetView{State: resetStateSent, Message: resetMsgForgotSent})
}

// resetShow is where the e-mailed link lands. It only shows the form: a GET
// must change nothing, because mail scanners and link previews open links, and
// it never looks the token up, so a hit or a miss tells nothing. The POST
// under the form spends the token. With no token at all the link is as good as
// broken.
func (a *App) resetShow(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		a.resetStateRender(c, http.StatusBadRequest, resetView{State: resetStateInvalid, Message: resetMsgInvalid})
		return
	}
	a.resetFormRender(c, token, nil)
}

// resetSubmit sets the new password. The token and the password are not echoed
// anywhere, and the logs never see them. A done reset has ended every session
// of the account, so the request's own cookie is cleared too.
func (a *App) resetSubmit(c *gin.Context) {
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	token := form.Get("token")
	err = accounts.ResetPassword(c.Request.Context(), a.Store, token, form.Get("password"), form.Get("password_confirm"), a.Now())
	var fieldErrs domain.FieldErrors
	switch {
	case err == nil:
		a.setSessionCookie(c, "", -1)
		a.resetStateRender(c, http.StatusOK, resetView{State: resetStateDone, Message: resetMsgDone})
	case errors.Is(err, accounts.ErrResetInvalid):
		a.resetStateRender(c, http.StatusBadRequest, resetView{State: resetStateInvalid, Message: resetMsgInvalid})
	case errors.As(err, &fieldErrs):
		a.resetFormRender(c, token, fieldErrs)
	default:
		a.Log.Error("reset password", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	}
}
