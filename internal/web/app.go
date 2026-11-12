// Package web holds the HTTP application: dependencies, router and middleware.
package web

import (
	"log/slog"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/config"
)

// App carries the dependencies shared by the handlers.
type App struct {
	Cfg config.Config
	Log *slog.Logger
}

// NewApp builds an App from a validated configuration.
func NewApp(cfg config.Config, log *slog.Logger) *App {
	return &App{Cfg: cfg, Log: log}
}

// Router builds the Gin engine.
func (a *App) Router() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	return r
}
