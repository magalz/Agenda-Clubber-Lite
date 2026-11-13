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

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

// App carries the dependencies shared by the handlers.
type App struct {
	Cfg    config.Config
	Log    *slog.Logger
	Store  domain.Store
	Now    func() time.Time // UTC; tests set a fixed clock
	Assets *Assets
	Render *Renderer
	Limits Limits
}

// Limits are the per-flow rate limiters. They live in memory, so each process
// has its own count.
type Limits struct {
	Login         *auth.Limiter // 10 per minute per IP
	Signup        *auth.Limiter // 5 per hour per IP
	EmailAction   *auth.Limiter // 5 per hour per IP: resend confirmation, forgot password
	Suggestions   *auth.Limiter // 60 per minute per IP
	PasswordCheck *auth.Limiter // 30 per minute per user
}

// NewApp builds an App from a validated configuration. It panics if the
// embedded static or template tree cannot be read or parsed: both are compiled
// into the binary, so a failure is a programming error, not a runtime
// condition. It also panics for a Config without a Location (config.Load
// always sets one, so a hand-built Config must too) and for a nil store.
func NewApp(cfg config.Config, log *slog.Logger, store domain.Store) *App {
	if cfg.Location == nil {
		panic("web: Config.Location is nil; config.Load always sets it, so a hand-built Config must set it too")
	}
	if store == nil {
		panic("web: Store is nil; cmd/agenda wires the database in")
	}
	static, err := fs.Sub(webfs.Static, "static")
	if err != nil {
		panic(fmt.Sprintf("web: embedded static tree: %v", err))
	}
	assets, err := NewAssets(static)
	if err != nil {
		panic(fmt.Sprintf("web: hashing embedded static files: %v", err))
	}

	templates, err := fs.Sub(webfs.Templates, "templates")
	if err != nil {
		panic(fmt.Sprintf("web: embedded template tree: %v", err))
	}
	render, err := NewRenderer(templates, assets, cfg.Location, log)
	if err != nil {
		panic(fmt.Sprintf("web: embedded templates: %v", err))
	}
	return &App{
		Cfg:    cfg,
		Log:    log,
		Store:  store,
		Now:    func() time.Time { return time.Now().UTC() },
		Assets: assets,
		Render: render,
		Limits: Limits{
			Login:         auth.NewLimiter(10, time.Minute),
			Signup:        auth.NewLimiter(5, time.Hour),
			EmailAction:   auth.NewLimiter(5, time.Hour),
			Suggestions:   auth.NewLimiter(60, time.Minute),
			PasswordCheck: auth.NewLimiter(30, time.Minute),
		},
	}
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
	// The Origin check runs before any session is looked up.
	r.Use(a.requestLog(), a.recovery(), securityHeaders(), limitBody(maxBodyBytes), a.sameOrigin())

	// Liveness and static files never load a session.
	r.GET("/healthz", healthz)
	static := a.Assets.Handler()
	r.GET("/static/*filepath", static)
	r.HEAD("/static/*filepath", static)

	// Each flow registers its routes from its own routes_<flow>.go file, so
	// flows written in parallel never edit this function. public routes load the
	// session (the page may show who is signed in) but need none; authed routes
	// need a signed-in user whose password has not expired.
	public := r.Group("", a.loadSession())
	authed := public.Group("", a.requireAuth(), a.requireFreshPassword())
	authed.GET("/", a.home)
	a.signupRoutes(public, authed)
	a.loginRoutes(public, authed)
	a.confirmRoutes(public, authed)
	a.passwordRoutes(public, authed)
	a.resetRoutes(public, authed)

	return r
}

// home renders the calendar page. It is a placeholder until the calendar
// lands.
func (a *App) home(c *gin.Context) {
	a.page(c, http.StatusOK, "app/home", PageData{Title: "Calendário", Nav: "calendario"})
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
