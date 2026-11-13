package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/accounts"
)

// loginRoutes registers login and logout (/entrar, /sair).
// public needs no session; authed requires one (see Router).
func (a *App) loginRoutes(public, authed *gin.RouterGroup) {
	public.GET("/entrar", a.loginForm)
	public.POST("/entrar", a.loginSubmit)
	authed.POST("/sair", a.logout)
}

// loginView is the data of pages/auth/entrar.html.
type loginView struct {
	Next    string // where to go after the login, already through safeNext
	Message string // the one status line, or "" for none
	Resend  bool   // offer to send the confirmation e-mail again
	Contact bool   // the message ends with the contact address as a link
}

// loginFailure is what the login page says for one outcome of accounts.Login.
// The messages never say more than the outcome: only the right password gets
// as far as the state of the account.
type loginFailure struct {
	err     error
	message string
	resend  bool
	contact bool
}

var loginFailures = []loginFailure{
	{err: accounts.ErrLoginFailed, message: "e-mail ou senha incorretos."},
	{err: accounts.ErrLoginLocked, message: "muitas tentativas erradas. por segurança, a conta fica bloqueada por 15 minutos."},
	{err: accounts.ErrLoginPendingEmail, message: "confirme seu e-mail antes de entrar.", resend: true},
	{err: accounts.ErrLoginPendingApproval, message: "seu cadastro aguarda a aprovação do admin. você recebe um e-mail quando for aprovado."},
	{err: accounts.ErrLoginDisabled, message: "esta conta está desativada. fale com", contact: true},
}

// loginForm shows the form, or sends a signed-in user on.
func (a *App) loginForm(c *gin.Context) {
	next := safeNext(c.Query("next"))
	if _, ok := a.currentUser(c); ok {
		a.redirect(c, next)
		return
	}
	a.page(c, http.StatusOK, "auth/entrar", PageData{Title: "Entrar", Data: loginView{Next: next}})
}

// loginSubmit checks the credentials and starts the session. The rate limit
// comes first, before the body is read or the store is touched; the page it
// answers with cannot know where the person was going, so it sends them to the
// calendar.
func (a *App) loginSubmit(c *gin.Context) {
	if !a.Limits.Login.Allow("login:"+c.ClientIP(), a.Now()) {
		a.page(c, http.StatusTooManyRequests, "auth/entrar", PageData{Title: "Entrar", Data: loginView{Next: "/", Message: msgTooMany}})
		return
	}
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	next := safeNext(form.Get("next"))

	user, err := accounts.Login(c.Request.Context(), a.Store, form.Get("email"), form.Get("password"), a.Now())
	if err == nil {
		if err := a.signIn(c, user.ID); err != nil {
			a.Log.Error("login: start session", "user", user.ID, "err", err)
			c.String(http.StatusInternalServerError, "erro interno")
			return
		}
		a.redirect(c, next)
		return
	}
	for _, f := range loginFailures {
		if errors.Is(err, f.err) {
			// Only the e-mail goes back into the form, never the password.
			a.page(c, http.StatusUnauthorized, "auth/entrar", PageData{
				Title: "Entrar",
				Form:  url.Values{"email": {form.Get("email")}},
				Data:  loginView{Next: next, Message: f.message, Resend: f.resend, Contact: f.contact},
			})
			return
		}
	}
	// accounts.Login keeps the e-mail and the password out of its errors.
	a.Log.Error("login", "err", err)
	c.String(http.StatusInternalServerError, "erro interno")
}

// logout ends the session and goes back to the login page.
func (a *App) logout(c *gin.Context) {
	if err := a.signOut(c); err != nil {
		a.Log.Error("logout", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	a.redirect(c, "/entrar")
}
