package web

import "github.com/gin-gonic/gin"

// confirmRoutes registers email confirmation and the waiting page (/confirmar, /confirmar/reenviar, /aguardando).
// public needs no session; authed requires one (see Router).
func (a *App) confirmRoutes(public, authed *gin.RouterGroup) {}
