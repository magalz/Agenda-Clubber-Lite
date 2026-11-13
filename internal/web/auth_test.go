package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// --- helpers ---

// captureLogs sends the App's log to a buffer and returns it.
func captureLogs(app *App) *bytes.Buffer {
	logs := &bytes.Buffer{}
	app.Log = slog.New(slog.NewJSONHandler(logs, nil))
	return logs
}

// whoami answers who the session middleware found.
func whoami(app *App) gin.HandlerFunc {
	return func(c *gin.Context) {
		u, ok := app.currentUser(c)
		if !ok {
			c.String(http.StatusOK, "anon")
			return
		}
		c.String(http.StatusOK, "user=%s token=%s", u.ID, app.sessionToken(c))
	}
}

// authEngine wires the production middleware chain around routes of its own, so
// these tests do not depend on the handlers the flows register later:
//
//	public  /publica                                       (loadSession)
//	authed  /protegida /senha /senha/trocar /senha/trocar/x /senha/verificar /sair
//	                                                       (loadSession, requireAuth, requireFreshPassword)
//
// Each takes GET, HEAD and POST. extra adds more routes.
func authEngine(app *App, extra ...func(public, authed *gin.RouterGroup)) *gin.Engine {
	r := gin.New()
	r.Use(app.requestLog(), app.recovery(), limitBody(maxBodyBytes), app.sameOrigin())
	public := r.Group("", app.loadSession())
	authed := public.Group("", app.requireAuth(), app.requireFreshPassword())
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		public.Handle(m, "/publica", whoami(app))
		for _, p := range []string{"/protegida", "/senha", "/senha/trocar", "/senha/trocar/x", "/senha/verificar", "/sair"} {
			authed.Handle(m, p, whoami(app))
		}
	}
	for _, f := range extra {
		f(public, authed)
	}
	return r
}

func header(k, v string) func(*http.Request) {
	return func(req *http.Request) { req.Header.Set(k, v) }
}

// hx marks a request as an HTMX one.
func hx(req *http.Request) { req.Header.Set("HX-Request", "true") }

// post sends a form post from the app's own origin, with the mods applied.
func post(r http.Handler, target, body string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	mods := append([]func(*http.Request){
		withOrigin,
		header("Content-Type", "application/x-www-form-urlencoded"),
	}, mod...)
	return do(r, http.MethodPost, target, strings.NewReader(body), mods...)
}

func responseCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// checkCookieFlags fails unless the cookie meets the __Host- rules and carries
// the flags every session cookie has.
func checkCookieFlags(t *testing.T, c *http.Cookie) {
	t.Helper()
	if c.Name != auth.SessionCookieName {
		t.Errorf("Name = %q, want %q", c.Name, auth.SessionCookieName)
	}
	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
	if c.Domain != "" {
		t.Errorf("Domain = %q, want none (the __Host- prefix forbids one)", c.Domain)
	}
	if !c.HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if !c.Secure {
		t.Error("cookie is not Secure")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}
}

// wantCleared fails unless the response ends the session cookie: empty value,
// Max-Age=0 and the same attributes as the cookie it replaces.
func wantCleared(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	c := responseCookie(rec, auth.SessionCookieName)
	if c == nil {
		t.Fatalf("no Set-Cookie for %s; headers %v", auth.SessionCookieName, rec.Header())
	}
	checkCookieFlags(t, c)
	if c.Value != "" {
		t.Errorf("cleared cookie has a value: %q", c.Value)
	}
	if c.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want a negative one (Max-Age=0 on the wire)", c.MaxAge)
	}
	if got := strings.Join(rec.Header().Values("Set-Cookie"), "\n"); !strings.Contains(got, "Max-Age=0") {
		t.Errorf("Set-Cookie lacks Max-Age=0: %s", got)
	}
}

func wantNoCookie(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if got := rec.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("unexpected Set-Cookie: %v", got)
	}
}

// sessionGone reports whether the store has no session for the cookie value.
func sessionGone(t *testing.T, app *App, raw string) bool {
	t.Helper()
	hash, err := auth.HashToken(raw)
	if err != nil {
		t.Fatalf("HashToken(%q): %v", raw, err)
	}
	_, err = app.Store.Repos().Sessions.Get(context.Background(), hash)
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Sessions.Get: %v", err)
	}
	return errors.Is(err, domain.ErrNotFound)
}

// repoOverride changes the repositories a store hands out, to inject an error.
type repoOverride struct {
	domain.Store
	mutate func(*domain.Repos)
}

func (s repoOverride) Repos() domain.Repos {
	r := s.Store.Repos()
	s.mutate(&r)
	return r
}

type failingSessions struct{ domain.Sessions }

func (failingSessions) Get(context.Context, []byte) (domain.Session, error) {
	return domain.Session{}, errors.New("sessions are down")
}

type failingMemberships struct{ domain.Memberships }

func (failingMemberships) UserCanCreateEvents(context.Context, string) (bool, error) {
	return false, errors.New("memberships are down")
}

// countingStore counts how often the repositories are asked for, which is how a
// request that loads a session shows itself.
type countingStore struct {
	stubStore
	repos int
}

func (s *countingStore) Repos() domain.Repos {
	s.repos++
	return domain.Repos{}
}

// --- the Origin check ---

func TestSameOrigin(t *testing.T) {
	app := newTestApp(t)
	called := 0
	r := gin.New()
	r.Use(app.sameOrigin())
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r.Handle(m, "/x", func(c *gin.Context) { called++; c.String(http.StatusOK, "ok") })
	}
	origin := header("Origin", testOrigin)

	tests := []struct {
		name   string
		method string
		mod    []func(*http.Request)
		want   int
	}{
		{"post without Origin", http.MethodPost, nil, 403},
		{"post from the app origin", http.MethodPost, []func(*http.Request){origin}, 200},
		{"post from another origin", http.MethodPost, []func(*http.Request){header("Origin", "https://evil.test")}, 403},
		{"post from the same host over http", http.MethodPost, []func(*http.Request){header("Origin", "http://agenda.test")}, 403},
		{"post from another port", http.MethodPost, []func(*http.Request){header("Origin", "https://agenda.test:8443")}, 403},
		{"post from an opaque origin", http.MethodPost, []func(*http.Request){header("Origin", "null")}, 403},
		{"post from the origin with a trailing slash", http.MethodPost, []func(*http.Request){header("Origin", testOrigin+"/")}, 403},
		{"post with an empty Origin", http.MethodPost, []func(*http.Request){header("Origin", "")}, 403},
		{"post marked cross-site", http.MethodPost, []func(*http.Request){origin, header("Sec-Fetch-Site", "cross-site")}, 403},
		{"post marked same-origin", http.MethodPost, []func(*http.Request){origin, header("Sec-Fetch-Site", "same-origin")}, 200},
		{"post marked same-site", http.MethodPost, []func(*http.Request){origin, header("Sec-Fetch-Site", "same-site")}, 200},
		{"post marked none", http.MethodPost, []func(*http.Request){origin, header("Sec-Fetch-Site", "none")}, 200},
		{"put without Origin", http.MethodPut, nil, 403},
		{"put from the app origin", http.MethodPut, []func(*http.Request){origin}, 200},
		{"patch without Origin", http.MethodPatch, nil, 403},
		{"delete without Origin", http.MethodDelete, nil, 403},
		{"delete from the app origin", http.MethodDelete, []func(*http.Request){origin}, 200},
		{"get without Origin", http.MethodGet, nil, 200},
		{"get from another origin", http.MethodGet, []func(*http.Request){header("Origin", "https://evil.test")}, 200},
		{"get marked cross-site", http.MethodGet, []func(*http.Request){header("Sec-Fetch-Site", "cross-site")}, 200},
		{"head marked cross-site", http.MethodHead, []func(*http.Request){header("Sec-Fetch-Site", "cross-site")}, 200},
		{"options without Origin", http.MethodOptions, nil, 200},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := called
			rec := do(r, tt.method, "/x", nil, tt.mod...)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, tt.want, rec.Body)
			}
			if tt.want == 403 {
				if rec.Body.String() != "origem não permitida" {
					t.Errorf("body = %q, want %q", rec.Body.String(), "origem não permitida")
				}
				if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
					t.Errorf("Content-Type = %q, want text/plain", got)
				}
				if called != before {
					t.Error("the handler ran for a refused request")
				}
			}
		})
	}
}

// The check also covers a route that does not exist, and the refusal still
// carries the security headers and is logged.
func TestSameOriginInTheRouter(t *testing.T) {
	r, logs := newRouter(t)

	rec := do(r, http.MethodPost, "/nao-existe", nil)
	if rec.Code != http.StatusForbidden || rec.Body.String() != "origem não permitida" {
		t.Fatalf("got %d %q, want 403 %q", rec.Code, rec.Body, "origem não permitida")
	}
	checkSecurityHeaders(t, rec.Header())
	if got := requestLine(t, logs)["status"]; got != float64(403) {
		t.Errorf("logged status = %v, want 403", got)
	}

	if rec := do(r, http.MethodPost, "/nao-existe", nil, withOrigin); rec.Code != http.StatusNotFound {
		t.Errorf("post from the app origin to an unknown route = %d, want 404", rec.Code)
	}
}

// A cross-site post is refused before any session is looked up. The test goes
// through the real router, to routes it really has (an unknown path never reaches
// the session middleware, whatever the order), with a cookie that is well formed,
// so a session middleware that ran first would ask the store about it.
func TestSameOriginRunsBeforeTheSession(t *testing.T) {
	store := &countingStore{}
	app := NewApp(testConfig(t), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), store)
	r := app.Router()
	raw, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	cookie := &http.Cookie{Name: auth.SessionCookieName, Value: raw}
	form := header("Content-Type", "application/x-www-form-urlencoded")

	// The premise: from the app's own origin the same request does reach the
	// store (which has no sessions, so the request ends in a 500).
	if rec := do(r, http.MethodPost, "/entrar", strings.NewReader(""), form, withOrigin, withCookie(cookie)); rec.Code != http.StatusInternalServerError || store.repos == 0 {
		t.Fatalf("POST /entrar from the app's origin = %d with %d store lookups, want a 500 after a lookup: the cookie does not reach the session middleware", rec.Code, store.repos)
	}

	for _, path := range []string{"/entrar", "/senha/trocar", "/sair"} {
		for name, origin := range map[string]func(*http.Request){
			"no Origin":    func(*http.Request) {},
			"other Origin": header("Origin", "https://evil.test"),
		} {
			store.repos = 0
			rec := do(r, http.MethodPost, path, strings.NewReader(""), form, origin, withCookie(cookie))
			if rec.Code != http.StatusForbidden {
				t.Errorf("POST %s with %s = %d, want 403", path, name, rec.Code)
			}
			if store.repos != 0 {
				t.Errorf("POST %s with %s: the session was looked up %d times before the Origin check", path, name, store.repos)
			}
		}
	}
}

// --- no-store pages ---

// Every page of the app says it must not be stored: the login form carries a
// ?next= and the pages of a signed-in person show who they are. Healthz sets its
// own header, and static files have their own policy (see the assets tests).
func TestPagesAreNotStored(t *testing.T) {
	app, store := newStoreApp(t)
	r := app.Router()
	cookie := loginAs(t, app, insertWebUser(t, store, "ana@agenda.test"))
	form := header("Content-Type", "application/x-www-form-urlencoded")

	for _, tt := range []struct {
		name   string
		method string
		target string
		body   string
		mods   []func(*http.Request)
		want   int
	}{
		{"login form", http.MethodGet, "/entrar", "", nil, http.StatusOK},
		{"login failure", http.MethodPost, "/entrar", "email=ninguem%40agenda.test&password=x", []func(*http.Request){withOrigin, form}, http.StatusUnauthorized},
		{"confirmation link", http.MethodGet, "/confirmar?token=x", "", nil, http.StatusOK},
		{"reset link", http.MethodGet, "/senha/redefinir?token=x", "", nil, http.StatusOK},
		{"calendar of a signed-in person", http.MethodGet, "/", "", []func(*http.Request){withCookie(cookie)}, http.StatusOK},
		{"change password form", http.MethodGet, "/senha/trocar", "", []func(*http.Request){withCookie(cookie)}, http.StatusOK},
		{"change password refusal", http.MethodPost, "/senha/trocar", "current_password=&password=&password_confirm=", []func(*http.Request){withOrigin, form, withCookie(cookie)}, http.StatusUnprocessableEntity},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(r, tt.method, tt.target, strings.NewReader(tt.body), tt.mods...)
			if rec.Code != tt.want {
				t.Fatalf("%s %s = %d, want %d", tt.method, tt.target, rec.Code, tt.want)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

// --- the session middleware ---

func TestLoadSession(t *testing.T) {
	app, store := newStoreApp(t)
	logs := captureLogs(app)
	r := authEngine(app)
	ana := insertWebUser(t, store, "ana@agenda.test")
	var used []string // every cookie value sent, to look for in the log

	send := func(c *http.Cookie) *httptest.ResponseRecorder {
		used = append(used, c.Value)
		return do(r, http.MethodGet, "/publica", nil, withCookie(c))
	}

	t.Run("no cookie is anonymous", func(t *testing.T) {
		rec := do(r, http.MethodGet, "/publica", nil)
		if rec.Body.String() != "anon" {
			t.Errorf("body = %q, want anon", rec.Body)
		}
		wantNoCookie(t, rec)
	})

	t.Run("a live session loads its user", func(t *testing.T) {
		c := loginAs(t, app, ana)
		rec := send(c)
		if want := "user=" + ana.ID + " token=" + c.Value; rec.Body.String() != want {
			t.Errorf("body = %q, want %q", rec.Body, want)
		}
		wantNoCookie(t, rec)
	})

	t.Run("a malformed cookie is anonymous and cleared", func(t *testing.T) {
		rec := send(&http.Cookie{Name: auth.SessionCookieName, Value: "not-a-token"})
		if rec.Body.String() != "anon" {
			t.Errorf("body = %q, want anon", rec.Body)
		}
		wantCleared(t, rec)
	})

	t.Run("an unknown token is anonymous and cleared", func(t *testing.T) {
		raw, _, err := auth.NewToken()
		if err != nil {
			t.Fatal(err)
		}
		rec := send(&http.Cookie{Name: auth.SessionCookieName, Value: raw})
		if rec.Body.String() != "anon" {
			t.Errorf("body = %q, want anon", rec.Body)
		}
		wantCleared(t, rec)
	})

	t.Run("a session past its idle timeout is anonymous, cleared and deleted", func(t *testing.T) {
		c := loginAs(t, app, ana)
		app.Now = func() time.Time { return webTestNow.Add(auth.SessionIdleTimeout + time.Hour) }
		defer func() { app.Now = func() time.Time { return webTestNow } }()
		rec := send(c)
		if rec.Body.String() != "anon" {
			t.Errorf("body = %q, want anon", rec.Body)
		}
		wantCleared(t, rec)
		if !sessionGone(t, app, c.Value) {
			t.Error("the expired session is still stored")
		}
	})

	for _, status := range []domain.UserStatus{domain.UserStatusPendingEmail, domain.UserStatusPendingApproval, domain.UserStatusDisabled} {
		t.Run("a "+string(status)+" user is signed out", func(t *testing.T) {
			u := insertWebUser(t, store, string(status)+"@agenda.test", func(u *domain.User) { u.Status = status })
			c := loginAs(t, app, u)
			rec := send(c)
			if rec.Body.String() != "anon" {
				t.Errorf("body = %q, want anon", rec.Body)
			}
			wantCleared(t, rec)
			if !sessionGone(t, app, c.Value) {
				t.Error("the session of an inactive user is still stored")
			}
		})
	}

	t.Run("a store error is a 500 that logs no token", func(t *testing.T) {
		broken := *app
		broken.Store = repoOverride{store, func(r *domain.Repos) { r.Sessions = failingSessions{r.Sessions} }}
		c := loginAs(t, app, ana)
		used = append(used, c.Value)
		rec := do(authEngine(&broken), http.MethodGet, "/publica", nil, withCookie(c))
		if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
			t.Errorf("got %d %q, want 500 %q", rec.Code, rec.Body, "erro interno")
		}
		if !strings.Contains(logs.String(), "sessions are down") {
			t.Errorf("the failure is not logged: %s", logs)
		}
	})

	for _, v := range used {
		if strings.Contains(logs.String(), v) {
			t.Errorf("the log holds the session cookie value %q", v)
		}
	}
}

// /healthz and /static never load a session, with or without a cookie.
func TestHealthzAndStaticNeverLoadASession(t *testing.T) {
	store := &countingStore{}
	app := NewApp(testConfig(t), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), store)
	r := app.Router()
	cookie := &http.Cookie{Name: auth.SessionCookieName, Value: "whatever"}

	for _, tt := range []struct{ method, target string }{
		{http.MethodGet, "/healthz"},
		{http.MethodGet, "/static/css/base.css"},
		{http.MethodHead, "/static/css/base.css"},
	} {
		rec := do(r, tt.method, tt.target, nil, withCookie(cookie))
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s = %d, want 200", tt.method, tt.target, rec.Code)
		}
	}
	if store.repos != 0 {
		t.Errorf("the store was asked for its repositories %d times, want 0", store.repos)
	}
}

// --- access rules ---

func TestRequireAuth(t *testing.T) {
	app := newTestApp(t) // anonymous requests never reach its store
	r := authEngine(app)
	next := func(target string) string { return "/entrar?next=" + url.QueryEscape(target) }

	tests := []struct {
		name       string
		method     string
		target     string
		mod        []func(*http.Request)
		wantStatus int
		wantHeader [2]string // name, value
	}{
		{"get goes to the login with next", http.MethodGet, "/protegida", nil, 303, [2]string{"Location", next("/protegida")}},
		{"get keeps the query in next", http.MethodGet, "/protegida?x=1&y=a%20b", nil, 303, [2]string{"Location", next("/protegida?x=1&y=a%20b")}},
		{"head goes to the login with next", http.MethodHead, "/protegida", nil, 303, [2]string{"Location", next("/protegida")}},
		{"post goes to the login without next", http.MethodPost, "/protegida", []func(*http.Request){withOrigin}, 303, [2]string{"Location", "/entrar"}},
		{"htmx get is a 401 with HX-Redirect", http.MethodGet, "/protegida", []func(*http.Request){hx}, 401, [2]string{"HX-Redirect", "/entrar"}},
		{"htmx post is a 401 with HX-Redirect", http.MethodPost, "/protegida", []func(*http.Request){withOrigin, hx}, 401, [2]string{"HX-Redirect", "/entrar"}},
		{"the password pages need a session too", http.MethodGet, "/senha/trocar", nil, 303, [2]string{"Location", next("/senha/trocar")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(r, tt.method, tt.target, nil, tt.mod...)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get(tt.wantHeader[0]); got != tt.wantHeader[1] {
				t.Errorf("%s = %q, want %q", tt.wantHeader[0], got, tt.wantHeader[1])
			}
			if tt.wantStatus == 401 && rec.Header().Get("Location") != "" {
				t.Errorf("an HTMX answer carries Location %q", rec.Header().Get("Location"))
			}
		})
	}

	t.Run("a public route needs no session", func(t *testing.T) {
		if rec := do(r, http.MethodGet, "/publica", nil); rec.Code != 200 || rec.Body.String() != "anon" {
			t.Errorf("got %d %q, want 200 anon", rec.Code, rec.Body)
		}
	})
}

func TestRequireFreshPassword(t *testing.T) {
	app, store := newStoreApp(t)
	r := authEngine(app)
	expired := insertWebUser(t, store, "velha@agenda.test", func(u *domain.User) { u.PasswordChangedAt = webTestNow.Add(-auth.MaxAge) })
	fresh := insertWebUser(t, store, "nova@agenda.test", func(u *domain.User) { u.PasswordChangedAt = webTestNow.Add(-auth.MaxAge + time.Minute) })
	if !auth.PasswordExpired(expired.PasswordChangedAt, webTestNow) || auth.PasswordExpired(fresh.PasswordChangedAt, webTestNow) {
		t.Fatal("the fixtures do not sit on each side of the expiry")
	}
	expiredCookie, freshCookie := loginAs(t, app, expired), loginAs(t, app, fresh)

	t.Run("an expired password may only reach the change, check and logout paths", func(t *testing.T) {
		for _, target := range []string{"/senha/trocar", "/senha/trocar?x=1", "/senha/verificar", "/sair"} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				rec := do(r, method, target, nil, withOrigin, withCookie(expiredCookie))
				if rec.Code != 200 || rec.Body.String() != "user="+expired.ID+" token="+expiredCookie.Value {
					t.Errorf("%s %s = %d %q, want 200 for the signed-in user", method, target, rec.Code, rec.Body)
				}
			}
		}
	})

	t.Run("an expired password is sent to the change page from everywhere else", func(t *testing.T) {
		for _, target := range []string{"/protegida", "/protegida?next=/senha/trocar", "/senha", "/senha/trocar/x"} {
			for _, method := range []string{http.MethodGet, http.MethodPost} {
				rec := do(r, method, target, nil, withOrigin, withCookie(expiredCookie))
				if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/senha/trocar" {
					t.Errorf("%s %s = %d Location %q, want 303 /senha/trocar", method, target, rec.Code, rec.Header().Get("Location"))
				}
			}
		}
	})

	t.Run("htmx is told to redirect", func(t *testing.T) {
		rec := do(r, http.MethodGet, "/protegida", nil, hx, withCookie(expiredCookie))
		if rec.Code != 401 || rec.Header().Get("HX-Redirect") != "/senha/trocar" || rec.Header().Get("Location") != "" {
			t.Errorf("got %d HX-Redirect %q Location %q, want 401 /senha/trocar and no Location",
				rec.Code, rec.Header().Get("HX-Redirect"), rec.Header().Get("Location"))
		}
	})

	t.Run("a password a minute short of expiry reaches everything", func(t *testing.T) {
		for _, target := range []string{"/protegida", "/senha", "/senha/trocar/x"} {
			if rec := do(r, http.MethodGet, target, nil, withCookie(freshCookie)); rec.Code != 200 {
				t.Errorf("GET %s = %d, want 200", target, rec.Code)
			}
		}
	})
}

func TestRequireAdmin(t *testing.T) {
	app, store := newStoreApp(t)
	admin := insertWebUser(t, store, "admin@agenda.test", func(u *domain.User) { u.IsAdmin = true })
	member := insertWebUser(t, store, "membro@agenda.test")
	r := authEngine(app, func(public, authed *gin.RouterGroup) {
		public.GET("/admin-publica", app.requireAdmin(), whoami(app))
		authed.GET("/admin-protegida", app.requireAdmin(), whoami(app))
	})

	tests := []struct {
		name   string
		target string
		cookie *http.Cookie
		want   int
	}{
		{"an admin passes", "/admin-protegida", loginAs(t, app, admin), 200},
		{"an admin passes on a public route", "/admin-publica", loginAs(t, app, admin), 200},
		{"a member is refused", "/admin-protegida", loginAs(t, app, member), 403},
		{"an anonymous visitor on a public route is refused", "/admin-publica", nil, 403},
		{"an anonymous visitor on a protected route is sent to the login", "/admin-protegida", nil, 303},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mod []func(*http.Request)
			if tt.cookie != nil {
				mod = append(mod, withCookie(tt.cookie))
			}
			rec := do(r, http.MethodGet, tt.target, nil, mod...)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if tt.want == 403 {
				if rec.Body.String() != "acesso negado" {
					t.Errorf("body = %q, want %q", rec.Body, "acesso negado")
				}
				if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
					t.Errorf("Content-Type = %q, want text/plain", got)
				}
			}
		})
	}
}

// --- signing in and out ---

func TestSignIn(t *testing.T) {
	app, store := newStoreApp(t)
	ana := insertWebUser(t, store, "ana@agenda.test")
	bia := insertWebUser(t, store, "bia@agenda.test")
	r := authEngine(app, func(public, _ *gin.RouterGroup) {
		public.POST("/entrar-sonda", func(c *gin.Context) {
			if err := app.signIn(c, c.Query("id")); err != nil {
				c.String(http.StatusInternalServerError, "sign-in failed")
				return
			}
			c.String(http.StatusOK, "signed-in")
		})
	})

	t.Run("starts a session and sets the cookie", func(t *testing.T) {
		rec := post(r, "/entrar-sonda?id="+ana.ID, "", header("User-Agent", "sonda/1.0"))
		if rec.Code != 200 || rec.Body.String() != "signed-in" {
			t.Fatalf("got %d %q", rec.Code, rec.Body)
		}
		c := responseCookie(rec, auth.SessionCookieName)
		if c == nil {
			t.Fatalf("no session cookie; headers %v", rec.Header())
		}
		checkCookieFlags(t, c)
		if want := int(auth.SessionMaxLifetime / time.Second); c.MaxAge != want {
			t.Errorf("MaxAge = %d, want %d", c.MaxAge, want)
		}
		s, err := auth.ResolveSession(context.Background(), app.Store.Repos().Sessions, c.Value, app.Now())
		if err != nil {
			t.Fatalf("the cookie value is not a live session: %v", err)
		}
		if s.UserID != ana.ID {
			t.Errorf("session user = %q, want %q", s.UserID, ana.ID)
		}
		if s.UserAgent != "sonda/1.0" {
			t.Errorf("session user agent = %q, want sonda/1.0", s.UserAgent)
		}
		if rec2 := do(r, http.MethodGet, "/publica", nil, withCookie(c)); rec2.Body.String() != "user="+ana.ID+" token="+c.Value {
			t.Errorf("the new cookie does not sign Ana in: %q", rec2.Body)
		}
	})

	t.Run("the cookie stays Secure in dev", func(t *testing.T) {
		dev := *app
		dev.Cfg.Env = "dev"
		rec := post(authEngine(&dev, func(public, _ *gin.RouterGroup) {
			public.POST("/entrar-sonda", func(c *gin.Context) { _ = dev.signIn(c, ana.ID); c.Status(http.StatusOK) })
		}), "/entrar-sonda", "")
		c := responseCookie(rec, auth.SessionCookieName)
		if c == nil {
			t.Fatal("no session cookie")
		}
		checkCookieFlags(t, c)
	})

	t.Run("ends the session the request came with", func(t *testing.T) {
		old := loginAs(t, app, ana)
		rec := post(r, "/entrar-sonda?id="+bia.ID, "", withCookie(old))
		c := responseCookie(rec, auth.SessionCookieName)
		if c == nil {
			t.Fatalf("no session cookie; body %q", rec.Body)
		}
		if c.Value == old.Value {
			t.Error("the session token was not rotated")
		}
		if !sessionGone(t, app, old.Value) {
			t.Error("the previous session is still stored")
		}
		if s, err := auth.ResolveSession(context.Background(), app.Store.Repos().Sessions, c.Value, app.Now()); err != nil || s.UserID != bia.ID {
			t.Errorf("new session = %+v, %v; want one for Bia", s, err)
		}
	})

	t.Run("a garbage cookie is replaced without an error", func(t *testing.T) {
		rec := post(r, "/entrar-sonda?id="+ana.ID, "", withCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "garbage"}))
		// loadSession cleared the stale cookie first; the response must carry
		// only the new one, not the clearing header before it.
		headers := rec.Header().Values("Set-Cookie")
		if rec.Code != 200 || len(headers) != 1 {
			t.Fatalf("got %d, Set-Cookie %v; want 200 and exactly one", rec.Code, headers)
		}
		if c := responseCookie(rec, auth.SessionCookieName); c == nil || c.Value == "" || c.Value == "garbage" || c.MaxAge <= 0 {
			t.Errorf("cookie = %+v, want a fresh session cookie", c)
		}
	})

	t.Run("an unknown user is an error and sets no cookie", func(t *testing.T) {
		rec := post(r, "/entrar-sonda?id=nobody", "")
		if rec.Code != http.StatusInternalServerError || rec.Body.String() != "sign-in failed" {
			t.Errorf("got %d %q, want the handler's 500", rec.Code, rec.Body)
		}
		wantNoCookie(t, rec)
	})
}

func TestSignOut(t *testing.T) {
	app, store := newStoreApp(t)
	ana := insertWebUser(t, store, "ana@agenda.test")
	r := authEngine(app, func(public, _ *gin.RouterGroup) {
		public.POST("/sair-sonda", func(c *gin.Context) {
			if err := app.signOut(c); err != nil {
				c.String(http.StatusInternalServerError, "sign-out failed")
				return
			}
			c.String(http.StatusOK, "signed-out")
		})
	})

	t.Run("deletes the session and clears the cookie", func(t *testing.T) {
		c := loginAs(t, app, ana)
		rec := post(r, "/sair-sonda", "", withCookie(c))
		if rec.Code != 200 || rec.Body.String() != "signed-out" {
			t.Fatalf("got %d %q", rec.Code, rec.Body)
		}
		wantCleared(t, rec)
		if !sessionGone(t, app, c.Value) {
			t.Error("the session is still stored")
		}
		if rec := do(r, http.MethodGet, "/publica", nil, withCookie(c)); rec.Body.String() != "anon" {
			t.Errorf("the old cookie still signs in: %q", rec.Body)
		}
	})

	t.Run("another session of the user survives", func(t *testing.T) {
		keep, drop := loginAs(t, app, ana), loginAs(t, app, ana)
		post(r, "/sair-sonda", "", withCookie(drop))
		if sessionGone(t, app, keep.Value) {
			t.Error("signing out one session ended another")
		}
	})

	t.Run("without a cookie it still clears and does not fail", func(t *testing.T) {
		rec := post(r, "/sair-sonda", "")
		if rec.Code != 200 {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		wantCleared(t, rec)
	})
}

// --- redirects, next and forms ---

func TestSafeNext(t *testing.T) {
	tests := []struct{ in, want string }{
		{"/", "/"},
		{"/eventos/novo", "/eventos/novo"},
		{"/meus-eventos?pagina=2", "/meus-eventos?pagina=2"},
		{"/agenda?mes=2026-11&q=a%20b", "/agenda?mes=2026-11&q=a%20b"},
		{"", "/"},
		{"entrar", "/"},
		{"//evil.test", "/"},
		{"//evil.test/x", "/"},
		{"///evil.test", "/"},
		{"/\\evil.test", "/"},
		{"/\\/evil.test", "/"},
		{"\\evil.test", "/"},
		{"/a\\b", "/"},
		{"http://evil.test", "/"},
		{"https://evil.test/x", "/"},
		{"javascript:alert(1)", "/"},
		{"mailto:a@b.test", "/"},
		{" /x", "/"},
		{"/x\n", "/"},
		{"/x\r\nSet-Cookie: a=b", "/"},
		{"/x\ty", "/"},
		{"/x\x00y", "/"},
		{"/x\x7fy", "/"},
		{"/%zz", "/"},
	}
	for _, tt := range tests {
		if got := safeNext(tt.in); got != tt.want {
			t.Errorf("safeNext(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestRedirect(t *testing.T) {
	app := newTestApp(t)
	r := gin.New()
	r.GET("/r", func(c *gin.Context) { app.redirect(c, "/destino") })

	rec := do(r, http.MethodGet, "/r", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/destino" {
		t.Errorf("plain request: %d Location %q, want 303 /destino", rec.Code, rec.Header().Get("Location"))
	}
	if rec.Header().Get("HX-Redirect") != "" {
		t.Errorf("plain request carries HX-Redirect %q", rec.Header().Get("HX-Redirect"))
	}

	rec = do(r, http.MethodGet, "/r", nil, hx)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/destino" {
		t.Errorf("htmx request: %d HX-Redirect %q, want 204 /destino", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if rec.Header().Get("Location") != "" {
		t.Errorf("htmx request carries Location %q", rec.Header().Get("Location"))
	}

	rec = do(r, http.MethodGet, "/r", nil, header("HX-Request", "false"))
	if rec.Code != http.StatusSeeOther {
		t.Errorf("HX-Request: false = %d, want 303", rec.Code)
	}
}

func TestParseForm(t *testing.T) {
	app := newTestApp(t)
	r := gin.New()
	r.Use(limitBody(maxBodyBytes))
	r.POST("/f", func(c *gin.Context) {
		v, err := app.parseForm(c)
		if err != nil {
			c.String(http.StatusBadRequest, "bad form")
			return
		}
		c.JSON(http.StatusOK, v)
	})
	form := header("Content-Type", "application/x-www-form-urlencoded")

	tests := []struct {
		name   string
		target string
		body   string
		mod    []func(*http.Request)
		want   url.Values // nil: an error
	}{
		{"fields and repeated fields", "/f", "a=1&b=x&b=y", nil, url.Values{"a": {"1"}, "b": {"x", "y"}}},
		{"valid UTF-8", "/f", "nome=caf%C3%A9+%E2%9C%93", nil, url.Values{"nome": {"café ✓"}}},
		{"the query string is not part of the form", "/f?q=1", "a=1", nil, url.Values{"a": {"1"}}},
		{"an empty body", "/f", "", nil, url.Values{}},
		{"invalid UTF-8 in a value", "/f", "a=%ff", nil, nil},
		{"invalid UTF-8 in a key", "/f", "%ff=1", nil, nil},
		{"a truncated UTF-8 sequence", "/f", "a=%c3", nil, nil},
		{"a malformed escape", "/f", "a=%zz", nil, nil},
		{"a body over the limit without a declared length", "/f", "a=" + strings.Repeat("x", maxBodyBytes+1), []func(*http.Request){chunked}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(r, http.MethodPost, tt.target, strings.NewReader(tt.body), append([]func(*http.Request){form}, tt.mod...)...)
			if tt.want == nil {
				if rec.Code != http.StatusBadRequest {
					t.Errorf("status = %d, want the handler's 400 for a parse error (body %q)", rec.Code, rec.Body)
				}
				return
			}
			if rec.Code != 200 {
				t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body)
			}
			var got url.Values
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode %q: %v", rec.Body, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("form = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- what the templates get ---

func TestViewUser(t *testing.T) {
	app, store := newStoreApp(t)
	logs := captureLogs(app)
	ctx := context.Background()
	repos := store.Repos()

	plain := insertWebUser(t, store, "plain@agenda.test")
	admin := insertWebUser(t, store, "admin@agenda.test", func(u *domain.User) { u.IsAdmin = true })
	owner := insertWebUser(t, store, "owner@agenda.test")
	pending := insertWebUser(t, store, "pending@agenda.test")
	warned := insertWebUser(t, store, "warned@agenda.test", func(u *domain.User) { u.PasswordChangedAt = webTestNow.Add(-(auth.MaxAge - 3*24*time.Hour)) })
	farOut := insertWebUser(t, store, "farout@agenda.test", func(u *domain.User) { u.PasswordChangedAt = webTestNow.Add(-(auth.MaxAge - 8*24*time.Hour)) })

	collective := domain.Collective{
		Name: "Coletivo Teste", NameKey: domain.NameKey("Coletivo Teste"), Kind: domain.CollectiveKindColetivo,
		Status: domain.CollectiveStatusApproved, CreatedBy: owner.ID, CreatedAt: webTestNow, UpdatedAt: webTestNow,
	}
	if err := repos.Collectives.Create(ctx, &collective); err != nil {
		t.Fatal(err)
	}
	for _, m := range []struct {
		user   domain.User
		status domain.MembershipStatus
	}{{owner, domain.MembershipStatusApproved}, {pending, domain.MembershipStatusPending}} {
		err := repos.Memberships.Create(ctx, domain.Membership{
			UserID: m.user.ID, CollectiveID: collective.ID, Role: domain.RoleOwner, Status: m.status, RequestedAt: webTestNow,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	r := authEngine(app, func(public, _ *gin.RouterGroup) {
		public.GET("/eu", func(c *gin.Context) { c.JSON(http.StatusOK, app.viewUser(c)) })
	})
	get := func(u *domain.User) *ViewUser {
		t.Helper()
		var mod []func(*http.Request)
		if u != nil {
			mod = append(mod, withCookie(loginAs(t, app, *u)))
		}
		rec := do(r, http.MethodGet, "/eu", nil, mod...)
		if rec.Code != 200 {
			t.Fatalf("status = %d, body %q", rec.Code, rec.Body)
		}
		var v *ViewUser
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("decode %q: %v", rec.Body, err)
		}
		return v
	}

	if v := get(nil); v != nil {
		t.Errorf("anonymous: %+v, want nil", v)
	}
	tests := []struct {
		name string
		user domain.User
		want ViewUser
	}{
		{"a plain member", plain, ViewUser{ID: plain.ID, Name: plain.Name}},
		{"an admin", admin, ViewUser{ID: admin.ID, Name: admin.Name, IsAdmin: true}},
		{"an approved owner of an approved collective", owner, ViewUser{ID: owner.ID, Name: owner.Name, CanCreateEvents: true}},
		{"an owner whose membership is pending", pending, ViewUser{ID: pending.ID, Name: pending.Name}},
		{"a password three days from expiry", warned, ViewUser{ID: warned.ID, Name: warned.Name, PasswordDaysLeft: 3}},
		{"a password eight days from expiry gets no warning", farOut, ViewUser{ID: farOut.ID, Name: farOut.Name}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if v := get(&tt.user); v == nil || *v != tt.want {
				t.Errorf("viewUser = %+v, want %+v", v, tt.want)
			}
		})
	}

	t.Run("a membership lookup error reads as false and is logged", func(t *testing.T) {
		broken := *app
		broken.Store = repoOverride{store, func(r *domain.Repos) { r.Memberships = failingMemberships{r.Memberships} }}
		rb := authEngine(&broken, func(public, _ *gin.RouterGroup) {
			public.GET("/eu", func(c *gin.Context) { c.JSON(http.StatusOK, broken.viewUser(c)) })
		})
		rec := do(rb, http.MethodGet, "/eu", nil, withCookie(loginAs(t, app, owner)))
		var v *ViewUser
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v == nil {
			t.Fatalf("decode %q: %v", rec.Body, err)
		}
		if v.CanCreateEvents {
			t.Error("CanCreateEvents = true after a lookup error")
		}
		if !strings.Contains(logs.String(), "memberships are down") {
			t.Errorf("the error is not logged: %s", logs)
		}
	})
}

func TestPage(t *testing.T) {
	app, store := newStoreApp(t)
	ana := insertWebUser(t, store, "ana@agenda.test", func(u *domain.User) { u.Name = "Ana Souza" })
	r := authEngine(app, func(public, _ *gin.RouterGroup) {
		public.GET("/pagina", func(c *gin.Context) {
			app.page(c, http.StatusCreated, "app/home", PageData{Title: "Calendário", Nav: "calendario"})
		})
		public.GET("/pagina-com-usuario", func(c *gin.Context) {
			app.page(c, http.StatusOK, "app/home", PageData{Title: "Calendário", Nav: "calendario", User: &ViewUser{ID: "x", Name: "Explícita"}})
		})
	})

	rec := do(r, http.MethodGet, "/pagina", nil, withCookie(loginAs(t, app, ana)))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "Ana Souza") || !strings.Contains(body, `action="/sair"`) {
		t.Errorf("the page does not show the signed-in user: %s", body)
	}

	rec = do(r, http.MethodGet, "/pagina", nil)
	if body := rec.Body.String(); rec.Code != http.StatusCreated || strings.Contains(body, `action="/sair"`) {
		t.Errorf("anonymous: %d, shows a logout form: %v", rec.Code, strings.Contains(body, `action="/sair"`))
	}

	rec = do(r, http.MethodGet, "/pagina-com-usuario", nil, withCookie(loginAs(t, app, ana)))
	if body := rec.Body.String(); !strings.Contains(body, "Explícita") || strings.Contains(body, "Ana Souza") {
		t.Errorf("page replaced the user it was given: %s", body)
	}
}

func TestFormPage(t *testing.T) {
	app := newTestApp(t)
	renderer, logs := newTestRenderer(t, shippedTemplates(t, []string{"auth"}, map[string]string{
		"pages/auth/x.html": `{{define "content"}}<p id="v">{{.Form.Get "email"}}</p>{{template "field-error" (dict "Errors" .Errors "Field" "email")}}{{end}}`,
	}))
	app.Render = renderer
	r := authEngine(app, func(public, _ *gin.RouterGroup) {
		public.POST("/formulario", func(c *gin.Context) {
			app.formPage(c, "auth/x", PageData{Title: "Entrar", Form: url.Values{"email": {"ana@agenda.test"}}},
				domain.FieldErrors{"email": "informe um e-mail <válido>"})
		})
	})

	rec := post(r, "/formulario", "")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; log %s", rec.Code, logs)
	}
	wantAll(t, "form page", rec.Body.String(),
		"<title>Entrar · AGENDA CLUBBER</title>",
		`<p id="v">ana@agenda.test</p>`,
		`<p class="field-error" id="erro-email">informe um e-mail &lt;válido&gt;</p>`)
}

func TestFieldErrorPartial(t *testing.T) {
	app := newTestApp(t)
	render := func(data map[string]any) string {
		t.Helper()
		c, rec := newCtx()
		app.Render.Partial(c, http.StatusOK, "field-error", data)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %q", rec.Code, rec.Body)
		}
		return strings.TrimSpace(rec.Body.String())
	}

	if got, want := render(map[string]any{"Errors": domain.FieldErrors{"email": "e-mail inválido"}, "Field": "email"}),
		`<p class="field-error" id="erro-email">e-mail inválido</p>`; got != want {
		t.Errorf("with a message: %q, want %q", got, want)
	}
	if got := render(map[string]any{"Errors": domain.FieldErrors{"email": "<b>x</b> & y"}, "Field": "email"}); !strings.Contains(got, "&lt;b&gt;x&lt;/b&gt; &amp; y") {
		t.Errorf("the message is not escaped: %q", got)
	}
	for name, errs := range map[string]any{
		"an error for another field": domain.FieldErrors{"nome": "obrigatório"},
		"empty errors":               domain.FieldErrors{},
		"nil errors":                 domain.FieldErrors(nil),
		"untyped nil errors":         nil,
		"an empty message":           domain.FieldErrors{"email": ""},
	} {
		if got := render(map[string]any{"Errors": errs, "Field": "email"}); got != "" {
			t.Errorf("%s: %q, want nothing", name, got)
		}
	}
}

// --- the router ---

func TestHomeNeedsASession(t *testing.T) {
	store := &countingStore{}
	app := NewApp(testConfig(t), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), store)
	r := app.Router()

	rec := do(r, http.MethodGet, "/", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar" {
		t.Errorf("got %d Location %q, want 303 /entrar (no next for the home page)", rec.Code, rec.Header().Get("Location"))
	}
	checkSecurityHeaders(t, rec.Header())

	rec = do(r, http.MethodGet, "/", nil, hx)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("HX-Redirect") != "/entrar" {
		t.Errorf("htmx: %d HX-Redirect %q, want 401 /entrar", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if store.repos != 0 {
		t.Errorf("a request without a cookie asked the store for its repositories %d times, want 0", store.repos)
	}
}

func TestHomeForASignedInUser(t *testing.T) {
	app, store := newStoreApp(t)
	r := app.Router()
	repos := store.Repos()
	ctx := context.Background()

	member := insertWebUser(t, store, "membro@agenda.test", func(u *domain.User) { u.Name = "Maria Membra" })
	admin := insertWebUser(t, store, "admin@agenda.test", func(u *domain.User) { u.Name = "Ada Admin"; u.IsAdmin = true })
	owner := insertWebUser(t, store, "dono@agenda.test", func(u *domain.User) { u.Name = "Davi Dono" })
	expired := insertWebUser(t, store, "velha@agenda.test", func(u *domain.User) { u.PasswordChangedAt = webTestNow.Add(-auth.MaxAge) })
	collective := domain.Collective{
		Name: "Coletivo Teste", NameKey: domain.NameKey("Coletivo Teste"), Kind: domain.CollectiveKindColetivo,
		Status: domain.CollectiveStatusApproved, CreatedBy: owner.ID, CreatedAt: webTestNow, UpdatedAt: webTestNow,
	}
	if err := repos.Collectives.Create(ctx, &collective); err != nil {
		t.Fatal(err)
	}
	if err := repos.Memberships.Create(ctx, domain.Membership{
		UserID: owner.ID, CollectiveID: collective.ID, Role: domain.RoleOwner, Status: domain.MembershipStatusApproved, RequestedAt: webTestNow,
	}); err != nil {
		t.Fatal(err)
	}

	get := func(u domain.User) *httptest.ResponseRecorder {
		return do(r, http.MethodGet, "/", nil, withCookie(loginAs(t, app, u)))
	}

	rec := get(member)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != htmlContentType {
		t.Fatalf("got %d %q, want 200 html", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	wantAll(t, "member's home", body, "<title>Calendário · AGENDA CLUBBER</title>", "Maria Membra", `action="/sair"`, `aria-disabled="true"`)
	if strings.Contains(body, `href="/admin"`) || strings.Contains(body, `href="/eventos/novo"`) {
		t.Errorf("a plain member sees admin or create-event links: %s", body)
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}

	if body := get(admin).Body.String(); !strings.Contains(body, `href="/admin"`) || !strings.Contains(body, "Ada Admin") {
		t.Errorf("the admin does not see the admin link: %s", body)
	}
	if body := get(owner).Body.String(); !strings.Contains(body, `href="/eventos/novo"`) || strings.Contains(body, `aria-disabled="true"`) {
		t.Errorf("the owner cannot reach Criar Evento: %s", body)
	}

	rec = get(expired)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/senha/trocar" {
		t.Errorf("expired password: %d Location %q, want 303 /senha/trocar", rec.Code, rec.Header().Get("Location"))
	}
}

func TestNewAppLimits(t *testing.T) {
	app := newTestApp(t)
	tests := []struct {
		name   string
		l      *auth.Limiter
		limit  int
		window time.Duration
	}{
		{"login", app.Limits.Login, 10, time.Minute},
		{"signup", app.Limits.Signup, 5, time.Hour},
		{"email action", app.Limits.EmailAction, 5, time.Hour},
		{"suggestions", app.Limits.Suggestions, 60, time.Minute},
		{"password check", app.Limits.PasswordCheck, 30, time.Minute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.l == nil {
				t.Fatal("limiter is nil")
			}
			now := webTestNow
			for i := 1; i <= tt.limit; i++ {
				if !tt.l.Allow("k", now) {
					t.Fatalf("hit %d of %d was refused", i, tt.limit)
				}
			}
			if tt.l.Allow("k", now.Add(tt.window-time.Second)) {
				t.Errorf("hit %d inside the window was allowed", tt.limit+1)
			}
			if !tt.l.Allow("k", now.Add(tt.window)) {
				t.Error("the limit did not reset after the window")
			}
			if !tt.l.Allow("other", now) {
				t.Error("one key's hits count against another")
			}
		})
	}
	if app.Limits.Login == app.Limits.Signup || app.Limits.Signup == app.Limits.EmailAction {
		t.Error("two flows share one limiter")
	}
	if msgTooMany != "muitas tentativas. espere alguns minutos e tente de novo." {
		t.Errorf("msgTooMany = %q", msgTooMany)
	}
}
