// Command agenda runs the Agenda Clubber Lite service.
package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	_ "time/tzdata" // IANA zones for APP_TZ in a container without /usr/share/zoneinfo

	"github.com/magalz/agenda-clubber-lite/internal/config"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	return 0
}

func newLogger(cfg config.Config, w io.Writer) *slog.Logger { return slog.Default() }

func newServer(cfg config.Config, log *slog.Logger) *http.Server { return &http.Server{} }
