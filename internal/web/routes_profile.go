package web

import "github.com/gin-gonic/gin"

// profileRoutes serves Editar Perfil: the account's own name, phone and
// WhatsApp flag, the email change with its confirmation, and the link to the
// password change. Router registers it; the handlers and the page are Phase 5
// work (ACL-106, which owns this file), so nothing is served until then.
func (a *App) profileRoutes(authed *gin.RouterGroup) {}

// The notices the profile pages answer with in ?msg=. The texts live in the
// perfilNotices table, which the profile pages task adds beside its handlers.
const (
	msgPerfilSaved  = "perfil-salvo"
	msgEmailPending = "email-pendente"
	msgEmailChanged = "email-confirmado"
	msgEmailInvalid = "email-invalido"
)
