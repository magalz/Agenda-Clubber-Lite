package web

import "github.com/gin-gonic/gin"

// profileRoutes serves Editar Perfil: the account's own name, phone and
// WhatsApp flag, the email change with its confirmation, and the link to the
// password change. The confirmation link (/confirmar-email) is public — it is
// clicked from an email, maybe without a session.
func (a *App) profileRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/perfil", a.profileShow)
	authed.POST("/perfil", a.profileSubmit)
	authed.POST("/perfil/email", a.profileEmailSubmit)
	public.GET("/confirmar-email", a.confirmEmail)
}

// RED: the handlers compile but do nothing yet, so the tests fail on their
// assertions rather than on a missing route.
func (a *App) profileShow(c *gin.Context)        {}
func (a *App) profileSubmit(c *gin.Context)      {}
func (a *App) profileEmailSubmit(c *gin.Context) {}
func (a *App) confirmEmail(c *gin.Context)       {}

// The notices the profile pages answer with in ?msg=. The texts live in the
// perfilNotices table, which the profile pages task adds beside its handlers.
const (
	msgPerfilSaved  = "perfil-salvo"
	msgEmailPending = "email-pendente"
	msgEmailChanged = "email-confirmado"
	msgEmailInvalid = "email-invalido"
	msgEmailInUse   = "email-em-uso"
)
