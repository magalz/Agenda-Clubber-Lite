package web

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	gin.SetMode(gin.TestMode)
	os.Exit(m.Run())
}

// newRouter returns the router of an App that logs JSON into the buffer.
func newRouter(t *testing.T, cidrs ...string) (*gin.Engine, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	app := NewApp(testConfig(t, cidrs...), slog.New(slog.NewJSONHandler(logs, nil)), stubStore{})
	app.Now = func() time.Time { return webTestNow }
	return app.Router(), logs
}

// logLines decodes the JSON log lines written so far.
func logLines(t *testing.T, logs *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	sc := bufio.NewScanner(bytes.NewReader(logs.Bytes()))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("log line %q is not JSON: %v", sc.Text(), err)
		}
		lines = append(lines, m)
	}
	return lines
}

// requestLine returns the single "request" log entry.
func requestLine(t *testing.T, logs *bytes.Buffer) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, l := range logLines(t, logs) {
		if l["msg"] == "request" {
			found = append(found, l)
		}
	}
	if len(found) != 1 {
		t.Fatalf("got %d request log entries, want 1: %s", len(found), logs)
	}
	return found[0]
}

// serve sends a request without a body; do (middleware_test.go) builds it.
func serve(r http.Handler, method, target string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	return do(r, method, target, nil, mod...)
}

func TestHealthz(t *testing.T) {
	r, _ := newRouter(t)
	rec := serve(r, http.MethodGet, "/healthz")
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("body = %q, want ok", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
}

func TestNewAppParsesTheEmbeddedTemplates(t *testing.T) {
	a := newTestApp(t)
	if a.Render == nil {
		t.Fatal("App.Render is nil")
	}

	// The shipped footer renders through the App's own renderer.
	c, rec := newCtx()
	a.Render.Partial(c, http.StatusOK, "footer", nil)
	for _, want := range []string{"feito_por --dev=IgnisDev", `href="mailto:ignisdev@magalz.space"`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("footer missing %q: %s", want, rec.Body)
		}
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	r, logs := newRouter(t)
	rec := serve(r, http.MethodGet, "/nope")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if got := requestLine(t, logs)["status"]; got != float64(404) {
		t.Errorf("logged status = %v, want 404", got)
	}
}

func TestPanicIsRecovered(t *testing.T) {
	r, logs := newRouter(t)
	r.GET("/boom", func(*gin.Context) { panic("kaboom-value") })

	rec := serve(r, http.MethodGet, "/boom")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if rec.Body.String() != "erro interno" {
		t.Errorf("body = %q, want %q", rec.Body.String(), "erro interno")
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", got)
	}

	var panicLine map[string]any
	for _, l := range logLines(t, logs) {
		if l["level"] == "ERROR" {
			panicLine = l
		}
	}
	if panicLine == nil {
		t.Fatalf("no ERROR log entry for the panic: %s", logs)
	}
	if panicLine["panic"] != "kaboom-value" {
		t.Errorf("logged panic = %v, want kaboom-value", panicLine["panic"])
	}
	if stack, _ := panicLine["stack"].(string); !strings.Contains(stack, "app_test.go") {
		t.Errorf("logged stack does not reach the panicking handler: %q", stack)
	}
	if got := requestLine(t, logs)["status"]; got != float64(500) {
		t.Errorf("logged request status = %v, want 500", got)
	}

	if rec := serve(r, http.MethodGet, "/healthz"); rec.Code != http.StatusOK {
		t.Errorf("healthz after a panic = %d, want 200", rec.Code)
	}
}

func TestRequestLogFields(t *testing.T) {
	r, logs := newRouter(t)
	serve(r, http.MethodGet, "/healthz?token=query-secret", func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer header-secret")
		req.Header.Set("Cookie", "__Host-session=cookie-secret")
		req.Header.Set("Referer", "http://localhost/?token=referer-secret")
	})

	line := requestLine(t, logs)
	if line["method"] != "GET" || line["path"] != "/healthz" || line["status"] != float64(200) {
		t.Errorf("method/path/status = %v/%v/%v", line["method"], line["path"], line["status"])
	}
	if _, ok := line["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms = %v, want a number", line["duration_ms"])
	}
	if line["ip"] != "192.0.2.1" {
		t.Errorf("ip = %v, want 192.0.2.1", line["ip"])
	}
	for _, secret := range []string{"query-secret", "header-secret", "cookie-secret", "referer-secret"} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("log leaks %q: %s", secret, logs)
		}
	}
}

func TestClientIP(t *testing.T) {
	cf := func(req *http.Request) { req.Header.Set("CF-Connecting-IP", "203.0.113.9") }
	xff := func(req *http.Request) { req.Header.Set("X-Forwarded-For", "198.51.100.7") }
	tests := []struct {
		name  string
		cidrs []string
		mod   []func(*http.Request)
		want  string
	}{
		{"no trusted proxy ignores CF-Connecting-IP", nil, []func(*http.Request){cf}, "192.0.2.1"},
		{"no trusted proxy ignores X-Forwarded-For", nil, []func(*http.Request){xff}, "192.0.2.1"},
		{"other trusted range ignores CF-Connecting-IP", []string{"10.0.0.0/8"}, []func(*http.Request){cf}, "192.0.2.1"},
		{"trusted proxy is believed on CF-Connecting-IP", []string{"192.0.2.0/24"}, []func(*http.Request){cf}, "203.0.113.9"},
		{"trusted proxy still ignores X-Forwarded-For", []string{"192.0.2.0/24"}, []func(*http.Request){xff}, "192.0.2.1"},
		{"trusted proxy without the header", []string{"192.0.2.0/24"}, nil, "192.0.2.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, logs := newRouter(t, tt.cidrs...)
			serve(r, http.MethodGet, "/healthz", tt.mod...)
			if got := requestLine(t, logs)["ip"]; got != tt.want {
				t.Errorf("ip = %v, want %s", got, tt.want)
			}
		})
	}
}
