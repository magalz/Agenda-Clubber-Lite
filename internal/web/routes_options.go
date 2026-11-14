package web

import "github.com/gin-gonic/gin"

// optionsRoutes registers the <option> lists the dependent selects swap in: GET
// /opcoes/cidades (the cities of a state) and GET /opcoes/subestilos (the
// substyles of a style). Task T4.15 (ACL-86) writes them.
func (a *App) optionsRoutes(public, authed *gin.RouterGroup) {}
