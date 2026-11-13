package web

import "github.com/gin-gonic/gin"

// signupRoutes registers the signup pages and the collective suggestions (/cadastro, /cadastro/enviado, /coletivos/sugestoes).
// public needs no session; authed requires one (see Router).
func (a *App) signupRoutes(public, authed *gin.RouterGroup) {}
