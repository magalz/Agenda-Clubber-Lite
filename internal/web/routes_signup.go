package web

import "github.com/gin-gonic/gin"

// passwordRule is one line of a password checklist: the rule's key (the
// data-rule attribute the page script ticks) and its label.
type passwordRule struct{ Rule, Label string }

// passwordChecklist stub: RED.
func passwordChecklist(withHistory bool) []passwordRule { return nil }

// signupRoutes registers the signup pages and the collective suggestions (/cadastro, /cadastro/enviado, /coletivos/sugestoes).
// public needs no session; authed requires one (see Router).
func (a *App) signupRoutes(public, authed *gin.RouterGroup) {}
