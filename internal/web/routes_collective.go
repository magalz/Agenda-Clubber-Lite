package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// collectiveRoutes registers a collective's management page (/coletivo/:id),
// open only to its approved Administradores, and its actions.
func (a *App) collectiveRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/coletivo/:id", collectiveStub)
	for _, p := range []string{"/pedido/aprovar", "/pedido/recusar", "/membro/promover", "/membro/rebaixar", "/nome"} {
		authed.POST("/coletivo/:id"+p, collectiveStub)
	}
}

// collectiveStub answers every route with 501 until the page exists.
func collectiveStub(c *gin.Context) { c.Status(http.StatusNotImplemented) }
