package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/accounts"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The notice codes the profile pages answer with in ?msg=.
const (
	msgPerfilSaved  = "perfil-salvo"
	msgEmailPending = "email-pendente"
	msgEmailChanged = "email-confirmado"
	msgEmailInvalid = "email-invalido"
	msgEmailInUse   = "email-em-uso"
)

// perfilNotices maps the ?msg= codes of /perfil to their fixed texts: the URL
// never supplies text, and a code outside the table shows nothing.
var perfilNotices = map[string]string{
	msgPerfilSaved:  "perfil salvo.",
	msgEmailPending: "se o e-mail estiver disponível, enviamos um link de confirmação. ele vale por 24 horas.",
	msgEmailChanged: "e-mail confirmado. seu endereço foi atualizado.",
	msgEmailInvalid: "este link expirou ou já foi usado.",
	msgEmailInUse:   "este e-mail já está em uso por outra conta. peça a troca novamente.",
}

// profileView is the Data of app/perfil. Email is the account's current address,
// shown read-only; Notice is a status and Alert an error (the rate limit and the
// two failed confirmations). Both carry only text from perfilNotices or msgTooMany.
type profileView struct {
	Email  string
	Notice string
	Alert  string
}

// profileRoutes serves Editar Perfil: the account's own name, phone and
// WhatsApp flag, the email change with its confirmation, and the link to the
// password change. The confirmation link (/confirmar-email) is public — it is
// clicked from an email, maybe without a session.
func (a *App) profileRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/perfil", a.profileShow)
	authed.POST("/perfil", a.profileSubmit)
	authed.POST("/perfil/email", a.profileEmailSubmit)
	public.GET("/confirmar-email", a.confirmEmailShow)
	public.POST("/confirmar-email", a.confirmEmailSubmit)
}

// emailConfirmView is the Data of auth/confirmar-email: Token is the link's
// token, carried in the hidden field of the button's form, and Message is the
// alert above it when the post was refused as too frequent (the token stays so
// the same link can be tried again).
type emailConfirmView struct {
	Token   string
	Message string
}

// profileData is the form page: the account's own values in .Form, the current
// address read-only in Data, and the notice code mapped through the fixed table.
// The two failed-confirmation codes are errors, not a success status.
func (a *App) profileData(c *gin.Context, u domain.User, code, alert string) PageData {
	v := profileView{Email: u.Email, Notice: perfilNotices[code], Alert: alert}
	switch code {
	case msgEmailInvalid, msgEmailInUse:
		v.Alert, v.Notice = v.Notice, ""
	}
	whatsapp := ""
	if u.PhoneIsWhatsApp {
		whatsapp = "1"
	}
	return PageData{
		Title: "Editar Perfil", Nav: "perfil",
		Form: url.Values{
			"name":              {u.Name},
			"phone":             {u.Phone},
			"phone_is_whatsapp": {whatsapp},
		},
		Data: v,
	}
}

func (a *App) profileShow(c *gin.Context) {
	u, ok := a.signedInUser(c, "edit profile")
	if !ok {
		return
	}
	a.page(c, http.StatusOK, "app/perfil", a.profileData(c, u, c.Query("msg"), ""))
}

// profileSubmit saves the person's own name and phone. A refused form comes
// back to this page with what was typed and the service's field errors, as the
// other app forms do. A successful one uses the house redirect, so htmx gets a
// 204 with HX-Redirect and a plain form post gets a 303.
func (a *App) profileSubmit(c *gin.Context) {
	u, ok := a.signedInUser(c, "edit profile")
	if !ok {
		return
	}
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	err = accounts.UpdateProfile(c.Request.Context(), a.Store, u,
		form.Get("name"), form.Get("phone"), form.Get("phone_is_whatsapp") == "1", a.Now())
	var fieldErrs domain.FieldErrors
	switch {
	case err == nil:
		a.redirect(c, "/perfil?msg="+msgPerfilSaved)
	case errors.As(err, &fieldErrs):
		data := a.profileData(c, u, "", "")
		data.Form = form
		a.formPage(c, "app/perfil", data, fieldErrs)
	default:
		a.Log.Error("update profile", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	}
}

// profileEmailSubmit asks for a confirmation link. Like the other forms that
// mail a link, it counts the post against the client address and the address
// named in the form before doing any work. The answer is the same whether or
// not the new address is free: the service never tells the caller, and neither
// the address nor the token reaches the page, the URL or the log.
func (a *App) profileEmailSubmit(c *gin.Context) {
	u, ok := a.signedInUser(c, "change email")
	if !ok {
		return
	}
	now := a.Now()
	form, allowed, err := a.emailForm(c, a.Limits.EmailAction, a.Limits.EmailActionEmail, "perfil-email", now)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	alert := ""
	if !allowed {
		alert = msgTooMany
	}
	data := a.profileData(c, u, "", alert)
	data.Form.Set("email", form.Get("email"))
	if !allowed {
		a.page(c, http.StatusTooManyRequests, "app/perfil", data)
		return
	}
	err = accounts.RequestEmailChange(c.Request.Context(), a.Store, a.Cfg.Origin, u, form.Get("email"), now)
	var fieldErrs domain.FieldErrors
	switch {
	case err == nil:
		a.redirect(c, "/perfil?msg="+msgEmailPending)
	case errors.As(err, &fieldErrs):
		a.formPage(c, "app/perfil", data, fieldErrs)
	default:
		a.Log.Error("request email change", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
	}
}

// confirmEmailShow is where the mailed link lands. It only shows a button: a
// GET must change nothing, because mail scanners and link previews open links,
// and it never looks the token up, so a hit or a miss tells nothing. The POST
// under the button spends the token. With no token at all the link is as good
// as broken; the POST then answers with the invalid-link code.
func (a *App) confirmEmailShow(c *gin.Context) {
	a.confirmEmailRender(c, http.StatusOK, c.Query("token"), "")
}

// confirmEmailRender shows the button page. A refused post comes back here with
// a 429 and the token it sent, so the person can retry with the same link.
func (a *App) confirmEmailRender(c *gin.Context, status int, token, message string) {
	a.page(c, status, "auth/confirmar-email", PageData{
		Title: "Confirmar novo e-mail",
		Data:  emailConfirmView{Token: token, Message: message},
	})
}

// confirmEmailSubmit spends the token under the button and redirects with a
// notice code: the link is public, so an anonymous visitor lands on /perfil and
// the access rules send them on to the login from there. The token goes back
// into the page only in the hidden field of a refused (429) post, and never
// reaches the logs. Guessing a token costs the store a lookup, so the form is
// limited per client address, like login, before any store work.
func (a *App) confirmEmailSubmit(c *gin.Context) {
	allowed := a.Limits.Login.Allow("confirm:"+c.ClientIP(), a.Now())
	form, err := a.parseForm(c)
	if err != nil {
		c.String(http.StatusBadRequest, "requisição inválida")
		return
	}
	if !allowed {
		a.confirmEmailRender(c, http.StatusTooManyRequests, form.Get("token"), msgTooMany)
		return
	}
	_, err = accounts.ConfirmEmailChange(c.Request.Context(), a.Store, form.Get("token"), a.Now())
	code := msgEmailChanged
	switch {
	case err == nil:
	case errors.Is(err, accounts.ErrEmailChangeInvalid):
		code = msgEmailInvalid
	case errors.Is(err, domain.ErrEmailTaken):
		code = msgEmailInUse
	default:
		a.Log.Error("confirm email change", "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	a.redirect(c, "/perfil?msg="+code)
}
