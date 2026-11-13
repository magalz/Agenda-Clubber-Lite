package web

import "github.com/gin-gonic/gin"

// adminManageRoutes registers the site admin's management pages: accounts
// (/admin/contas), collectives and their blocks (/admin/coletivos) and the
// decision history (/admin/historico). admin is the /admin group.
func (a *App) adminManageRoutes(admin *gin.RouterGroup) {}
