package bdd

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
	_ "time/tzdata"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
	"github.com/magalz/agenda-clubber-lite/internal/web"
)

// Clock is a thread-safe mutable clock for controlling web.App time in tests.
type Clock struct {
	mu  sync.RWMutex
	now time.Time
}

// NewClock returns a Clock initialized to start in UTC.
func NewClock(start time.Time) *Clock {
	return &Clock{now: start.UTC()}
}

// Now returns the current clock time in UTC.
func (c *Clock) Now() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.now
}

// Set updates the clock time to t in UTC.
func (c *Clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t.UTC()
}

// Advance moves the clock forward by d.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// World holds the isolated test database, HTTP server, application, and clock
// for an end-to-end BDD scenario.
type World struct {
	T      *testing.T
	App    *web.App
	Store  domain.Store
	Server *httptest.Server
	Clock  *Clock

	outboxMu   sync.Mutex
	outbox     []domain.OutboxMessage
	seenOutbox map[string]bool
}

// Actor represents an independent browser client with its own cookie jar.
type Actor struct {
	Name   string
	Client *http.Client
}

// Response captures an HTTP response without following redirects.
type Response struct {
	Status   int
	Location string
	Headers  http.Header
	Body     string
}

var defaultWorldTime = time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)

// NewWorld creates a fresh MongoDB-backed web.App served over an httptest TLS
// server so __Host- Secure session cookies work natively in Actor cookie jars.
func NewWorld(t *testing.T) *World {
	t.Helper()

	store := mongotest.New(t)
	loc, err := time.LoadLocation("America/Fortaleza")
	if err != nil {
		t.Fatalf("load America/Fortaleza: %v", err)
	}
	clock := NewClock(defaultWorldTime)

	gin.SetMode(gin.TestMode)

	var router http.Handler
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(w, r)
	}))
	srv.StartTLS()
	t.Cleanup(srv.Close)

	cfg := config.Config{
		Env:      "dev",
		Origin:   srv.URL,
		Location: loc,
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	app := web.NewApp(cfg, logger, store)
	app.Now = clock.Now
	router = app.Router()

	return &World{
		T:          t,
		App:        app,
		Store:      store,
		Server:     srv,
		Clock:      clock,
		seenOutbox: make(map[string]bool),
	}
}

// NewActor creates a named HTTP actor with an isolated cookie jar and redirect
// interception enabled so tests observe 3xx Location headers directly.
func (w *World) NewActor(name string) *Actor {
	w.T.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		w.T.Fatalf("create cookie jar for %s: %v", name, err)
	}
	return &Actor{
		Name: name,
		Client: &http.Client{
			Jar:       jar,
			Transport: w.Server.Client().Transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// GET sends an HTTP GET request as actor to path with optional extra headers.
func (w *World) GET(actor *Actor, path string, headers ...http.Header) Response {
	w.T.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, w.resolveURL(path), nil)
	if err != nil {
		w.T.Fatalf("build GET %s: %v", path, err)
	}
	applyHeaders(req, headers...)
	return w.do(actor, req)
}

// POST sends an HTTP POST request with form-encoded body and default Origin and
// Content-Type headers unless overridden in headers.
func (w *World) POST(actor *Actor, path string, form url.Values, headers ...http.Header) Response {
	w.T.Helper()
	encoded := ""
	if form != nil {
		encoded = form.Encode()
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, w.resolveURL(path), strings.NewReader(encoded))
	if err != nil {
		w.T.Fatalf("build POST %s: %v", path, err)
	}
	req.Header.Set("Origin", w.Server.URL)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	applyHeaders(req, headers...)
	return w.do(actor, req)
}

// OutboxMessages claims any newly queued outbox messages and returns all
// captured messages addressed to to (or every captured message when to is empty).
func (w *World) OutboxMessages(to string) []domain.OutboxMessage {
	w.T.Helper()
	w.outboxMu.Lock()
	defer w.outboxMu.Unlock()

	claimed, err := w.Store.Repos().Outbox.Claim(
		context.Background(),
		w.Clock.Now().Add(time.Minute),
		24*time.Hour,
		domain.ListLimitMax,
	)
	if err != nil {
		w.T.Fatalf("claim outbox messages: %v", err)
	}
	for _, msg := range claimed {
		if !w.seenOutbox[msg.ID] {
			w.seenOutbox[msg.ID] = true
			w.outbox = append(w.outbox, msg)
		}
	}

	var matched []domain.OutboxMessage
	for _, msg := range w.outbox {
		if to == "" || strings.EqualFold(msg.To, to) {
			matched = append(matched, msg)
		}
	}
	return matched
}

var linkPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

// ExtractLink returns the first URL in body whose path starts with pathPrefix,
// or an empty string if none is found.
func (w *World) ExtractLink(body, pathPrefix string) string {
	for _, raw := range linkPattern.FindAllString(body, -1) {
		cleaned := strings.TrimRight(raw, ".,);:")
		u, err := url.Parse(cleaned)
		if err != nil {
			continue
		}
		if strings.HasPrefix(u.Path, pathPrefix) {
			return cleaned
		}
	}
	return ""
}

func (w *World) resolveURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return w.Server.URL + path
}

func (w *World) do(actor *Actor, req *http.Request) Response {
	w.T.Helper()
	resp, err := actor.Client.Do(req)
	if err != nil {
		w.T.Fatalf("%s %s %s: %v", actor.Name, req.Method, req.URL.String(), err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		w.T.Fatalf("read response body for %s %s: %v", req.Method, req.URL.String(), err)
	}
	return Response{
		Status:   resp.StatusCode,
		Location: resp.Header.Get("Location"),
		Headers:  resp.Header.Clone(),
		Body:     string(data),
	}
}

func applyHeaders(req *http.Request, headers ...http.Header) {
	for _, h := range headers {
		for k, vs := range h {
			if len(vs) == 0 {
				req.Header.Del(k)
				continue
			}
			req.Header[http.CanonicalHeaderKey(k)] = slices.Clone(vs)
		}
	}
}
