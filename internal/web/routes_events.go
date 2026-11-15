package web

import "github.com/gin-gonic/gin"

// eventRoutes registers an event's detail (GET /eventos/:id, the page or, for
// HTMX, the sheet's content) and its deletion (POST /eventos/:id/excluir). Task
// T4.11 (ACL-82) writes them.
func (a *App) eventRoutes(public, authed *gin.RouterGroup) {}

// ticketHref is the ticket link of an event, or "" when it is not an http or
// https link with a host.
func ticketHref(raw string) string { return "" }
