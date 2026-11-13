package web

import "github.com/gin-gonic/gin"

// passwordRoutes registers the password change for a signed-in user (/senha/trocar, /senha/verificar).
// public needs no session; authed requires one (see Router).
func (a *App) passwordRoutes(public, authed *gin.RouterGroup) {}
