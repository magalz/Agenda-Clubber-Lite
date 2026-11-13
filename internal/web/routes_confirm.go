package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/accounts"
)

// The three things the confirmation page can show.
const (
	confirmStateButton = "button" // a link was opened: the button that spends it
	confirmStateResend = "resend" // the link does not work (or the resend was refused): the form for a new one
	confirmStateSent   = "sent"   // a new link was asked for
)

// Texts of the confirmation pages (the rate-limit one is msgTooMany).
const (
	confirmMsgInvalid = "este link de confirmação expirou ou já foi usado."
	confirmMsgSent    = "se houver um cadastro aguardando confirmação para este e-mail, enviamos um novo link. ele vale por 24 horas."
)

// confirmView is the Data of auth/confirmar. Token is the link's token, put in
// the button's form; Message is the line the "button" and "resend" states show
// as an alert (a refused post) and the "sent" state as a status.
type confirmView struct {
	State   string
	Token   string
	Message string
}

// confirmRoutes registers email confirmation and the waiting page (/confirmar, /confirmar/reenviar, /aguardando).
// public needs no session; authed requires one (see Router).
func (a *App) confirmRoutes(public, authed *gin.RouterGroup) {
	public.GET("/confirmar", a.confirmShow)
	public.POST("/confirmar", a.confirmSubmit)
	public.POST("/confirmar/reenviar", a.confirmResend)
	public.GET("/aguardando", a.confirmWaiting)
}

// confirmRender shows the confirmation page in one of its states. form refills
// the resend form (nil for none).
func (a *App) confirmRender(c *gin.Context, status int, form url.Values, v confirmView) {
	a.page(c, status, "auth/confirmar", PageData{Title: "Confirmar e-mail", Form: form, Data: v})
}

// confirmShow is where the e-mailed link lands. It only shows a button: a GET
// must change nothing, because mail scanners and link previews open links, and
// it never looks the token up, so a hit or a miss tells nothing. The POST under
// the button spends the token. With no token at all the link is as good as
// broken.
func (a *App) confirmShow(c *gin.Context) {
	token := c.Query("token")
	if token == "" {
		a.confirmRender(c, http.StatusBadRequest, nil, confirmView{State: confirmStateResend, Message: confirmMsgInvalid})
		return
	}
	a.confirmRender(c, http.StatusOK, nil, confirmView{State: confirmStateButton, Token: token})
}

// confirmSubmit spends the link. The token goes back into the page only in the
// hidden field of a refused (429) post, so the person can retry later, and the
// logs never see it. Guessing a token costs the store a lookup, so the form is
// limited per client address, like login, before any store work.
func (a *App) confirmSubmit(c *gin.Context) {
	allowed := a.Limits.Login.Allow("confirm:"+c.ClientIP(), a.Now())
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	if !allowed {
		a.confirmRender(c, http.StatusTooManyRequests, nil, confirmView{State: confirmStateButton, Token: form.Get("token"), Message: msgTooMany})
		return
	}
	switch err := accounts.ConfirmEmail(c.Request.Context(), a.Store, a.Cfg.Origin, form.Get("token"), a.Now()); {
	case err == nil:
		a.redirect(c, "/aguardando")
	case errors.Is(err, accounts.ErrConfirmInvalid):
		a.confirmRender(c, http.StatusBadRequest, nil, confirmView{State: confirmStateResend, Message: confirmMsgInvalid})
	default:
		a.Log.Error("confirm email", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	}
}

// confirmResend mails a new link. The post is counted against the client address
// and the e-mail (see emailForm). The answer is the same page whether or not the
// e-mail has an account waiting for its link, so the form cannot be used to find
// out which addresses are registered.
func (a *App) confirmResend(c *gin.Context) {
	now := a.Now()
	form, allowed, err := a.emailForm(c, a.Limits.EmailAction, "resend", now)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	if !allowed {
		a.confirmRender(c, http.StatusTooManyRequests, form, confirmView{State: confirmStateResend, Message: msgTooMany})
		return
	}
	if err := accounts.ResendConfirmation(c.Request.Context(), a.Store, a.Cfg.Origin, form.Get("email"), now); err != nil {
		a.Log.Error("resend confirmation", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	a.confirmRender(c, http.StatusOK, nil, confirmView{State: confirmStateSent, Message: confirmMsgSent})
}

// confirmWaiting is where a confirmed account waits for the admin.
func (a *App) confirmWaiting(c *gin.Context) {
	a.page(c, http.StatusOK, "auth/aguardando", PageData{Title: "Cadastro em análise"})
}
