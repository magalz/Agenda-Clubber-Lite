package web

import "github.com/gin-gonic/gin"

// collectiveRoutes registers a collective's management page (/coletivo/:id),
// open only to its approved Administradores, and its actions.
func (a *App) collectiveRoutes(public, authed *gin.RouterGroup) {}
