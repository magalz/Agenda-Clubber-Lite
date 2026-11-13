// Command agenda runs the Agenda Clubber Lite service.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // IANA zones for APP_TZ in a container without /usr/share/zoneinfo

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongostore"
	"github.com/magalz/agenda-clubber-lite/internal/web"
)

const (
	shutdownTimeout = 15 * time.Second

	usage = `usage: agenda [command]

commands:
  serve           run the web server (default)
  migrate         create or update the database schema (safe to run again)
  healthcheck     probe the running server's /healthz and exit 0 if it answers 200
  create-admin    create an active admin: --name, --email and --phone, password on stdin
  seed-dev        fill a dev database with sample accounts and collectives (APP_ENV=dev, SEED_PASSWORD)
`
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run dispatches the subcommand and returns the process exit code. Each
// subcommand reads only the configuration it uses; only create-admin reads
// stdin (the password).
func run(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		args = []string{"serve"}
	}
	switch {
	case args[0] == "serve" && len(args) == 1:
		return serve(ctx, getenv, stdout, stderr)
	case args[0] == "migrate" && len(args) == 1:
		return migrate(ctx, getenv, stdout, stderr)
	case args[0] == "healthcheck" && len(args) == 1:
		return healthcheck(ctx, getenv, stderr)
	case args[0] == "create-admin":
		return createAdmin(ctx, args[1:], getenv, stdin, stdout, stderr)
	case args[0] == "seed-dev" && len(args) == 1:
		return seedDev(ctx, getenv, stdout, stderr)
	default:
		fmt.Fprint(stderr, usage)
		return 2
	}
}

// openTimeout bounds opening the database and creating its schema; a variable
// so a test can shorten it.
var openTimeout = 30 * time.Second

// healthcheckTimeout bounds one probe; a variable so a test can shorten it.
var healthcheckTimeout = 3 * time.Second

// healthzURL returns the URL a probe inside the container uses to reach the
// server listening on addr (APP_ADDR, config.DefaultAddr when empty): always
// loopback, whatever host the server binds. config.Load accepts port 0 (an
// ephemeral port for a server) but a probe cannot dial it, so it is refused.
func healthzURL(addr string) (string, error) {
	if addr == "" {
		addr = config.DefaultAddr
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", errors.New("APP_ADDR: must be host:port")
	}
	if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
		return "", errors.New("APP_ADDR: port must be a number from 1 to 65535")
	}
	return "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz", nil
}

// healthcheck probes the running server for the container's HEALTHCHECK. It
// reads APP_ADDR only: the full configuration (SMTP, origin) has no bearing on
// whether the server answers, and validating it here would turn a config
// mistake into a second, misleading failure.
func healthcheck(ctx context.Context, getenv func(string) string, stderr io.Writer) int {
	url, err := healthzURL(strings.TrimSpace(getenv("APP_ADDR")))
	if err != nil {
		fmt.Fprintf(stderr, "agenda: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		fmt.Fprintf(stderr, "agenda: %v\n", err)
		return 1
	}
	// A redirect is not the plain 200 the app answers: report it, do not follow.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "agenda: healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(stderr, "agenda: healthcheck: %s answered %s\n", url, resp.Status)
		return 1
	}
	return 0
}

// openStore opens the MongoDB store and makes sure its schema is in place, both
// within openTimeout. Its errors are fixed texts, never the driver's: those may
// name the server, and the URI may hold a password. The text of mongostore.Open
// is one of them (it says what failed and holds nothing of the URI), but only its
// text is passed on, not the error: %s, never %w.
func openStore(ctx context.Context, cfg config.MongoConfig) (*mongostore.Store, error) {
	ctx, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	store, err := mongostore.Open(ctx, cfg.URI, cfg.DB)
	if err != nil {
		return nil, fmt.Errorf("cannot open the database: %s", err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		_ = closeStore(store)
		return nil, errors.New("cannot create the database schema")
	}
	return store, nil
}

// closeStore releases the connections on a context of its own, bounded by
// shutdownTimeout: the one serve and migrate ran with may be canceled already.
func closeStore(store *mongostore.Store) error {
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return store.Close(ctx)
}

// migrate creates or updates the database schema and exits. It reads the MongoDB
// settings and no other variable, and is safe to run again.
func migrate(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) int {
	cfg, err := config.LoadMongo(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "agenda: %v\n", err)
		return 1
	}
	store, err := openStore(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "agenda: migrate: %v\n", err)
		return 1
	}
	if err := closeStore(store); err != nil {
		fmt.Fprintln(stderr, "agenda: migrate: closing the database failed")
	}
	fmt.Fprintln(stdout, "schema ready")
	return 0
}

// serve opens the database, runs the HTTP server until ctx is canceled and shuts
// both down. It validates the whole configuration first.
func serve(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "agenda: %v\n", err)
		return 1
	}
	log := newLogger(cfg, stdout)

	store, err := openStore(ctx, cfg.Mongo)
	if err != nil {
		log.Error("database failed", "err", err)
		return 1
	}
	// Deferred, so it runs after the server has shut down, and also when the
	// server failed to listen.
	defer func() {
		if err := closeStore(store); err != nil {
			log.Error("closing the database failed")
		}
	}()
	log.Info("database ready", "db", cfg.Mongo.DB)

	// Build the shared dummy hash now, so the first login for an unknown email
	// does not take longer than the ones after it.
	auth.DummyVerify("")

	srv := newServer(cfg, log, store)

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

// cleanupInterval is how often serve removes expired sessions and tokens; a
// variable so a test can shorten it.
var cleanupInterval = time.Hour

// newSender is a stub until the green commit.
func newSender(cfg config.Config, log *slog.Logger) (mail.Sender, error) { return nil, nil }

// startJobs is a stub until the green commit.
func startJobs(ctx context.Context, log *slog.Logger, repos domain.Repos, sender mail.Sender) (stop func(timeout time.Duration) bool) {
	return func(time.Duration) bool { return true }
}

// newLogger logs JSON in production and text in development.
func newLogger(cfg config.Config, w io.Writer) *slog.Logger {
	if cfg.IsDev() {
		return slog.New(slog.NewTextHandler(w, nil))
	}
	return slog.New(slog.NewJSONHandler(w, nil))
}

// newServer sets the Gin mode and builds the http.Server with its timeouts. The
// web app reads and writes through store.
func newServer(cfg config.Config, log *slog.Logger, store domain.Store) *http.Server {
	if cfg.IsDev() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           web.NewApp(cfg, log, store).Router(),
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelError),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}
