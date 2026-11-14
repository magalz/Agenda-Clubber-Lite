package web

import "github.com/gin-gonic/gin"

// dayRoutes registers the day list (Analisar Data): GET /eventos/dia, the page
// or, for HTMX, only the list. Task T4.14 (ACL-85) writes it.
func (a *App) dayRoutes(public, authed *gin.RouterGroup) {}
