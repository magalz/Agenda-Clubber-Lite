package web

import "github.com/gin-gonic/gin"

// eventFormRoutes registers the event form: GET and POST /eventos/novo, GET and
// POST /eventos/:id/editar. Task T4.12 (ACL-83) writes them.
func (a *App) eventFormRoutes(public, authed *gin.RouterGroup) {}
