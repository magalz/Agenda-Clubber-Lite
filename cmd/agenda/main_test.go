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
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/config"
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

func TestRunUsageOnUnknownSubcommand(t *testing.T) {
	for _, args := range [][]string{{"bogus"}, {"-h"}, {"serve", "extra"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), args, getenvOf(), &stdout, &stderr)
			if code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if !strings.Contains(stderr.String(), "usage") || !strings.Contains(stderr.String(), "serve") {
				t.Errorf("stderr = %q, want a usage text listing serve", stderr.String())
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
	env := getenvOf("APP_ORIGIN", "http://localhost", "APP_ADDR", busy.Addr().String())
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
	env := getenvOf("APP_ORIGIN", "http://"+addr, "APP_ADDR", addr)
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
