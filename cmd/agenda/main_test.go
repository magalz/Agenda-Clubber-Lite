package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// noStore is a domain.Store for the tests that build a server and never reach
// the database.
type noStore struct{}

func (noStore) Repos() domain.Repos { return domain.Repos{} }
func (noStore) WithTx(context.Context, func(context.Context, domain.Repos) error) error {
	panic("main test: noStore.WithTx")
}
func (noStore) Ping(context.Context) error  { return nil }
func (noStore) Close(context.Context) error { return nil }

func getenvOf(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// freeAddr returns a loopback address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	return ln.Addr().String()
}

func TestRunUsageOnUnknownSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"bogus"}, {"-h"}, {"serve", "extra"}, {"healthcheck", "extra"}, {"migrate", "extra"},
		{"backup", "a.db"}, // a SQLite-era command, gone with the SQLite store
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), args, getenvOf(), &stdout, &stderr)
			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			for _, want := range []string{"usage", "serve", "migrate", "healthcheck"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr = %q, want a usage text mentioning %q", stderr.String(), want)
				}
			}
			if strings.Contains(stderr.String(), "backup") {
				t.Errorf("stderr = %q, want a usage text without backup", stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

// testMongoURI is a well-formed URI for tests that stop at the configuration:
// nothing connects to it.
const testMongoURI = "mongodb://localhost:27017/?directConnection=true"

// unreachableURI is a well-formed URI of a server nobody runs, with a password
// that must never reach any output.
const unreachableURI = "mongodb://dbuser:" + uriSecret + "@127.0.0.1:1/?directConnection=true"

const uriSecret = "uri-secret-7f3a"

// mongoEnv returns MONGO_URI and MONGO_DB for a run on a database of its own,
// which is dropped when the test ends. Without MONGO_TEST_URI the test is
// skipped, and fails in CI (see mongotest.URI).
func mongoEnv(t *testing.T) []string {
	t.Helper()
	uri := mongotest.URI(t)
	var suffix [13]byte
	_, _ = rand.Read(suffix[:])                          // never fails: see crypto/rand.Read
	db := "acltest_cmd_" + hex.EncodeToString(suffix[:]) // 12 + 26 = 38 characters, the most MONGO_DB takes
	mongotest.DropOnCleanup(t, uri, db)
	return []string{"MONGO_URI", uri, "MONGO_DB", db}
}

// collectionNames lists the collections of the database that mongoEnv named.
func collectionNames(t *testing.T, env []string) []string {
	t.Helper()
	get := getenvOf(env...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(get("MONGO_URI")))
	if err != nil {
		t.Fatal("connect to the test database") // the error may echo the URI
	}
	defer func() { _ = client.Disconnect(ctx) }()
	names, err := client.Database(get("MONGO_DB")).ListCollectionNames(ctx, bson.M{})
	if err != nil {
		t.Fatalf("list the collections: %v", err)
	}
	slices.Sort(names)
	return names
}

func TestRunServeWithoutOriginFails(t *testing.T) {
	for _, args := range [][]string{nil, {"serve"}} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), args, getenvOf("MONGO_URI", testMongoURI), &stdout, &stderr)
		if code != 1 {
			t.Errorf("run(%v) = %d, want 1", args, code)
		}
		if !strings.Contains(stderr.String(), "APP_ORIGIN") {
			t.Errorf("stderr = %q, want the config error naming APP_ORIGIN", stderr.String())
		}
	}
}

func TestRunServeWithoutMongoURIFails(t *testing.T) {
	addr := freeAddr(t)
	for _, args := range [][]string{nil, {"serve"}} {
		var stdout, stderr bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		code := run(ctx, args, getenvOf("APP_ORIGIN", "http://"+addr, "APP_ADDR", addr), &stdout, &stderr)
		cancel()
		if code != 1 {
			t.Errorf("run(%v) = %d, want 1", args, code)
		}
		if !strings.Contains(stderr.String(), "MONGO_URI") {
			t.Errorf("stderr = %q, want the config error naming MONGO_URI", stderr.String())
		}
	}
}

// A MONGO_URI that is rejected must not reach the output: it may hold the
// database password.
func TestRunServeRejectedMongoURIIsNotEchoed(t *testing.T) {
	addr := freeAddr(t)
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	env := getenvOf("APP_ORIGIN", "http://"+addr, "APP_ADDR", addr,
		"MONGO_URI", "postgres://dbuser:uri-secret@db.example:5432/agenda")
	if code := run(ctx, []string{"serve"}, env, &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "MONGO_URI") {
		t.Errorf("stderr = %q, want the config error naming MONGO_URI", stderr.String())
	}
	for _, out := range []string{stdout.String(), stderr.String()} {
		for _, leak := range []string{"uri-secret", "dbuser", "db.example"} {
			if strings.Contains(out, leak) {
				t.Errorf("output %q leaks %q", out, leak)
			}
		}
	}
}

func TestRunServeListenErrorExits1(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer busy.Close()

	var stdout, stderr bytes.Buffer
	env := getenvOf(append(mongoEnv(t), "APP_ORIGIN", "http://localhost", "APP_ADDR", busy.Addr().String())...)
	done := make(chan int, 1)
	go func() { done <- run(context.Background(), []string{"serve"}, env, &stdout, &stderr) }()
	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run did not return after a listen error")
	}
	if !strings.Contains(stdout.String(), "listen failed") {
		t.Errorf("stdout = %q, want the listen failure logged", stdout.String())
	}
}

// serve opens the database before it listens: a database that cannot be reached
// is a failed start, not a server that answers /healthz and fails on its first
// request. Neither the URI nor its password may reach the output.
func TestRunServeWithAnUnreachableDatabaseExits1(t *testing.T) {
	old := openTimeout
	openTimeout = 500 * time.Millisecond
	t.Cleanup(func() { openTimeout = old })

	addr := freeAddr(t)
	var stdout, stderr bytes.Buffer
	env := getenvOf("APP_ORIGIN", "http://"+addr, "APP_ADDR", addr, "MONGO_URI", unreachableURI)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if code := run(ctx, []string{"serve"}, env, &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("serve took %v to give up, want it bounded by openTimeout", took)
	}
	if !strings.Contains(stdout.String(), openHint) {
		t.Errorf("stdout = %q, want the log to say why: %q", stdout.String(), openHint)
	}
	wantNoSecrets(t, stdout.String(), stderr.String())
	if _, err := http.Get("http://" + addr + "/healthz"); err == nil {
		t.Error("the server answers although the database never opened")
	}
}

// wantNoSecrets fails when an output carries any part of unreachableURI.
func wantNoSecrets(t *testing.T, outputs ...string) {
	t.Helper()
	for _, out := range outputs {
		for _, leak := range []string{uriSecret, "dbuser", "127.0.0.1:1", "?directConnection"} {
			if strings.Contains(out, leak) {
				t.Errorf("output %q leaks %q", out, leak)
			}
		}
	}
}

func TestRunServesAndShutsDownGracefully(t *testing.T) {
	addr := freeAddr(t)
	var stdout, stderr bytes.Buffer
	dbEnv := mongoEnv(t)
	env := getenvOf(append(dbEnv, "APP_ORIGIN", "http://"+addr, "APP_ADDR", addr)...)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, env, &stdout, &stderr) }()

	var resp *http.Response
	var err error
	// The server listens after the database is open and its schema is in place.
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if resp, err = http.Get("http://" + addr + "/healthz"); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("server never answered: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Errorf("GET /healthz = %d %q, want 200 ok", resp.StatusCode, body)
	}
	if gin.Mode() != gin.ReleaseMode {
		t.Errorf("gin mode = %q, want release in prod", gin.Mode())
	}

	// The database reaches the web app: the calendar needs a session, so a
	// request without a cookie is sent to the login page.
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	home, err := noFollow.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	home.Body.Close()
	if home.StatusCode != http.StatusSeeOther || home.Header.Get("Location") != "/entrar" {
		t.Errorf("GET / without a session = %d Location %q, want 303 /entrar", home.StatusCode, home.Header.Get("Location"))
	}

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0 after a clean shutdown; stderr: %s", code, &stderr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after the context was canceled")
	}
	if _, err := http.Get("http://" + addr + "/healthz"); err == nil {
		t.Error("server still answers after shutdown")
	}

	// The database is logged by name only.
	if !strings.Contains(stdout.String(), "database ready") || !strings.Contains(stdout.String(), dbEnv[3]) {
		t.Errorf("stdout = %q, want %q with the database name", stdout.String(), "database ready")
	}
	if strings.Contains(stdout.String(), dbEnv[1]) {
		t.Errorf("stdout logs the MongoDB URI: %q", stdout.String())
	}
	if got := collectionNames(t, dbEnv); len(got) != 6 {
		t.Errorf("collections after serve = %v, want the six of the schema", got)
	}
}

// migrate creates the schema and is safe to run again. It reads the MongoDB
// settings and nothing else, so a variable that serve would refuse (here a bad
// SMTP port, an origin that is no URL and an unknown environment) does not stop
// it.
func TestRunMigrateCreatesTheSchemaAndRunsTwice(t *testing.T) {
	dbEnv := mongoEnv(t)
	env := getenvOf(append(dbEnv, "SMTP_PORT", "abc", "APP_ORIGIN", "not a url", "APP_ENV", "bogus")...)
	for i := 1; i <= 2; i++ {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), []string{"migrate"}, env, &stdout, &stderr); code != 0 {
			t.Fatalf("run %d: exit code = %d, want 0; stderr: %s", i, code, &stderr)
		}
		if stdout.String() != "schema ready\n" || stderr.Len() != 0 {
			t.Errorf("run %d: stdout = %q, stderr = %q; want %q and nothing on stderr", i, stdout.String(), stderr.String(), "schema ready\n")
		}
		want := []string{"collectives", "memberships", "outbox", "sessions", "tokens", "users"}
		if got := collectionNames(t, dbEnv); !slices.Equal(got, want) {
			t.Errorf("run %d: collections = %v, want %v", i, got, want)
		}
	}
}

func TestRunMigrateConfigErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		env  []string
		want string
	}{
		"no MONGO_URI":        {[]string{}, "MONGO_URI"},
		"not a mongodb URI":   {[]string{"MONGO_URI", "postgres://dbuser:" + uriSecret + "@db.example:5432/agenda"}, "MONGO_URI"},
		"no directConnection": {[]string{"MONGO_URI", "mongodb://dbuser:" + uriSecret + "@db.example:27017/agenda"}, "MONGO_URI"},
		"bad MONGO_DB":        {[]string{"MONGO_URI", testMongoURI, "MONGO_DB", "not a name!"}, "MONGO_DB"},
	} {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), []string{"migrate"}, getenvOf(tc.env...), &stdout, &stderr); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), tc.want) {
				t.Errorf("stderr = %q, want the config error naming %s", stderr.String(), tc.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			wantNoSecrets(t, stdout.String(), stderr.String())
			for _, leak := range []string{"db.example", "not a name!"} {
				if strings.Contains(stderr.String(), leak) {
					t.Errorf("stderr = %q leaks %q", stderr.String(), leak)
				}
			}
		})
	}
}

func TestRunMigrateWithAnUnreachableDatabaseExits1(t *testing.T) {
	old := openTimeout
	openTimeout = 500 * time.Millisecond
	t.Cleanup(func() { openTimeout = old })

	var stdout, stderr bytes.Buffer
	start := time.Now()
	if code := run(context.Background(), []string{"migrate"}, getenvOf("MONGO_URI", unreachableURI), &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("migrate took %v to give up, want it bounded by openTimeout", took)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "agenda: migrate: "+openHint) {
		t.Errorf("stdout = %q, stderr = %q; want nothing on stdout and %q on stderr", stdout.String(), stderr.String(), openHint)
	}
	wantNoSecrets(t, stdout.String(), stderr.String())
}

// openHint is what an operator reads when the server cannot be reached: the fixed
// text of mongostore.Open, with the step that failed.
const openHint = "cannot open the database: mongostore: cannot reach the MongoDB server"

// openStore passes on the text of Open, which is fixed and holds nothing of the
// URI, but not the error itself: an error that wraps another one could be
// unwrapped and printed with more than that.
func TestOpenStoreSaysWhyTheDatabaseDoesNotOpen(t *testing.T) {
	old := openTimeout
	openTimeout = 500 * time.Millisecond
	t.Cleanup(func() { openTimeout = old })

	_, err := openStore(context.Background(), config.MongoConfig{URI: unreachableURI, DB: "agenda"})
	if err == nil {
		t.Fatal("openStore succeeded on an unreachable server")
	}
	if err.Error() != openHint {
		t.Errorf("error = %q, want %q", err, openHint)
	}
	if inner := errors.Unwrap(err); inner != nil {
		t.Errorf("the error wraps another one, which may carry the URI: %v", inner)
	}
	wantNoSecrets(t, err.Error())
}

func TestHealthzURL(t *testing.T) {
	for addr, want := range map[string]string{
		"":                "http://127.0.0.1:8120/healthz", // APP_ADDR unset: the default
		":8120":           "http://127.0.0.1:8120/healthz",
		":9000":           "http://127.0.0.1:9000/healthz",
		"0.0.0.0:9001":    "http://127.0.0.1:9001/healthz", // the probe always dials loopback
		"[::]:9002":       "http://127.0.0.1:9002/healthz",
		"localhost:9003":  "http://127.0.0.1:9003/healthz",
		"127.0.0.1:65535": "http://127.0.0.1:65535/healthz",
	} {
		got, err := healthzURL(addr)
		if err != nil || got != want {
			t.Errorf("healthzURL(%q) = %q, %v; want %q", addr, got, err, want)
		}
	}
	for _, addr := range []string{"nonsense", "host:", ":abc", ":0", ":65536", ":-1", "8120"} {
		if got, err := healthzURL(addr); err == nil {
			t.Errorf("healthzURL(%q) = %q, want an error", addr, got)
		}
	}
	// The unset-APP_ADDR fallback is the one config.Load applies.
	unset, err1 := healthzURL("")
	dflt, err2 := healthzURL(config.DefaultAddr)
	if err1 != nil || err2 != nil || unset != dflt {
		t.Errorf("healthzURL(\"\") = %q, %v; healthzURL(config.DefaultAddr) = %q, %v; want the same URL", unset, err1, dflt, err2)
	}
}

// healthcheckAgainst runs the healthcheck subcommand with APP_ADDR pointing at
// addr and the rest of the environment deliberately unusable: the probe must
// not depend on the full configuration.
func healthcheckAgainst(addr string) (code int, stdout, stderr string) {
	var out, errb bytes.Buffer
	env := getenvOf("APP_ADDR", addr, "APP_ENV", "bogus", "SMTP_PORT", "not-a-port", "APP_ORIGIN", "not a url", "MONGO_URI", "not a uri")
	code = run(context.Background(), []string{"healthcheck"}, env, &out, &errb)
	return code, out.String(), errb.String()
}

func TestRunHealthcheckStatus(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   int
	}{
		{"200 is healthy", http.StatusOK, 0},
		{"500 is unhealthy", http.StatusInternalServerError, 1},
		{"503 is unhealthy", http.StatusServiceUnavailable, 1},
		{"a redirect is not followed", http.StatusFound, 1},
		{"204 is not the 200 the app answers", http.StatusNoContent, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotMethod, gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath = r.Method, r.URL.Path
				if tc.status == http.StatusFound {
					w.Header().Set("Location", "/elsewhere")
				}
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			code, stdout, stderr := healthcheckAgainst(srv.Listener.Addr().String())
			if code != tc.want {
				t.Errorf("exit code = %d, want %d; stderr: %s", code, tc.want, stderr)
			}
			if gotMethod != http.MethodGet || gotPath != "/healthz" {
				t.Errorf("probe sent %s %s, want GET /healthz", gotMethod, gotPath)
			}
			if tc.want == 1 && !strings.Contains(stderr, "agenda:") {
				t.Errorf("stderr = %q, want a one-line reason", stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want empty", stdout)
			}
		})
	}
}

func TestRunHealthcheckFailures(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		code, _, stderr := healthcheckAgainst(freeAddr(t))
		if code != 1 || !strings.Contains(stderr, "agenda:") {
			t.Errorf("exit code = %d, stderr = %q; want 1 and a reason", code, stderr)
		}
	})
	t.Run("bad APP_ADDR", func(t *testing.T) {
		for _, addr := range []string{"nonsense", ":abc", ":0"} {
			code, _, stderr := healthcheckAgainst(addr)
			if code != 1 || !strings.Contains(stderr, "APP_ADDR") {
				t.Errorf("APP_ADDR=%q: exit code = %d, stderr = %q; want 1 naming APP_ADDR", addr, code, stderr)
			}
		}
	})
	t.Run("an answer that never comes times out", func(t *testing.T) {
		old := healthcheckTimeout
		healthcheckTimeout = 100 * time.Millisecond
		t.Cleanup(func() { healthcheckTimeout = old })

		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
		defer srv.Close() // runs after close(release): defers are last in, first out
		defer close(release)

		done := make(chan int, 1)
		go func() { code, _, _ := healthcheckAgainst(srv.Listener.Addr().String()); done <- code }()
		select {
		case code := <-done:
			if code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("healthcheck outlived its timeout")
		}
	})
}

func TestNewServerSettings(t *testing.T) {
	gin.DefaultWriter = io.Discard // debug mode prints its route table
	t.Cleanup(func() { gin.DefaultWriter = os.Stdout; gin.SetMode(gin.TestMode) })

	srv := newServer(config.Config{Env: "prod", Addr: ":8120", Location: time.UTC}, slog.New(slog.NewJSONHandler(io.Discard, nil)), noStore{})
	if srv.Addr != ":8120" || srv.Handler == nil {
		t.Errorf("Addr = %q, Handler = %v", srv.Addr, srv.Handler)
	}
	if srv.ReadHeaderTimeout != 10*time.Second || srv.ReadTimeout != 30*time.Second ||
		srv.WriteTimeout != 30*time.Second || srv.IdleTimeout != 120*time.Second {
		t.Errorf("timeouts = header %v read %v write %v idle %v", srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
	}
	if gin.Mode() != gin.ReleaseMode {
		t.Errorf("prod gin mode = %q, want release", gin.Mode())
	}

	newServer(config.Config{Env: "dev", Addr: ":8120", Location: time.UTC}, slog.New(slog.NewJSONHandler(io.Discard, nil)), noStore{})
	if gin.Mode() != gin.DebugMode {
		t.Errorf("dev gin mode = %q, want debug", gin.Mode())
	}
}

func TestNewLogger(t *testing.T) {
	var prod, dev bytes.Buffer
	newLogger(config.Config{Env: "prod"}, &prod).Info("hello")
	newLogger(config.Config{Env: "dev"}, &dev).Info("hello")
	if !json.Valid(prod.Bytes()) {
		t.Errorf("prod log %q is not JSON", prod.String())
	}
	if json.Valid(dev.Bytes()) || !strings.Contains(dev.String(), "msg=hello") {
		t.Errorf("dev log %q is not slog text", dev.String())
	}
}
