package web

import (
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// msgTooMany is the message of every rate-limited answer.
const msgTooMany = "muitas tentativas. espere alguns minutos e tente de novo."

// Stubs: the behavior lands in the next commit.

func (a *App) sameOrigin() gin.HandlerFunc           { return func(c *gin.Context) { c.Next() } }
func (a *App) loadSession() gin.HandlerFunc          { return func(c *gin.Context) { c.Next() } }
func (a *App) requireAuth() gin.HandlerFunc          { return func(c *gin.Context) { c.Next() } }
func (a *App) requireFreshPassword() gin.HandlerFunc { return func(c *gin.Context) { c.Next() } }
func (a *App) requireAdmin() gin.HandlerFunc         { return func(c *gin.Context) { c.Next() } }

func (a *App) currentUser(c *gin.Context) (domain.User, bool) { return domain.User{}, false }
func (a *App) sessionToken(c *gin.Context) string             { return "" }

func (a *App) signIn(c *gin.Context, userID string) error { return nil }
func (a *App) signOut(c *gin.Context) error               { return nil }

func safeNext(raw string) string { return raw }

func (a *App) redirect(c *gin.Context, to string) {}

func (a *App) parseForm(c *gin.Context) (url.Values, error) { return nil, nil }

func (a *App) viewUser(c *gin.Context) *ViewUser { return nil }

func (a *App) page(c *gin.Context, status int, name string, data PageData) {
	a.Render.Page(c, status, name, data)
}

func (a *App) formPage(c *gin.Context, name string, data PageData, errs domain.FieldErrors) {
	a.Render.Page(c, 200, name, data)
}
