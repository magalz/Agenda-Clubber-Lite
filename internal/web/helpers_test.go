package web

import (
	"bytes"
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

// testOrigin is the APP_ORIGIN of every test App.
const testOrigin = "https://agenda.test"

// webTestNow is the fixed clock of every test App.
var webTestNow = time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)

// Helpers shared by more than one test file of the package. The router helpers
// (newRouter, serve, do, logLines) stay with the middleware and app tests.

func fortaleza(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Fortaleza")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return loc
}

// testConfig is the production-mode configuration of every test App.
func testConfig(t *testing.T, cidrs ...string) config.Config {
	t.Helper()
	return config.Config{Env: "prod", Origin: testOrigin, Location: fortaleza(t), TrustedProxyCIDRs: cidrs}
}

// stubStore is the store of the tests that never reach the database: its
// repositories are nil, so a call on one panics, and WithTx panics. Such a test
// proves that its request does not load a session.
type stubStore struct{}

func (stubStore) Repos() domain.Repos { return domain.Repos{} }
func (stubStore) WithTx(context.Context, func(context.Context, domain.Repos) error) error {
	panic("web test: stubStore.WithTx")
}
func (stubStore) Ping(context.Context) error  { return nil }
func (stubStore) Close(context.Context) error { return nil }

// newTestApp builds the production-mode App the handler and layout tests share,
// on a store that has no database behind it and a fixed clock.
func newTestApp(t *testing.T) *App {
	t.Helper()
	app := NewApp(testConfig(t), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), stubStore{})
	app.Now = func() time.Time { return webTestNow }
	return app
}

// newStoreApp builds an App on an empty MongoDB database of its own and a fixed
// clock (webTestNow). It skips locally, and fails in CI, without MONGO_TEST_URI.
func newStoreApp(t *testing.T) (*App, domain.Store) {
	t.Helper()
	store := mongotest.New(t)
	app := NewApp(testConfig(t), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), store)
	app.Now = func() time.Time { return webTestNow }
	return app, store
}

// insertWebUser stores an active user with a verified email and returns it with
// its ID. mutate adjusts the fixture before it is stored.
func insertWebUser(t *testing.T, store domain.Store, email string, mutate ...func(*domain.User)) domain.User {
	t.Helper()
	verified := webTestNow
	u := domain.User{
		Name:              "Pessoa Teste",
		Email:             email,
		Phone:             "81987654321",
		PasswordHash:      "placeholder-hash",
		PasswordChangedAt: webTestNow,
		Status:            domain.UserStatusActive,
		EmailVerifiedAt:   &verified,
		CreatedAt:         webTestNow,
		UpdatedAt:         webTestNow,
	}
	for _, f := range mutate {
		f(&u)
	}
	if err := store.Repos().Users.Create(context.Background(), &u); err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	return u
}

// loginAs starts a session for the user at app.Now() and returns the cookie a
// browser would send.
func loginAs(t *testing.T, app *App, user domain.User) *http.Cookie {
	t.Helper()
	raw, _, err := auth.StartSession(context.Background(), app.Store.Repos().Sessions, user.ID, "test-agent", app.Now())
	if err != nil {
		t.Fatalf("start session for %s: %v", user.Email, err)
	}
	return &http.Cookie{Name: auth.SessionCookieName, Value: raw}
}

// withOrigin makes a request come from the app's own origin, as a browser form
// post does.
func withOrigin(req *http.Request) { req.Header.Set("Origin", testOrigin) }

// withCookie sends the cookie with the request.
func withCookie(c *http.Cookie) func(*http.Request) {
	return func(req *http.Request) { req.AddCookie(c) }
}

// mustPanic fails unless f panics with a string that contains want.
func mustPanic(t *testing.T, want string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		v := recover()
		if v == nil {
			t.Fatal("no panic")
		}
		if msg, _ := v.(string); !strings.Contains(msg, want) {
			t.Errorf("panic = %v, want it to mention %q", v, want)
		}
	}()
	f()
}

// withFiles adds text files to a MapFS.
func withFiles(m fstest.MapFS, files map[string]string) fstest.MapFS {
	for name, data := range files {
		m[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return m
}

// shippedTemplates copies the shipped base layout, the named layouts and every
// partial into a tree of its own, plus the extra files.
func shippedTemplates(t *testing.T, layouts []string, extra map[string]string) fstest.MapFS {
	t.Helper()
	sub, err := fs.Sub(webfs.Templates, "templates")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	names, err := fs.Glob(sub, "partials/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range append([]string{"base"}, layouts...) {
		names = append(names, "layouts/"+l+".html")
	}
	m := fstest.MapFS{}
	for _, name := range names {
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			t.Fatalf("shipped template %s: %v", name, err)
		}
		m[name] = &fstest.MapFile{Data: data}
	}
	return withFiles(m, extra)
}

// renderAssets hashes a static tree with every file the base layout links.
func renderAssets(t *testing.T) *Assets {
	t.Helper()
	files := []string{
		"favicon.svg",
		"css/tokens.css", "css/base.css", "css/components.css",
		"js/app.js", "js/vendor/htmx-2.0.11.min.js", "js/vendor/alpine-csp-3.17.5.min.js",
	}
	static := fstest.MapFS{}
	for _, name := range files {
		static[name] = &fstest.MapFile{Data: []byte("content of " + name)}
	}
	assets, err := NewAssets(static)
	if err != nil {
		t.Fatalf("NewAssets: %v", err)
	}
	return assets
}

func newTestRenderer(t *testing.T, fsys fs.FS) (*Renderer, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	r, err := NewRenderer(fsys, renderAssets(t), fortaleza(t), slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	return r, logs
}

func newCtx() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, rec
}

func renderPage(t *testing.T, r *Renderer, page string, data PageData) *httptest.ResponseRecorder {
	t.Helper()
	c, rec := newCtx()
	r.Page(c, http.StatusOK, page, data)
	return rec
}

// inOrder fails unless every needle appears in body, in the given order.
func inOrder(t *testing.T, body string, needles ...string) {
	t.Helper()
	pos := 0
	for _, n := range needles {
		i := strings.Index(body[pos:], n)
		if i < 0 {
			t.Errorf("%q not found (in order) in:\n%s", n, body)
			return
		}
		pos += i + len(n)
	}
}

var (
	styleAttr   = regexp.MustCompile(`(?i)\sstyle\s*=`)
	handlerAttr = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	scriptTag   = regexp.MustCompile(`(?i)<script\b[^>]*>`)
	srcAttr     = regexp.MustCompile(`(?i)\ssrc\s*=`)
)

// cspViolations lists what the CSP would block in rendered HTML: style
// attributes, event handler attributes and inline scripts.
func cspViolations(body string) []string {
	var found []string
	if styleAttr.MatchString(body) {
		found = append(found, "style attribute")
	}
	if m := handlerAttr.FindString(body); m != "" {
		found = append(found, "event handler attribute "+strings.TrimSpace(m))
	}
	for _, tag := range scriptTag.FindAllString(body, -1) {
		if !srcAttr.MatchString(tag) {
			found = append(found, "inline script "+tag)
		}
	}
	return found
}
