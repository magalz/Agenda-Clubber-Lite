package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/store"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

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

// dataDirEnv returns an environment with only DATA_DIR set: migrate and backup
// must not need APP_ORIGIN.
func dataDirEnv(dir string) func(string) string { return getenvOf("DATA_DIR", dir) }

// countTables reports how many of the named tables exist in the database
// inside dir (or at path, for a file).
func countTables(t *testing.T, dbPath string, names ...string) int {
	t.Helper()
	db, err := store.Open(context.Background(), dbPath)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer db.Close()
	var n int
	for _, name := range names {
		var found int
		if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&found); err != nil {
			t.Fatalf("look up table %s: %v", name, err)
		}
		n += found
	}
	return n
}

// migratedDataDir returns a data dir whose database has the schema.
func migratedDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"migrate"}, dataDirEnv(dir), &stdout, &stderr); code != 0 {
		t.Fatalf("migrate: exit code = %d; stderr: %s", code, &stderr)
	}
	return dir
}

func TestRunUsageOnUnknownSubcommand(t *testing.T) {
	for _, args := range [][]string{
		{"bogus"}, {"-h"}, {"serve", "extra"},
		{"migrate", "extra"}, {"backup"}, {"backup", "a.db", "b.db"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), args, getenvOf(), &stdout, &stderr)
			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			for _, want := range []string{"usage", "serve", "migrate", "backup"} {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr = %q, want a usage text mentioning %q", stderr.String(), want)
				}
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func TestRunServeWithoutOriginFails(t *testing.T) {
	for _, args := range [][]string{nil, {"serve"}} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), args, getenvOf(), &stdout, &stderr)
		if code != 1 {
			t.Errorf("run(%v) = %d, want 1", args, code)
		}
		if !strings.Contains(stderr.String(), "APP_ORIGIN") {
			t.Errorf("stderr = %q, want the config error naming APP_ORIGIN", stderr.String())
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
	env := getenvOf("APP_ORIGIN", "http://localhost", "APP_ADDR", busy.Addr().String(), "DATA_DIR", t.TempDir())
	done := make(chan int, 1)
	go func() { done <- run(context.Background(), []string{"serve"}, env, &stdout, &stderr) }()
	select {
	case code := <-done:
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return after a listen error")
	}
}

func TestRunServesAndShutsDownGracefully(t *testing.T) {
	addr := freeAddr(t)
	var stdout, stderr bytes.Buffer
	dataDir := filepath.Join(t.TempDir(), "data")
	env := getenvOf("APP_ORIGIN", "http://"+addr, "APP_ADDR", addr, "DATA_DIR", dataDir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	go func() { done <- run(ctx, nil, env, &stdout, &stderr) }()

	var resp *http.Response
	var err error
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
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
	dbPath := filepath.Join(dataDir, "agenda.db")
	if n := countTables(t, dbPath, "users", "events", "schema_migrations"); n != 3 {
		t.Errorf("serve left %d of the 3 probed tables in the database, want all of them (migrated at startup)", n)
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
	// A closed database has released its files: the last close deletes the
	// WAL, and Windows refuses to remove an open file.
	if _, err := os.Stat(dbPath + "-wal"); err == nil {
		t.Error("the WAL file is still there after shutdown: the database was not closed")
	}
	if err := os.Remove(dbPath); err != nil {
		t.Errorf("remove database after shutdown: %v", err)
	}
}

func TestRunMigrateCreatesTheSchemaAndRepeats(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new", "data") // run creates it
	for i := range 2 {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), []string{"migrate"}, dataDirEnv(dir), &stdout, &stderr); code != 0 {
			t.Fatalf("run %d: exit code = %d, want 0; stderr: %s", i+1, code, &stderr)
		}
	}
	if n := countTables(t, filepath.Join(dir, "agenda.db"), "users", "collectives", "events", "schema_migrations"); n != 4 {
		t.Errorf("migrate left %d of the 4 probed tables, want all of them", n)
	}
}

func TestRunBackupCopiesTheDatabase(t *testing.T) {
	dir := migratedDataDir(t)
	dest := filepath.Join(t.TempDir(), "backup.db")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"backup", dest}, dataDirEnv(dir), &stdout, &stderr); code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, &stderr)
	}
	if n := countTables(t, dest, "users", "events", "schema_migrations"); n != 3 {
		t.Errorf("backup holds %d of the 3 probed tables, want all of them", n)
	}
}

func TestRunBackupFailures(t *testing.T) {
	t.Run("destination exists", func(t *testing.T) {
		dir := migratedDataDir(t)
		dest := filepath.Join(t.TempDir(), "backup.db")
		if err := os.WriteFile(dest, []byte("keep me"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), []string{"backup", dest}, dataDirEnv(dir), &stdout, &stderr); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if !strings.Contains(stderr.String(), "agenda:") {
			t.Errorf("stderr = %q, want an error line", stderr.String())
		}
		if got, err := os.ReadFile(dest); err != nil || string(got) != "keep me" {
			t.Errorf("destination = %q, %v; want it untouched", got, err)
		}
	})
	t.Run("no database yet", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "data")
		dest := filepath.Join(t.TempDir(), "backup.db")
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), []string{"backup", dest}, dataDirEnv(dir), &stdout, &stderr); code != 1 {
			t.Errorf("exit code = %d, want 1: there is nothing to back up", code)
		}
		if _, err := os.Stat(filepath.Join(dir, "agenda.db")); err == nil {
			t.Error("backup created an empty database")
		}
		if _, err := os.Stat(dest); err == nil {
			t.Error("backup wrote a file for a database that does not exist")
		}
	})
}

func TestRunExits1WhenTheDatabaseIsUnusable(t *testing.T) {
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	newer := t.TempDir() // a database from a newer build than this binary
	db, err := store.Open(context.Background(), filepath.Join(newer, "agenda.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO schema_migrations (version, applied_at) VALUES (999, '2026-11-12T16:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	for name, dir := range map[string]string{"data dir is a file": notADir, "database newer than the binary": newer} {
		t.Run("serve/"+name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := getenvOf("APP_ORIGIN", "http://localhost", "APP_ADDR", freeAddr(t), "DATA_DIR", dir)
			done := make(chan int, 1)
			go func() { done <- run(context.Background(), []string{"serve"}, env, &stdout, &stderr) }()
			select {
			case code := <-done:
				if code != 1 {
					t.Errorf("exit code = %d, want 1", code)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("run kept serving with an unusable database")
			}
			if !strings.Contains(stdout.String(), `"level":"ERROR"`) {
				t.Errorf("stdout = %q, want the failure logged as an error", stdout.String())
			}
		})
		t.Run("migrate/"+name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), []string{"migrate"}, dataDirEnv(dir), &stdout, &stderr); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if !strings.Contains(stderr.String(), "agenda:") {
				t.Errorf("stderr = %q, want an error line", stderr.String())
			}
		})
	}
}

func TestNewServerSettings(t *testing.T) {
	gin.DefaultWriter = io.Discard // debug mode prints its route table
	t.Cleanup(func() { gin.DefaultWriter = os.Stdout; gin.SetMode(gin.TestMode) })

	srv := newServer(config.Config{Env: "prod", Addr: ":8120"}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
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

	newServer(config.Config{Env: "dev", Addr: ":8120"}, slog.New(slog.NewJSONHandler(io.Discard, nil)))
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
