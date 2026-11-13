package web

import "github.com/gin-gonic/gin"

// loginRoutes registers login and logout (/entrar, /sair).
// public needs no session; authed requires one (see Router).
func (a *App) loginRoutes(public, authed *gin.RouterGroup) {}
