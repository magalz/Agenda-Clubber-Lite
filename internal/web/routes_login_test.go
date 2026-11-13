package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/accounts"
	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const (
	loginTestEmail    = "ana@agenda.test"
	loginTestPassword = "Senha-Certa-1"
	loginTestWrong    = "Senha-Errada-2"
)

// The messages the login page shows; the first is the bare text, the others are
// checked as the brief words them.
const (
	loginMsgFailed          = "e-mail ou senha incorretos."
	loginMsgLocked          = "muitas tentativas erradas. por segurança, a conta fica bloqueada por 15 minutos."
	loginMsgPendingEmail    = "confirme seu e-mail antes de entrar."
	loginMsgPendingApproval = "seu cadastro aguarda a aprovação do admin. você recebe um e-mail quando for aprovado."
	loginMsgDisabled        = "esta conta está desativada. fale com "
)

// loginMessages lists every status message by a text that identifies it.
var loginMessages = []string{loginMsgFailed, loginMsgLocked, loginMsgPendingEmail, loginMsgPendingApproval, loginMsgDisabled, msgTooMany}

// loginWebHash is the argon2id hash of loginTestPassword, built once: hashing is
// slow on purpose.
var loginWebHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword(loginTestPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// loginEnv is an App on a real database whose login limit is out of the way
// (the 429 test uses its own App), with its log captured. Every test request
// comes from one IP on a fixed clock, so the default limit would stop an App
// after ten POSTs.
type loginEnv struct {
	app   *App
	store domain.Store
	r     http.Handler
	logs  *bytes.Buffer
}

func newLoginEnv(t *testing.T) loginEnv {
	t.Helper()
	app, store := newStoreApp(t)
	app.Limits.Login = auth.NewLimiter(1000, time.Minute)
	logs := captureLogs(app)
	return loginEnv{app: app, store: store, r: app.Router(), logs: logs}
}

// user stores a user whose password is loginTestPassword.
func (e loginEnv) user(t *testing.T, mutate ...func(*domain.User)) domain.User {
	t.Helper()
	withHash := func(u *domain.User) { u.PasswordHash = loginWebHash() }
	return insertWebUser(t, e.store, loginTestEmail, append([]func(*domain.User){withHash}, mutate...)...)
}

// signIn posts the login form.
func (e loginEnv) signIn(email, password, next string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	form := url.Values{"email": {email}, "password": {password}, "next": {next}}
	return post(e.r, "/entrar", form.Encode(), mod...)
}

// wantNoSecrets fails if the log holds the email or a password.
func (e loginEnv) wantNoSecrets(t *testing.T) {
	t.Helper()
	got := e.logs.String()
	for _, secret := range []string{loginTestEmail, loginTestPassword, loginTestWrong} {
		if strings.Contains(got, secret) {
			t.Errorf("the log holds %q:\n%s", secret, got)
		}
	}
}

// loginStatus returns the status message of the page: the text from its
// role="alert" element to the end of that element.
func loginStatus(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `role="alert"`)
	if i < 0 {
		t.Fatalf("the page has no role=alert message:\n%s", body)
	}
	rest := body[i:]
	end := strings.Index(rest, "</p>")
	if end < 0 {
		t.Fatalf("the status message is not closed by </p>:\n%s", rest)
	}
	return rest[:end]
}

var loginPasswordInput = regexp.MustCompile(`<input[^>]*name="password"[^>]*>`)

// loginFormChecks fails unless the body is the login form, whatever it says
// around it, and shows nothing the CSP would block.
func loginFormChecks(t *testing.T, body string) {
	t.Helper()
	wantAll(t, "login form", body,
		`<form method="post" action="/entrar"`,
		`name="email"`, `type="email"`, `autocomplete="email"`,
		`name="password"`, `type="password"`, `autocomplete="current-password"`,
		`[→] ENTRAR`,
		`href="/cadastro"`, `href="/senha/esqueci"`,
		"<title>Entrar · AGENDA CLUBBER</title>",
	)
	if v := cspViolations(body); len(v) != 0 {
		t.Errorf("CSP violations: %v", v)
	}
	input := loginPasswordInput.FindString(body)
	if input == "" {
		t.Fatalf("no password input in:\n%s", body)
	}
	if strings.Contains(input, "value=") {
		t.Errorf("the password input carries a value: %s", input)
	}
}

// --- GET /entrar ---

func TestLoginPageRenders(t *testing.T) {
	app := newTestApp(t)
	rec := serve(app.Router(), http.MethodGet, "/entrar")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	loginFormChecks(t, body)
	if strings.Contains(body, `role="alert"`) {
		t.Errorf("a fresh form shows a status message:\n%s", body)
	}
	wantAll(t, "next defaults to the calendar", body, `name="next" value="/"`)
	wantNoCookie(t, rec)
}

func TestLoginPageKeepsOnlyASafeNext(t *testing.T) {
	r := newTestApp(t).Router()
	for _, tc := range []struct{ next, want string }{
		{"/eventos/12", "/eventos/12"},
		{"/eventos?dia=2026-11-12", "/eventos?dia=2026-11-12"},
		{"//evil.test", "/"},
		{"https://evil.test/x", "/"},
		{`/\evil.test`, "/"},
		{"", "/"},
	} {
		rec := serve(r, http.MethodGet, "/entrar?next="+url.QueryEscape(tc.next))
		if rec.Code != http.StatusOK {
			t.Fatalf("next %q: status = %d", tc.next, rec.Code)
		}
		body := rec.Body.String()
		hidden := `name="next" value="` + tc.want + `"`
		if !strings.Contains(body, hidden) {
			t.Errorf("next %q: want %s in the form:\n%s", tc.next, hidden, body)
		}
		if tc.want == "/" && strings.Contains(body, "evil.test") {
			t.Errorf("next %q leaks into the page:\n%s", tc.next, body)
		}
	}
}

func TestLoginPageSendsASignedInUserOn(t *testing.T) {
	app, store := newStoreApp(t)
	cookie := loginAs(t, app, insertWebUser(t, store, loginTestEmail))
	r := app.Router()

	for _, tc := range []struct{ next, want string }{
		{"", "/"},
		{"/eventos/12", "/eventos/12"},
		{"//evil.test", "/"},
	} {
		rec := serve(r, http.MethodGet, "/entrar?next="+url.QueryEscape(tc.next), withCookie(cookie))
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != tc.want {
			t.Errorf("next %q: %d to %q, want 303 to %q", tc.next, rec.Code, rec.Header().Get("Location"), tc.want)
		}
	}
}

// --- POST /entrar, success ---

func TestLoginSuccess(t *testing.T) {
	e := newLoginEnv(t)
	u := e.user(t, func(u *domain.User) { u.FailedLogins = 2 })

	rec := e.signIn("  ANA@Agenda.Test ", loginTestPassword, "/eventos/12")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/eventos/12" {
		t.Fatalf("%d to %q, want 303 to /eventos/12", rec.Code, rec.Header().Get("Location"))
	}
	c := responseCookie(rec, auth.SessionCookieName)
	if c == nil || c.Value == "" {
		t.Fatalf("no session cookie; headers %v", rec.Header())
	}
	checkCookieFlags(t, c)
	if c.MaxAge <= 0 {
		t.Errorf("MaxAge = %d, want a positive lifetime", c.MaxAge)
	}
	if sessionGone(t, e.app, c.Value) {
		t.Error("the cookie names no stored session")
	}

	if got, err := e.store.Repos().Users.ByID(context.Background(), u.ID); err != nil || got.FailedLogins != 0 {
		t.Errorf("FailedLogins = %d (err %v), want 0 after a login", got.FailedLogins, err)
	}
	home := serve(e.r, http.MethodGet, "/", withCookie(c))
	if home.Code != http.StatusOK {
		t.Errorf("GET / with the new session = %d, want 200", home.Code)
	}
	e.wantNoSecrets(t)
}

func TestLoginSuccessGoesToASafeNextOnly(t *testing.T) {
	e := newLoginEnv(t)
	e.user(t)

	for _, tc := range []struct{ next, want string }{
		{"", "/"},
		{"//evil.test", "/"},
		{"https://evil.test/x", "/"},
		{`/\evil.test`, "/"},
		{"/eventos?dia=2026-11-12", "/eventos?dia=2026-11-12"},
	} {
		rec := e.signIn(loginTestEmail, loginTestPassword, tc.next)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != tc.want {
			t.Errorf("next %q: %d to %q, want 303 to %q", tc.next, rec.Code, rec.Header().Get("Location"), tc.want)
		}
	}
}

func TestLoginSuccessRotatesTheSession(t *testing.T) {
	e := newLoginEnv(t)
	u := e.user(t)
	stale := loginAs(t, e.app, u)

	rec := e.signIn(loginTestEmail, loginTestPassword, "", withCookie(stale))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	fresh := responseCookie(rec, auth.SessionCookieName)
	if fresh == nil || fresh.Value == "" || fresh.Value == stale.Value {
		t.Fatalf("the login reused the session token: %+v", fresh)
	}
	if !sessionGone(t, e.app, stale.Value) {
		t.Error("the session the request came with is still valid")
	}
}

func TestLoginHTMXSuccessRedirectsWithAHeader(t *testing.T) {
	e := newLoginEnv(t)
	e.user(t)

	rec := e.signIn(loginTestEmail, loginTestPassword, "/eventos/12", hx)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/eventos/12" {
		t.Errorf("%d with HX-Redirect %q, want 204 with /eventos/12", rec.Code, rec.Header().Get("HX-Redirect"))
	}
}

// An expired password does not stop the login; requireFreshPassword then keeps
// the person on the page that changes it.
func TestLoginWithAnExpiredPasswordStillSignsIn(t *testing.T) {
	e := newLoginEnv(t)
	e.user(t, func(u *domain.User) { u.PasswordChangedAt = webTestNow.Add(-auth.MaxAge - time.Hour) })

	rec := e.signIn(loginTestEmail, loginTestPassword, "")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/" {
		t.Fatalf("%d to %q, want 303 to /", rec.Code, rec.Header().Get("Location"))
	}
	c := responseCookie(rec, auth.SessionCookieName)
	if c == nil || c.Value == "" {
		t.Fatalf("no session cookie; headers %v", rec.Header())
	}
	home := serve(e.r, http.MethodGet, "/", withCookie(c))
	if home.Code != http.StatusSeeOther || home.Header().Get("Location") != "/senha/trocar" {
		t.Errorf("GET / = %d to %q, want 303 to /senha/trocar", home.Code, home.Header().Get("Location"))
	}
}

// --- POST /entrar, failures ---

func TestLoginFailures(t *testing.T) {
	soon := webTestNow.Add(10 * time.Minute)
	for _, tc := range []struct {
		name     string
		mutate   func(*domain.User) // nil: no user at all
		email    string
		password string
		want     string
	}{
		{"unknown email", nil, "ninguem@agenda.test", loginTestPassword, loginMsgFailed},
		{"wrong password", func(*domain.User) {}, loginTestEmail, loginTestWrong, loginMsgFailed},
		{"locked", func(u *domain.User) { u.LockedUntil = &soon }, loginTestEmail, loginTestPassword, loginMsgLocked},
		{"pending email", func(u *domain.User) { u.Status = domain.UserStatusPendingEmail }, loginTestEmail, loginTestPassword, loginMsgPendingEmail},
		{"pending approval", func(u *domain.User) { u.Status = domain.UserStatusPendingApproval }, loginTestEmail, loginTestPassword, loginMsgPendingApproval},
		{"disabled", func(u *domain.User) { u.Status = domain.UserStatusDisabled }, loginTestEmail, loginTestPassword, loginMsgDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newLoginEnv(t)
			if tc.mutate != nil {
				e.user(t, tc.mutate)
			}

			rec := e.signIn(tc.email, tc.password, "/eventos/12")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			wantNoCookie(t, rec)
			body := rec.Body.String()
			loginFormChecks(t, body)

			if n := strings.Count(body, `role="alert"`); n != 1 {
				t.Errorf("%d role=alert messages, want 1", n)
			}
			if n := strings.Count(body, "status--error"); n != 1 {
				t.Errorf("%d status--error elements, want 1", n)
			}
			status := loginStatus(t, body)
			for _, msg := range loginMessages {
				if has := strings.Contains(status, msg); has != (msg == tc.want) {
					t.Errorf("status shows %q = %v, want %v:\n%s", msg, has, msg == tc.want, status)
				}
			}
			wantAll(t, "kept input", body, `name="email"`, `value="`+tc.email+`"`, `name="next" value="/eventos/12"`)
			if strings.Contains(body, tc.password) {
				t.Errorf("the page repeats the password")
			}
			e.wantNoSecrets(t)
		})
	}
}

// Only the person who knows the password learns the state of the account.
func TestLoginDoesNotRevealTheStatusWithoutThePassword(t *testing.T) {
	for _, status := range []domain.UserStatus{domain.UserStatusPendingEmail, domain.UserStatusPendingApproval, domain.UserStatusDisabled} {
		t.Run(string(status), func(t *testing.T) {
			e := newLoginEnv(t)
			e.user(t, func(u *domain.User) { u.Status = status })

			rec := e.signIn(loginTestEmail, loginTestWrong, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			msg := loginStatus(t, rec.Body.String())
			if !strings.Contains(msg, loginMsgFailed) {
				t.Errorf("a wrong password shows %q, want %q", msg, loginMsgFailed)
			}
			for _, leak := range []string{loginMsgLocked, loginMsgPendingEmail, loginMsgPendingApproval, loginMsgDisabled} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Errorf("a wrong password reveals %q", leak)
				}
			}
			if strings.Contains(rec.Body.String(), "/confirmar/reenviar") {
				t.Error("a wrong password offers the resend form")
			}
		})
	}
}

func TestLoginPendingEmailOffersToResendTheConfirmation(t *testing.T) {
	e := newLoginEnv(t)
	e.user(t, func(u *domain.User) { u.Status = domain.UserStatusPendingEmail })

	rec := e.signIn(loginTestEmail, loginTestPassword, "")
	body := rec.Body.String()
	wantAll(t, "resend form", body,
		`<form method="post" action="/confirmar/reenviar"`,
		`<input type="hidden" name="email" value="`+loginTestEmail+`">`,
		`[→] REENVIAR CONFIRMAÇÃO`,
	)
	if n := strings.Count(body, "<form"); n != 2 {
		t.Errorf("%d forms, want the login form and the resend form", n)
	}

	// Any other failure shows the login form alone.
	other := e.signIn("ninguem@agenda.test", loginTestPassword, "")
	if strings.Contains(other.Body.String(), "/confirmar/reenviar") {
		t.Error("the resend form shows for an unknown email")
	}
}

func TestLoginDisabledPointsToTheContactAddress(t *testing.T) {
	e := newLoginEnv(t)
	e.user(t, func(u *domain.User) { u.Status = domain.UserStatusDisabled })

	rec := e.signIn(loginTestEmail, loginTestPassword, "")
	wantAll(t, "disabled message", loginStatus(t, rec.Body.String()),
		`<a href="mailto:ignisdev@magalz.space">ignisdev@magalz.space</a>`)
}

func TestLoginLocksTheAccountAfterFiveWrongPasswords(t *testing.T) {
	e := newLoginEnv(t)
	e.user(t)

	for i := 1; i < accounts.LoginMaxFailures; i++ {
		rec := e.signIn(loginTestEmail, loginTestWrong, "")
		if rec.Code != http.StatusUnauthorized || !strings.Contains(loginStatus(t, rec.Body.String()), loginMsgFailed) {
			t.Fatalf("wrong password %d: %d, want 401 with %q", i, rec.Code, loginMsgFailed)
		}
	}
	rec := e.signIn(loginTestEmail, loginTestWrong, "")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(loginStatus(t, rec.Body.String()), loginMsgLocked) {
		t.Fatalf("the fifth wrong password: %d, want 401 with %q", rec.Code, loginMsgLocked)
	}

	// The right password does not get through a lock.
	rec = e.signIn(loginTestEmail, loginTestPassword, "")
	if rec.Code != http.StatusUnauthorized || !strings.Contains(loginStatus(t, rec.Body.String()), loginMsgLocked) {
		t.Errorf("the right password while locked: %d, want 401 with %q", rec.Code, loginMsgLocked)
	}
	wantNoCookie(t, rec)
}

// --- POST /entrar, rate limit, bad input, server errors ---

// The limit is checked before the body is read, so ten requests with a body that
// is not a form fill it without ever reaching the store.
func TestLoginRateLimit(t *testing.T) {
	app := newTestApp(t) // stubStore: a request that reached the store would be a 500
	r := app.Router()

	for i := 1; i <= 10; i++ {
		if rec := post(r, "/entrar", "email=%ff"); rec.Code != http.StatusBadRequest {
			t.Fatalf("request %d: status = %d, want 400", i, rec.Code)
		}
	}
	rec := post(r, "/entrar", "email=%ff")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("request 11: status = %d, want 429", rec.Code)
	}
	wantAll(t, "429 page", rec.Body.String(), msgTooMany)
	wantNoCookie(t, rec)

	// The limit is per client IP.
	otherIP := func(req *http.Request) { req.RemoteAddr = "192.0.2.77:4321" }
	if rec := post(r, "/entrar", "email=%ff", otherIP); rec.Code != http.StatusBadRequest {
		t.Errorf("another IP: status = %d, want 400", rec.Code)
	}
}

func TestLoginRateLimitStopsTheRightPasswordToo(t *testing.T) {
	app, store := newStoreApp(t)
	app.Limits.Login = auth.NewLimiter(2, time.Minute)
	e := loginEnv{app: app, store: store, r: app.Router(), logs: captureLogs(app)}
	e.user(t)

	for i := 1; i <= 2; i++ {
		if rec := e.signIn(loginTestEmail, loginTestWrong, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("request %d: status = %d, want 401", i, rec.Code)
		}
	}
	rec := e.signIn(loginTestEmail, loginTestPassword, "")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	wantNoCookie(t, rec)
}

func TestLoginBadFormIsA400(t *testing.T) {
	r := newTestApp(t).Router()
	for _, body := range []string{"email=%ff", "email=%zz", "%ff=1"} {
		rec := post(r, "/entrar", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, rec.Code)
		}
		wantNoCookie(t, rec)
	}
}

type loginFailingUsers struct{ domain.Users }

func (loginFailingUsers) ByEmail(context.Context, string) (domain.User, error) {
	return domain.User{}, errors.New("users are down")
}

func TestLoginServerErrorsAre500AndLeakNothing(t *testing.T) {
	t.Run("store failure", func(t *testing.T) {
		e := newLoginEnv(t)
		e.app.Store = repoOverride{Store: e.store, mutate: func(r *domain.Repos) { r.Users = loginFailingUsers{r.Users} }}
		e.r = e.app.Router()

		rec := e.signIn(loginTestEmail, loginTestPassword, "")
		if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
			t.Errorf("%d %q, want 500 erro interno", rec.Code, rec.Body.String())
		}
		wantNoCookie(t, rec)
		if !strings.Contains(e.logs.String(), "users are down") {
			t.Errorf("the failure was not logged:\n%s", e.logs)
		}
		e.wantNoSecrets(t)
	})

	t.Run("malformed stored hash", func(t *testing.T) {
		e := newLoginEnv(t)
		u := insertWebUser(t, e.store, loginTestEmail) // "placeholder-hash" is not a PHC string

		rec := e.signIn(loginTestEmail, loginTestPassword, "")
		if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
			t.Errorf("%d %q, want 500 erro interno", rec.Code, rec.Body.String())
		}
		wantNoCookie(t, rec)
		if !strings.Contains(e.logs.String(), "invalid password hash") {
			t.Errorf("the failure was not logged:\n%s", e.logs)
		}
		e.wantNoSecrets(t)
		if got, err := e.store.Repos().Users.ByID(context.Background(), u.ID); err != nil || got.FailedLogins != 0 {
			t.Errorf("a broken hash counted %d failures (err %v), want 0", got.FailedLogins, err)
		}
	})
}

// --- POST /sair ---

func TestLogoutEndsTheSession(t *testing.T) {
	app, store := newStoreApp(t)
	cookie := loginAs(t, app, insertWebUser(t, store, loginTestEmail))
	r := app.Router()

	rec := post(r, "/sair", "", withCookie(cookie))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar" {
		t.Fatalf("%d to %q, want 303 to /entrar", rec.Code, rec.Header().Get("Location"))
	}
	wantCleared(t, rec)
	if !sessionGone(t, app, cookie.Value) {
		t.Error("the session is still stored")
	}
	if again := serve(r, http.MethodGet, "/", withCookie(cookie)); again.Code != http.StatusSeeOther {
		t.Errorf("GET / with the old cookie = %d, want 303 to the login", again.Code)
	}
}

func TestLogoutHTMXRedirectsWithAHeader(t *testing.T) {
	app, store := newStoreApp(t)
	cookie := loginAs(t, app, insertWebUser(t, store, loginTestEmail))

	rec := post(app.Router(), "/sair", "", withCookie(cookie), hx)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/entrar" {
		t.Errorf("%d with HX-Redirect %q, want 204 with /entrar", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	wantCleared(t, rec)
}

// A user whose password has expired can still sign out.
func TestLogoutWithAnExpiredPassword(t *testing.T) {
	app, store := newStoreApp(t)
	user := insertWebUser(t, store, loginTestEmail, func(u *domain.User) { u.PasswordChangedAt = webTestNow.Add(-auth.MaxAge - time.Hour) })
	cookie := loginAs(t, app, user)

	rec := post(app.Router(), "/sair", "", withCookie(cookie))
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar" {
		t.Fatalf("%d to %q, want 303 to /entrar", rec.Code, rec.Header().Get("Location"))
	}
	if !sessionGone(t, app, cookie.Value) {
		t.Error("the session is still stored")
	}
}

func TestLogoutNeedsASessionAnOriginAndAPost(t *testing.T) {
	app, store := newStoreApp(t)
	cookie := loginAs(t, app, insertWebUser(t, store, loginTestEmail))
	r := app.Router()

	if rec := post(r, "/sair", ""); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar" {
		t.Errorf("anonymous: %d to %q, want 303 to /entrar", rec.Code, rec.Header().Get("Location"))
	}
	noOrigin := do(r, http.MethodPost, "/sair", strings.NewReader(""), withCookie(cookie))
	if noOrigin.Code != http.StatusForbidden {
		t.Errorf("no Origin: status = %d, want 403", noOrigin.Code)
	}
	if sessionGone(t, app, cookie.Value) {
		t.Error("a post without the Origin header ended the session")
	}
	if rec := serve(r, http.MethodGet, "/sair", withCookie(cookie)); rec.Code != http.StatusNotFound {
		t.Errorf("GET /sair = %d, want 404 (mutations are POST only)", rec.Code)
	}
	if sessionGone(t, app, cookie.Value) {
		t.Error("a GET ended the session")
	}
}
