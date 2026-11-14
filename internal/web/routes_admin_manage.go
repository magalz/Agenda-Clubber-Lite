package web

import (
	"github.com/gin-gonic/gin"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// adminManageRoutes registers the site admin's management pages: accounts
// (/admin/contas), collectives and their blocks (/admin/coletivos) and the
// decision history (/admin/historico). admin is the /admin group.
func (a *App) adminManageRoutes(admin *gin.RouterGroup) {}

// auditActionLabel is the pt-BR label of an audit action; a value this code does
// not know shows as it is.
func auditActionLabel(domain.AuditAction) string { return "" }
