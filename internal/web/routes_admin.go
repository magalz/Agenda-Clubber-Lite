package web

import "github.com/gin-gonic/gin"

// adminRoutes registers the site admin's approval queue (/admin) and its
// decisions. admin is the /admin group, already behind requireAuth,
// requireFreshPassword and requireAdmin.
func (a *App) adminRoutes(admin *gin.RouterGroup) {}
