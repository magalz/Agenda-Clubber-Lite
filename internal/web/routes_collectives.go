package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// collectivesRoutes registers Meus Coletivos (/coletivos) and its join and new
// collective requests. GET /coletivos/sugestoes (public) stays with the signup
// flow.
func (a *App) collectivesRoutes(public, authed *gin.RouterGroup) {
	authed.GET("/coletivos", a.collectivesNotBuilt)
	authed.POST("/coletivos/entrar", a.collectivesNotBuilt)
	authed.POST("/coletivos/novo", a.collectivesNotBuilt)
}

func (a *App) collectivesNotBuilt(c *gin.Context) { c.Status(http.StatusNotImplemented) }
