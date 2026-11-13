package web

import "github.com/gin-gonic/gin"

// collectivesRoutes registers Meus Coletivos (/coletivos) and its join and new
// collective requests. GET /coletivos/sugestoes (public) stays with the signup
// flow.
func (a *App) collectivesRoutes(public, authed *gin.RouterGroup) {}
