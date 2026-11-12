// Command agenda runs the Agenda Clubber Lite service.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // IANA zones for APP_TZ in a container without /usr/share/zoneinfo

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/web"
)

const (
	shutdownTimeout = 15 * time.Second

	usage = `usage: agenda [command]

commands:
  serve    run the web server (default)
`
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches the subcommand and returns the process exit code. Later
// subcommands (migrate, backup, healthcheck, create-admin, seed-dev) join the
// switch.
func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"serve"}
	}
	switch {
	case args[0] == "serve" && len(args) == 1:
		return serve(ctx, getenv, stdout, stderr)
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
}

// serve runs the HTTP server until ctx is canceled, then shuts it down.
func serve(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "agenda: %v\n", err)
		return 1
	}
	log := newLogger(cfg, stdout)
	srv := newServer(cfg, log)

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("starting", "addr", cfg.Addr, "env", cfg.Env, "origin", cfg.Origin)

	select {
	case err := <-errCh: // only a failure: nothing has called Shutdown yet
		log.Error("listen failed", "err", err)
		return 1
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown failed", "err", err)
		return 1
	}
	return 0
}

// newLogger logs JSON in production and text in development.
func newLogger(cfg config.Config, w io.Writer) *slog.Logger {
	if cfg.IsDev() {
		return slog.New(slog.NewTextHandler(w, nil))
	}
	return slog.New(slog.NewJSONHandler(w, nil))
}

// newServer sets the Gin mode and builds the http.Server with its timeouts.
func newServer(cfg config.Config, log *slog.Logger) *http.Server {
	if cfg.IsDev() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           web.NewApp(cfg, log).Router(),
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
