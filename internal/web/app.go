// Package web holds the HTTP application: dependencies, router and middleware.
package web

import (
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

// App carries the dependencies shared by the handlers.
type App struct {
	Cfg    config.Config
	Log    *slog.Logger
	Assets *Assets
	Render *Renderer
}

// NewApp builds an App from a validated configuration. It panics if the
// embedded static tree cannot be read: it is compiled into the binary, so a
// failure is a programming error, not a runtime condition.
func NewApp(cfg config.Config, log *slog.Logger) *App {
	static, err := fs.Sub(webfs.Static, "static")
	if err != nil {
		panic(fmt.Sprintf("web: embedded static tree: %v", err))
	}
	assets, err := NewAssets(static)
	if err != nil {
		panic(fmt.Sprintf("web: hashing embedded static files: %v", err))
	}
	return &App{Cfg: cfg, Log: log, Assets: assets}
}

// Router builds the Gin engine: middleware first, then the routes.
func (a *App) Router() *gin.Engine {
	r := gin.New()

	// gin.New trusts X-Forwarded-For from every peer. Trust only the
	// configured proxies, and only their CF-Connecting-IP header.
	if err := r.SetTrustedProxies(a.Cfg.TrustedProxyCIDRs); err != nil {
		panic(fmt.Sprintf("web: trusted proxies: %v", err)) // config.Load validated them
	}
	r.RemoteIPHeaders = []string{"CF-Connecting-IP"}

	// Gin answers a trailing-slash mismatch ("/healthz/") with a 301 before
	// any middleware runs, which would leave that response without the
	// security headers. Such a path is a plain 404 instead.
	r.RedirectTrailingSlash = false

	// The request log wraps recovery so a recovered panic is logged as a 500.
	// Later middleware (origin check, sessions) goes here, before the routes.
	r.Use(a.requestLog(), a.recovery(), securityHeaders(), limitBody(maxBodyBytes))

	r.GET("/healthz", healthz)
	static := a.Assets.Handler()
	r.GET("/static/*filepath", static)
	r.HEAD("/static/*filepath", static)
	// Later tasks register their routes here.

	return r
}

// healthz answers the liveness probe.
func healthz(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.String(http.StatusOK, "ok")
}

// requestLog logs one line per request: method, path (never the query
// string), status, duration and client IP. No headers, cookies or bodies.
func (a *App) requestLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		a.Log.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"ip", c.ClientIP(),
		)
	}
}

// recovery turns a handler panic into a logged 500 instead of a dropped
// connection.
func (a *App) recovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if v := recover(); v != nil {
				a.Log.Error("panic recovered", "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
				if !c.Writer.Written() {
					c.String(http.StatusInternalServerError, "erro interno")
				}
				c.Abort()
			}
		}()
		c.Next()
	}
}
