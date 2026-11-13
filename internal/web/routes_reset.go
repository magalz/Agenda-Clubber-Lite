package web

import "github.com/gin-gonic/gin"

// resetRoutes registers password recovery (/senha/esqueci, /senha/redefinir).
// public needs no session; authed requires one (see Router).
func (a *App) resetRoutes(public, authed *gin.RouterGroup) {}
