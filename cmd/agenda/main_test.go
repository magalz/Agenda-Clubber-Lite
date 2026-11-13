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
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongostore"
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
			code := run(context.Background(), args, getenvOf(), nil, &stdout, &stderr)
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

// prodSMTP is the SMTP setting production needs: a host and a login (TLS and
// authentication are mandatory there). Nothing connects to it.
var prodSMTP = []string{"SMTP_HOST", "smtp.example", "SMTP_USERNAME", "smtp-user-9b2e", "SMTP_PASSWORD", "smtp-pass-9b2e"}

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
		code := run(context.Background(), args, getenvOf("MONGO_URI", testMongoURI), nil, &stdout, &stderr)
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
		code := run(ctx, args, getenvOf("APP_ORIGIN", "http://"+addr, "APP_ADDR", addr), nil, &stdout, &stderr)
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
	if code := run(ctx, []string{"serve"}, env, nil, &stdout, &stderr); code != 1 {
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
	env := getenvOf(slices.Concat(mongoEnv(t), prodSMTP, []string{"APP_ORIGIN", "http://localhost", "APP_ADDR", busy.Addr().String()})...)
	done := make(chan int, 1)
	go func() { done <- run(context.Background(), []string{"serve"}, env, nil, &stdout, &stderr) }()
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
	env := getenvOf(slices.Concat(prodSMTP, []string{"APP_ORIGIN", "http://" + addr, "APP_ADDR", addr, "MONGO_URI", unreachableURI})...)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	if code := run(ctx, []string{"serve"}, env, nil, &stdout, &stderr); code != 1 {
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
	env := getenvOf(slices.Concat(dbEnv, prodSMTP, []string{"APP_ORIGIN", "http://" + addr, "APP_ADDR", addr})...)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, env, nil, &stdout, &stderr) }()

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
	// Production sends through the SMTP relay; the log names the kind of sender and
	// holds neither the relay's host nor the login.
	if !strings.Contains(stdout.String(), `"sender":"smtp"`) {
		t.Errorf("stdout = %q, want the sender kind logged as smtp", stdout.String())
	}
	if strings.Contains(stdout.String(), "smtp.example") || strings.Contains(stdout.String(), "9b2e") {
		t.Errorf("stdout logs the SMTP host, username or password: %q", stdout.String())
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
		if code := run(context.Background(), []string{"migrate"}, env, nil, &stdout, &stderr); code != 0 {
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
			if code := run(context.Background(), []string{"migrate"}, getenvOf(tc.env...), nil, &stdout, &stderr); code != 1 {
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
	if code := run(context.Background(), []string{"migrate"}, getenvOf("MONGO_URI", unreachableURI), nil, &stdout, &stderr); code != 1 {
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
	code = run(context.Background(), []string{"healthcheck"}, env, nil, &out, &errb)
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

// --- serve: the outbox worker and the cleanup jobs ---

// waitUntil polls cond every 20 ms and fails the test when it is still false
// after 15 s.
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// waitFor fails the test when ch gets no value within 5 s.
func waitFor(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// countLevel counts the lines of a slog text log at the given level.
func countLevel(logs, level string) int {
	n := 0
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "level="+level+" ") {
			n++
		}
	}
	return n
}

// openTestStore opens the database that mongoEnv named, for a test that seeds
// and inspects it while serve runs on the same one.
func openTestStore(t *testing.T, env []string) *mongostore.Store {
	t.Helper()
	get := getenvOf(env...)
	s, err := mongostore.Open(t.Context(), get("MONGO_URI"), get("MONGO_DB"))
	if err != nil {
		t.Fatalf("open the test database: %v", err) // the text of Open holds nothing of the URI
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Errorf("close the test store: %v", err)
		}
	})
	if err := s.EnsureSchema(t.Context()); err != nil {
		t.Fatalf("create the schema: %v", err)
	}
	return s
}

// serveInBackground runs serve on a free port, with the environment kv on top of
// APP_ORIGIN and APP_ADDR, and returns once it answers /healthz. stop cancels it
// and returns its exit code and what it logged; it may be called again, and
// runs when the test ends.
func serveInBackground(t *testing.T, kv ...string) (stop func() (code int, stdout string)) {
	t.Helper()
	gin.DefaultWriter = io.Discard // dev mode prints Gin's route table
	t.Cleanup(func() { gin.DefaultWriter = os.Stdout; gin.SetMode(gin.TestMode) })

	addr := freeAddr(t)
	env := getenvOf(slices.Concat([]string{"APP_ORIGIN", "http://" + addr, "APP_ADDR", addr}, kv)...)
	ctx, cancel := context.WithCancel(context.Background())
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"serve"}, env, nil, &out, &errb) }()

	var (
		once   sync.Once
		code   = -1
		stdout string
	)
	stop = func() (int, string) {
		once.Do(func() {
			cancel()
			select {
			case code = <-done:
				stdout = out.String() // safe: serve has returned
			case <-time.After(10 * time.Second):
				t.Error("serve did not return within 10 s of its context being canceled")
			}
		})
		return code, stdout
	}
	t.Cleanup(func() { stop() })

	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		select {
		case c := <-done:
			done <- c // for stop
			t.Fatalf("serve exited with %d before it answered /healthz\nstdout: %s\nstderr: %s", c, &out, &errb)
		default:
		}
		if resp, err := http.Get("http://" + addr + "/healthz"); err == nil {
			resp.Body.Close()
			return stop
		}
		if time.Now().After(deadline) {
			t.Fatal("serve never answered /healthz")
		}
	}
}

// hashOf is a 32-byte token or session hash made of one repeated byte.
func hashOf(n byte) []byte { return bytes.Repeat([]byte{n}, domain.TokenHashLen) }

func seedUser(t *testing.T, s domain.Store) domain.User {
	t.Helper()
	now := time.Now().UTC()
	verified := now
	u := domain.User{
		Name:              "Pessoa Teste",
		Email:             "pessoa@example.com",
		Phone:             "81987654321",
		PasswordHash:      "placeholder-hash",
		PasswordChangedAt: now,
		Status:            domain.UserStatusActive,
		EmailVerifiedAt:   &verified,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	if err := s.Repos().Users.Create(t.Context(), &u); err != nil {
		t.Fatalf("insert the user: %v", err)
	}
	return u
}

// enqueueTestMail queues one message that is due.
func enqueueTestMail(t *testing.T, s domain.Store) {
	t.Helper()
	now := time.Now().UTC()
	m := domain.OutboxMessage{
		To:            "destino@example.com",
		Subject:       "Teste de envio",
		Text:          "corpo do e-mail",
		NextAttemptAt: now.Add(-time.Minute),
		CreatedAt:     now,
	}
	if err := s.Repos().Outbox.Enqueue(t.Context(), &m); err != nil {
		t.Fatalf("enqueue the message: %v", err)
	}
}

// unsentMessages counts the messages a worker could still claim an hour from
// now: the ones that were never marked sent or given up.
func unsentMessages(t *testing.T, s domain.Store) int {
	t.Helper()
	msgs, err := s.Repos().Outbox.Claim(t.Context(), time.Now().UTC().Add(time.Hour), time.Minute, 10)
	if err != nil {
		t.Fatalf("claim the outbox: %v", err)
	}
	return len(msgs)
}

func TestRunServeDeliversQueuedMailThroughTheDropFolder(t *testing.T) {
	env := mongoEnv(t)
	store := openTestStore(t, env)
	enqueueTestMail(t, store)
	dropDir := filepath.Join(t.TempDir(), "mail")

	stop := serveInBackground(t, slices.Concat(env, []string{
		"APP_ENV", "dev", "MAIL_DROP_DIR", dropDir, "SMTP_FROM", "Clube <noreply@clube.test>",
	})...)

	var file string
	waitUntil(t, "the queued message in the drop folder", func() bool {
		files, _ := filepath.Glob(filepath.Join(dropDir, "*.eml"))
		if len(files) == 0 {
			return false
		}
		file = files[0]
		return true
	})
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read the message: %v", err)
	}
	for _, want := range []string{"destino@example.com", "noreply@clube.test", "Teste de envio"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the message file lacks %q:\n%s", want, raw)
		}
	}

	code, stdout := stop()
	if code != 0 {
		t.Errorf("exit code = %d, want 0 after a clean shutdown", code)
	}
	if !strings.Contains(stdout, "drop folder") || !strings.Contains(stdout, "sender=drop") {
		t.Errorf("stdout = %q, want the drop folder logged as sender=drop", stdout)
	}
	if n := countLevel(stdout, "ERROR"); n != 0 {
		t.Errorf("stdout has %d error lines: %s", n, stdout)
	}
	// The shutdown waited for the worker: the result was recorded before the
	// database closed.
	if n := unsentMessages(t, store); n != 0 {
		t.Errorf("%d messages are still queued after the shutdown, want 0", n)
	}
}

func TestRunServeWithoutASenderWarnsOnceAndLeavesTheOutbox(t *testing.T) {
	env := mongoEnv(t)
	store := openTestStore(t, env)
	enqueueTestMail(t, store)

	stop := serveInBackground(t, slices.Concat(env, []string{"APP_ENV", "dev"})...)
	time.Sleep(300 * time.Millisecond) // time enough for a worker to have picked the message up

	code, stdout := stop()
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if n := countLevel(stdout, "WARN"); n != 1 || !strings.Contains(stdout, "email sending disabled: no SMTP_HOST or MAIL_DROP_DIR") || !strings.Contains(stdout, "sender=none") {
		t.Errorf("stdout has %d warnings, want exactly one saying email sending is disabled, with sender=none:\n%s", n, stdout)
	}
	if n := unsentMessages(t, store); n != 1 {
		t.Errorf("%d messages are queued, want the 1 left for a later run with a sender", n)
	}
}

func TestRunServePurgesExpiredSessionsAndTokens(t *testing.T) {
	old := cleanupInterval
	cleanupInterval = 50 * time.Millisecond
	t.Cleanup(func() { cleanupInterval = old })

	env := mongoEnv(t)
	store := openTestStore(t, env)
	u := seedUser(t, store)
	stop := serveInBackground(t, slices.Concat(env, []string{"APP_ENV", "dev"})...)

	now := time.Now().UTC()
	ctx := t.Context()
	repos := store.Repos()
	for _, s := range []domain.Session{
		{Hash: hashOf(1), UserID: u.ID, CreatedAt: now.Add(-2 * time.Hour), LastSeenAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)},
		{Hash: hashOf(2), UserID: u.ID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)},
	} {
		if err := repos.Sessions.Create(ctx, s); err != nil {
			t.Fatalf("insert a session: %v", err)
		}
	}
	// The purposes differ, so storing the second token does not retire the first.
	for _, tok := range []domain.Token{
		{UserID: u.ID, Purpose: domain.TokenPurposeVerifyEmail, Hash: hashOf(3), CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)},
		{UserID: u.ID, Purpose: domain.TokenPurposeResetPassword, Hash: hashOf(4), CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	} {
		if err := repos.Tokens.Create(ctx, &tok); err != nil {
			t.Fatalf("insert a token: %v", err)
		}
	}

	waitUntil(t, "the expired session to be removed", func() bool {
		_, err := repos.Sessions.Get(ctx, hashOf(1))
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("get the expired session: %v", err)
		}
		return err != nil
	})
	// Tokens have no Get: storing a token under a hash that is still stored is
	// ErrConflict, so the first Create that works shows the old one is gone.
	waitUntil(t, "the expired token to be removed", func() bool {
		at := time.Now().UTC()
		err := repos.Tokens.Create(ctx, &domain.Token{UserID: u.ID, Purpose: domain.TokenPurposeVerifyEmail, Hash: hashOf(3), CreatedAt: at, ExpiresAt: at.Add(time.Hour)})
		if err != nil && !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("probe the expired token: %v", err)
		}
		return err == nil
	})

	if _, err := repos.Sessions.Get(ctx, hashOf(2)); err != nil {
		t.Errorf("the live session: %v, want it kept", err)
	}
	at := time.Now().UTC()
	live := domain.Token{UserID: u.ID, Purpose: domain.TokenPurposeResetPassword, Hash: hashOf(4), CreatedAt: at, ExpiresAt: at.Add(time.Hour)}
	if err := repos.Tokens.Create(ctx, &live); !errors.Is(err, domain.ErrConflict) {
		t.Errorf("storing the live token again = %v, want ErrConflict: it must stay", err)
	}

	code, stdout := stop()
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	for _, want := range []string{`expired sessions removed" count=1`, `expired tokens removed" count=1`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout = %q, want the cleanup logged with %q", stdout, want)
		}
	}
}

// A sender that cannot be built stops the start before the database is opened,
// and says so without echoing the settings.
func TestRunServeWithAMailSenderThatCannotBeBuiltExits1(t *testing.T) {
	old := openTimeout
	openTimeout = 500 * time.Millisecond
	t.Cleanup(func() { openTimeout = old })

	addr := freeAddr(t)
	var stdout, stderr bytes.Buffer
	env := getenvOf("APP_ENV", "dev", "APP_ORIGIN", "http://"+addr, "APP_ADDR", addr, "MONGO_URI", unreachableURI,
		"SMTP_HOST", "smtp.private-relay.example", "SMTP_USERNAME", "user-secret", "SMTP_PASSWORD", "smtp-secret",
		"SMTP_FROM", "not an address")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if code := run(ctx, []string{"serve"}, env, nil, &stdout, &stderr); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "mail sender failed") {
		t.Errorf("stdout = %q, want the sender failure logged", stdout.String())
	}
	if strings.Contains(stdout.String(), openHint) {
		t.Errorf("stdout = %q: the database was opened before the sender was built", stdout.String())
	}
	wantNoSecrets(t, stdout.String(), stderr.String())
	for _, out := range []string{stdout.String(), stderr.String()} {
		for _, leak := range []string{"user-secret", "smtp-secret", "private-relay"} {
			if strings.Contains(out, leak) {
				t.Errorf("output %q leaks %q", out, leak)
			}
		}
	}
}

func TestNewSender(t *testing.T) {
	smtp := config.SMTPConfig{
		Host: "smtp.private-relay.example", Port: 587, Username: "user-secret", Password: "smtp-secret",
		From: "Clube <noreply@clube.test>",
	}
	newLog := func() (*slog.Logger, *bytes.Buffer) {
		var buf bytes.Buffer
		return slog.New(slog.NewTextHandler(&buf, nil)), &buf
	}
	noSecrets := func(t *testing.T, texts ...string) {
		t.Helper()
		for _, text := range texts {
			for _, leak := range []string{"user-secret", "smtp-secret"} {
				if strings.Contains(text, leak) {
					t.Errorf("%q leaks %q", text, leak)
				}
			}
		}
	}

	t.Run("a drop folder writes files with the configured From", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "mail")
		log, buf := newLog()
		s, err := newSender(config.Config{Env: "dev", SMTP: config.SMTPConfig{From: smtp.From}, MailDropDir: dir}, log)
		if err != nil {
			t.Fatalf("newSender: %v", err)
		}
		d, ok := s.(*mail.DropSender)
		if !ok {
			t.Fatalf("sender = %T, want *mail.DropSender", s)
		}
		if d.From != smtp.From {
			t.Errorf("From = %q, want SMTP_FROM %q", d.From, smtp.From)
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			t.Errorf("the drop folder was not created: %v", err)
		}
		if !strings.Contains(buf.String(), "drop folder") || !strings.Contains(buf.String(), "sender=drop") || countLevel(buf.String(), "WARN") != 0 {
			t.Errorf("log = %q, want the drop folder named, sender=drop and no warning", buf.String())
		}
	})

	t.Run("a drop folder wins over SMTP_HOST", func(t *testing.T) {
		log, buf := newLog()
		s, err := newSender(config.Config{Env: "dev", SMTP: smtp, MailDropDir: t.TempDir()}, log)
		if err != nil {
			t.Fatalf("newSender: %v", err)
		}
		if _, ok := s.(*mail.DropSender); !ok {
			t.Errorf("sender = %T, want *mail.DropSender", s)
		}
		noSecrets(t, buf.String())
	})

	t.Run("SMTP_HOST gives the SMTP sender and logs no login", func(t *testing.T) {
		log, buf := newLog()
		s, err := newSender(config.Config{Env: "prod", SMTP: smtp}, log)
		if err != nil {
			t.Fatalf("newSender: %v", err)
		}
		if _, ok := s.(*mail.SMTPSender); !ok {
			t.Errorf("sender = %T, want *mail.SMTPSender", s)
		}
		if !strings.Contains(buf.String(), "sender=smtp") {
			t.Errorf("log = %q, want sender=smtp", buf.String())
		}
		// The kind is logged, not the relay: no host, user or password.
		if strings.Contains(buf.String(), "private-relay") {
			t.Errorf("log = %q, want no SMTP host", buf.String())
		}
		noSecrets(t, buf.String())
	})

	t.Run("neither gives no sender and one warning", func(t *testing.T) {
		log, buf := newLog()
		s, err := newSender(config.Config{Env: "dev", SMTP: config.SMTPConfig{Port: 587, From: smtp.From}}, log)
		if err != nil {
			t.Fatalf("newSender: %v", err)
		}
		if s != nil {
			t.Errorf("sender = %T, want none", s)
		}
		if n := countLevel(buf.String(), "WARN"); n != 1 || !strings.Contains(buf.String(), "email sending disabled: no SMTP_HOST or MAIL_DROP_DIR") || !strings.Contains(buf.String(), "sender=none") {
			t.Errorf("log = %q, want exactly one warning that email sending is disabled, with sender=none", buf.String())
		}
	})

	t.Run("errors give no sender and leak nothing", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		noPassword, badFrom := smtp, smtp
		noPassword.Password = ""
		badFrom.From = "not an address"
		for name, cfg := range map[string]config.Config{
			"a username without a password": {Env: "prod", SMTP: noPassword},
			"a From that is no address":     {Env: "prod", SMTP: badFrom},
			"a drop folder under a file":    {Env: "dev", SMTP: smtp, MailDropDir: filepath.Join(file, "mail")},
		} {
			log, buf := newLog()
			s, err := newSender(cfg, log)
			if err == nil {
				t.Errorf("%s: newSender succeeded, want an error", name)
				continue
			}
			if s != nil {
				t.Errorf("%s: sender = %T with an error, want a nil sender", name, s)
			}
			noSecrets(t, err.Error(), buf.String())
		}
	})
}

// fakeSessions stands in for domain.Sessions: only DeleteExpired exists.
type fakeSessions struct {
	domain.Sessions
	calls   atomic.Int32
	err     error
	entered chan struct{} // gets a value each time DeleteExpired starts, when set
	release chan struct{} // DeleteExpired waits for it to close, when set
}

func (f *fakeSessions) DeleteExpired(context.Context, time.Time) (int, error) {
	f.calls.Add(1)
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		<-f.release
	}
	return 0, f.err
}

// fakeTokens stands in for domain.Tokens: only DeleteExpired exists.
type fakeTokens struct {
	domain.Tokens
	calls   atomic.Int32
	removed int
}

func (f *fakeTokens) DeleteExpired(context.Context, time.Time) (int, error) {
	f.calls.Add(1)
	return f.removed, nil
}

// fakeOutbox hands out one message, once, and records the error of a failed send.
type fakeOutbox struct {
	domain.Outbox
	mu     sync.Mutex
	msg    *domain.OutboxMessage
	failed chan string
}

func (f *fakeOutbox) Claim(context.Context, time.Time, time.Duration, int) ([]domain.OutboxMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.msg == nil {
		return nil, nil
	}
	m := *f.msg
	f.msg = nil
	return []domain.OutboxMessage{m}, nil
}

func (f *fakeOutbox) MarkFailed(_ context.Context, _ string, lastError string, _ *time.Time) error {
	f.failed <- lastError
	return nil
}

// blockingSender holds a send open until its context is canceled and then takes
// a moment to finish, as a send that is cut short by a shutdown does.
type blockingSender struct {
	entered  chan struct{}
	finished atomic.Bool
}

func (s *blockingSender) Send(ctx context.Context, _ domain.OutboxMessage) error {
	close(s.entered)
	<-ctx.Done()
	time.Sleep(100 * time.Millisecond)
	s.finished.Store(true)
	return ctx.Err()
}

// startTestJobs starts the jobs and stops them when the test ends.
func startTestJobs(t *testing.T, log *slog.Logger, repos domain.Repos, sender mail.Sender) (stop func(time.Duration) bool) {
	t.Helper()
	stop = startJobs(context.Background(), log, repos, sender)
	t.Cleanup(func() { stop(5 * time.Second) })
	return stop
}

func TestStartJobsStopWaitsForTheMessageInFlight(t *testing.T) {
	outbox := &fakeOutbox{
		msg:    &domain.OutboxMessage{ID: "m1", To: "destino@example.com", Subject: "x", Text: "y"},
		failed: make(chan string, 4),
	}
	sender := &blockingSender{entered: make(chan struct{})}
	stop := startTestJobs(t, discardLog(), domain.Repos{Outbox: outbox, Sessions: &fakeSessions{}, Tokens: &fakeTokens{}}, sender)
	waitFor(t, sender.entered, "the worker to start sending the queued message")

	if !stop(5 * time.Second) {
		t.Fatal("stop(5s) = false, want the jobs to stop")
	}
	if !sender.finished.Load() {
		t.Error("stop returned before the send in flight was dealt with")
	}
	select {
	case <-outbox.failed:
	default:
		t.Error("stop returned before the result of the send in flight was recorded")
	}
}

func TestStartJobsStopWaitsForTheCleanup(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	sessions := &fakeSessions{entered: make(chan struct{}, 1), release: release}
	stop := startTestJobs(t, discardLog(), domain.Repos{Sessions: sessions, Tokens: &fakeTokens{}}, nil)
	t.Cleanup(func() { once.Do(func() { close(release) }) }) // runs before stop's own cleanup
	waitFor(t, sessions.entered, "the cleanup to start")

	stopped := make(chan bool, 1)
	go func() { stopped <- stop(10 * time.Second) }()
	select {
	case <-stopped:
		t.Fatal("stop returned while the cleanup was still running")
	case <-time.After(200 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	select {
	case ok := <-stopped:
		if !ok {
			t.Error("stop(10s) = false after the cleanup finished, want true")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return after the cleanup finished")
	}
}

func TestStartJobsStopGivesUpAfterItsTimeout(t *testing.T) {
	release := make(chan struct{})
	sessions := &fakeSessions{entered: make(chan struct{}, 1), release: release}
	stop := startTestJobs(t, discardLog(), domain.Repos{Sessions: sessions, Tokens: &fakeTokens{}}, nil)
	t.Cleanup(func() { close(release) }) // runs before stop's own cleanup
	waitFor(t, sessions.entered, "the cleanup to start")

	start := time.Now()
	if stop(50 * time.Millisecond) {
		t.Error("stop = true while a job hangs, want false")
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("stop took %v, want it bounded by its timeout", took)
	}
}

func TestStartJobsCleansUpOnEveryTick(t *testing.T) {
	old := cleanupInterval
	cleanupInterval = 10 * time.Millisecond
	t.Cleanup(func() { cleanupInterval = old })

	sessions := &fakeSessions{err: errors.New("sessions boom")}
	tokens := &fakeTokens{removed: 3}
	var buf bytes.Buffer
	stop := startTestJobs(t, slog.New(slog.NewTextHandler(&buf, nil)), domain.Repos{Sessions: sessions, Tokens: tokens}, nil)

	// A failing session purge must not keep the token purge from running.
	waitUntil(t, "three runs of the cleanup", func() bool { return sessions.calls.Load() >= 3 && tokens.calls.Load() >= 3 })
	if !stop(5 * time.Second) {
		t.Fatal("stop(5s) = false, want the jobs to stop")
	}
	logs := buf.String() // safe: the jobs have stopped
	if !strings.Contains(logs, "sessions boom") {
		t.Errorf("log = %q, want the failed session purge logged", logs)
	}
	if !strings.Contains(logs, `expired tokens removed" count=3`) {
		t.Errorf("log = %q, want the removed tokens counted", logs)
	}
	// The failed purge is an error, not a count.
	if strings.Contains(logs, "expired sessions removed") {
		t.Errorf("log = %q, want no count for the session purge that failed", logs)
	}
}

// Each purge logs its count at Info on every run, also when nothing was removed:
// an operator can see the cleanup ran.
func TestPurgeExpiredLogsEachCountEvenWhenNothingWasRemoved(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	purgeExpired(context.Background(), log, domain.Repos{Sessions: &fakeSessions{}, Tokens: &fakeTokens{}})
	for _, want := range []string{`level=INFO msg="cleanup: expired sessions removed" count=0`, `level=INFO msg="cleanup: expired tokens removed" count=0`} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log = %q, want %q", buf.String(), want)
		}
	}
}
