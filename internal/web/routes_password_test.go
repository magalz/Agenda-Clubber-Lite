package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const (
	passwordPathChange = "/senha/trocar"
	passwordPathCheck  = "/senha/verificar"

	// passwordOld is the password the account had before loginTestPassword;
	// passwordWrong is none of the account's passwords. No page text holds either.
	passwordOld   = "Senha-Antiga-2"
	passwordWrong = "Atual-Errada-3"

	passwordMsgDone         = "senha alterada. as outras sessões foram encerradas."
	passwordMsgExpired      = "sua senha expirou. escolha uma nova para continuar."
	passwordMsgWrongCurrent = "senha atual incorreta"
	passwordMsgRequired     = "campo obrigatório"
	passwordMsgPolicy       = "requisitos da senha:"
)

// The two clocks of an account: a password set yesterday and one past its 90 days.
var (
	passwordFresh   = webTestNow.Add(-24 * time.Hour)
	passwordExpired = webTestNow.Add(-auth.MaxAge - time.Hour)
)

// passwordSecrets are the passwords the tests use: none may reach a page or a log.
var passwordSecrets = []string{loginTestPassword, passwordOld, passwordWrong, signupPassword}

// passwordInputs matches the password inputs of a page, to prove none carries a value.
var passwordInputs = regexp.MustCompile(`<input[^>]*type="password"[^>]*>`)

// passwordOldHash is the argon2id hash of passwordOld, built once: hashing is slow on purpose.
var passwordOldHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword(passwordOld)
	if err != nil {
		panic(err)
	}
	return h
})

// passwordEnv is an App on a real database with one signed-in account (resetEmail,
// password loginTestPassword) and its log captured.
type passwordEnv struct {
	app    *App
	store  domain.Store
	r      http.Handler
	logs   *bytes.Buffer
	user   domain.User
	cookie *http.Cookie
}

// newPasswordEnv signs in an account whose password was set yesterday and whose
// history holds passwordOld after loginTestPassword.
func newPasswordEnv(t *testing.T) passwordEnv {
	t.Helper()
	return newPasswordEnvWith(t, passwordFresh, passwordOldHash())
}

// newPasswordEnvWith signs in an account whose password was set at changedAt and
// whose previous hash is prevHash.
func newPasswordEnvWith(t *testing.T, changedAt time.Time, prevHash string) passwordEnv {
	t.Helper()
	app, store := signupStoreApp(t)
	logs := captureLogs(app)
	user := resetUser(t, store, func(u *domain.User) { u.PasswordHash = prevHash })
	if err := store.Repos().Users.ChangePassword(context.Background(), user.ID, loginWebHash(), changedAt); err != nil {
		t.Fatalf("change password: %v", err)
	}
	return passwordEnv{app: app, store: store, r: app.Router(), logs: logs, user: user, cookie: loginAs(t, app, user)}
}

// show is GET /senha/trocar as the signed-in person.
func (e passwordEnv) show() *httptest.ResponseRecorder {
	return serve(e.r, http.MethodGet, passwordPathChange, withCookie(e.cookie))
}

// change posts the change form as the signed-in person.
func (e passwordEnv) change(current, password, confirm string) *httptest.ResponseRecorder {
	form := url.Values{"current_password": {current}, "password": {password}, "password_confirm": {confirm}}
	return post(e.r, passwordPathChange, form.Encode(), withCookie(e.cookie))
}

// verify posts a password to the live history check, as the page's script does.
func (e passwordEnv) verify(password string) *httptest.ResponseRecorder {
	return e.verifyRaw(url.Values{"password": {password}}.Encode())
}

// verifyRaw is verify with the body as it goes on the wire.
func (e passwordEnv) verifyRaw(body string) *httptest.ResponseRecorder {
	return post(e.r, passwordPathCheck, body, withCookie(e.cookie), hx)
}

// hash is the password hash the store holds for the account.
func (e passwordEnv) hash(t *testing.T) string {
	t.Helper()
	return resetPasswordHash(t, e.store, e.user.ID)
}

// wantUnchanged fails unless the account still has loginTestPassword and the
// session of the cookie is alive.
func (e passwordEnv) wantUnchanged(t *testing.T, sessions ...*http.Cookie) {
	t.Helper()
	if e.hash(t) != loginWebHash() {
		t.Error("the password changed")
	}
	for _, c := range append(sessions, e.cookie) {
		if sessionGone(t, e.app, c.Value) {
			t.Error("a session ended")
		}
	}
}

// passwordStranger signs in another person, whose password is loginTestPassword too.
func passwordStranger(t *testing.T, e passwordEnv) *http.Cookie {
	t.Helper()
	u := insertWebUser(t, e.store, "outra@example.com", func(u *domain.User) { u.PasswordHash = loginWebHash() })
	return loginAs(t, e.app, u)
}

// passwordWantNoSecrets fails if what holds one of the test passwords.
func passwordWantNoSecrets(t *testing.T, what, got string) {
	t.Helper()
	for _, secret := range passwordSecrets {
		if strings.Contains(got, secret) {
			t.Errorf("%s holds the password %q:\n%s", what, secret, got)
		}
	}
}

// passwordWantServerError fails unless rec is the plain 500 and the log has an error line.
func passwordWantServerError(t *testing.T, rec *httptest.ResponseRecorder, logs *bytes.Buffer) {
	t.Helper()
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
		t.Errorf("%d %q, want 500 \"erro interno\"", rec.Code, rec.Body.String())
	}
	if !strings.Contains(logs.String(), `"level":"ERROR"`) {
		t.Errorf("no error was logged:\n%s", logs)
	}
	passwordWantNoSecrets(t, "the log", logs.String())
}

// passwordItem checks that rec is the history item and nothing else, and returns it.
func passwordItem(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body:\n%s", rec.Code, body)
	}
	if !strings.HasPrefix(body, "<li ") || !strings.HasSuffix(strings.TrimSpace(body), "</li>") || strings.Count(body, "<li") != 1 {
		t.Fatalf("the answer is not the one history item:\n%s", body)
	}
	wantAll(t, "history item", body, `class="checklist__item`, `id="password-history"`, `data-rule="history"`, "diferente das 2 últimas senhas")
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}
	passwordWantNoSecrets(t, "the history item", body)
	return body
}

// passwordItemState checks the item against one of its three states: "pending"
// (not checked), "ok" or "reused".
func passwordItemState(t *testing.T, rec *httptest.ResponseRecorder, state string) {
	t.Helper()
	body := passwordItem(t, rec)
	met, failed := strings.Contains(body, "is-ok"), strings.Contains(body, "field-error")
	switch state {
	case "pending":
		wantAll(t, "pending item", body, "(pendente)")
		if met || failed {
			t.Errorf("a pending item is marked:\n%s", body)
		}
	case "ok":
		wantAll(t, "met item", body, "is-ok", "(atendido)")
		if failed {
			t.Errorf("a met item shows an error:\n%s", body)
		}
	case "reused":
		wantAll(t, "failed item", body, "(não atendido)", `class="field-error"`, "você já usou esta senha")
		if met {
			t.Errorf("a failed item is marked met:\n%s", body)
		}
	default:
		t.Fatalf("unknown state %q", state)
	}
}

// passwordFormChecks checks the change form is on the page and carries no password.
func passwordFormChecks(t *testing.T, body string) {
	t.Helper()
	wantAll(t, "change form", body,
		`<form method="post" action="/senha/trocar"`,
		`for="current_password"`, `name="current_password"`, `autocomplete="current-password"`,
		`name="password"`, `name="password_confirm"`, `autocomplete="new-password"`,
		"[→] TROCAR SENHA")
	inputs := passwordInputs.FindAllString(body, -1)
	if len(inputs) != 3 {
		t.Errorf("%d password inputs, want 3", len(inputs))
	}
	for _, in := range inputs {
		if strings.Contains(in, "value=") {
			t.Errorf("a password input carries a value: %s", in)
		}
	}
	passwordWantNoSecrets(t, "the page", body)
}

// --- GET /senha/trocar ---

func TestPasswordChangeShowsTheForm(t *testing.T) {
	e := newPasswordEnv(t)

	body := confirmAssertPage(t, e.show(), http.StatusOK, "Trocar senha",
		`data-email="`+resetEmail+`"`,
		`<ul class="checklist" id="password-checklist" hx-post="/senha/verificar"`,
		`hx-trigger="input changed delay:400ms from:#password"`,
		`hx-target="#password-history"`, `hx-swap="outerHTML"`, `hx-params="password"`)
	passwordFormChecks(t, body)
	for _, absent := range []string{`role="alert"`, `role="status"`, "sua senha expirou", `class="field-error"`} {
		if strings.Contains(body, absent) {
			t.Errorf("the empty form shows %q", absent)
		}
	}

	// The seven policy rules and the history, which comes last.
	if n := strings.Count(body, "data-rule="); n != 8 {
		t.Errorf("%d checklist items, want 8", n)
	}
	inOrder(t, body, `data-rule="min_length"`, `data-rule="not_email"`, `data-rule="max_length"`, `id="password-history"`)

	// The page and the live check draw the item with the same markup.
	if pending := passwordItem(t, e.verify("")); !strings.Contains(body, pending) {
		t.Errorf("the page does not hold the pending item %q", pending)
	}
}

func TestPasswordChangeNamesAnExpiredPassword(t *testing.T) {
	e := newPasswordEnvWith(t, passwordExpired, passwordOldHash())

	// The premise: access rules send this person to the change page.
	home := serve(e.r, http.MethodGet, "/", withCookie(e.cookie))
	if home.Code != http.StatusSeeOther || home.Header().Get("Location") != passwordPathChange {
		t.Fatalf("GET / = %d to %q, want 303 to %s", home.Code, home.Header().Get("Location"), passwordPathChange)
	}

	body := confirmAssertPage(t, e.show(), http.StatusOK, "Trocar senha", passwordMsgExpired)
	passwordFormChecks(t, body)
}

func TestPasswordChangeNeedsASession(t *testing.T) {
	// stubStore panics on any call: no session cookie means no lookup.
	r := signupApp(t, stubStore{}).Router()

	rec := serve(r, http.MethodGet, passwordPathChange)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar?next=%2Fsenha%2Ftrocar" {
		t.Errorf("GET = %d to %q, want 303 to the login with ?next=", rec.Code, rec.Header().Get("Location"))
	}
	form := url.Values{"current_password": {loginTestPassword}, "password": {signupPassword}, "password_confirm": {signupPassword}}
	rec = post(r, passwordPathChange, form.Encode())
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar" {
		t.Errorf("POST = %d to %q, want 303 to /entrar", rec.Code, rec.Header().Get("Location"))
	}
	rec = post(r, passwordPathCheck, url.Values{"password": {signupPassword}}.Encode(), hx)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("HX-Redirect") != "/entrar" {
		t.Errorf("POST check = %d with HX-Redirect %q, want 401 with /entrar", rec.Code, rec.Header().Get("HX-Redirect"))
	}
}

func TestPasswordPostsNeedTheAppsOrigin(t *testing.T) {
	// The Origin check runs before any session is looked up, so the stub store
	// is never reached.
	r := signupApp(t, stubStore{}).Router()
	for _, path := range []string{passwordPathChange, passwordPathCheck} {
		for name, mod := range map[string]func(*http.Request){
			"no Origin":    func(*http.Request) {},
			"other Origin": header("Origin", "https://evil.test"),
		} {
			rec := do(r, http.MethodPost, path, strings.NewReader("password=x"), header("Content-Type", "application/x-www-form-urlencoded"), mod)
			if rec.Code != http.StatusForbidden {
				t.Errorf("POST %s with %s = %d, want 403", path, name, rec.Code)
			}
		}
	}
}

// --- POST /senha/trocar ---

func TestPasswordChangeSetsThePasswordAndEndsTheOtherSessions(t *testing.T) {
	e := newPasswordEnv(t)
	other := loginAs(t, e.app, e.user)
	stranger := passwordStranger(t, e)

	rec := e.change(loginTestPassword, signupPassword, signupPassword)
	body := confirmAssertPage(t, rec, http.StatusOK, "Trocar senha",
		passwordMsgDone, `class="status status--ok" role="status"`, `<a href="/">ir para o calendário</a>`)
	// The layout's own logout form is on the page; the change form is not.
	for _, absent := range []string{`action="/senha/trocar"`, `name="password"`, `name="current_password"`, `role="alert"`} {
		if strings.Contains(body, absent) {
			t.Errorf("the done page holds %q", absent)
		}
	}
	passwordWantNoSecrets(t, "the page", body)
	wantNoCookie(t, rec)

	// The session that changed it stays; the person's others end; nobody else is touched.
	if sessionGone(t, e.app, e.cookie.Value) {
		t.Error("the session that changed the password ended")
	}
	if !sessionGone(t, e.app, other.Value) {
		t.Error("another session of the person is still alive")
	}
	if sessionGone(t, e.app, stranger.Value) {
		t.Error("another person's session ended")
	}
	if got := serve(e.r, http.MethodGet, "/", withCookie(e.cookie)); got.Code != http.StatusOK {
		t.Errorf("GET / with the kept session = %d, want 200", got.Code)
	}

	// The account has the new password, since now, and signs in with it only.
	u, err := e.store.Repos().Users.ByID(context.Background(), e.user.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	if !u.PasswordChangedAt.Equal(webTestNow) {
		t.Errorf("PasswordChangedAt = %v, want %v", u.PasswordChangedAt, webTestNow)
	}
	if rec := resetSignIn(e.r, signupPassword); rec.Code != http.StatusSeeOther {
		t.Errorf("sign in with the new password = %d, want 303", rec.Code)
	}
	if rec := resetSignIn(e.r, loginTestPassword); rec.Code != http.StatusUnauthorized {
		t.Errorf("sign in with the old password = %d, want 401", rec.Code)
	}
	passwordWantNoSecrets(t, "the log", e.logs.String())
}

func TestPasswordChangeLetsAnExpiredPasswordThrough(t *testing.T) {
	e := newPasswordEnvWith(t, passwordExpired, passwordOldHash())

	if rec := e.verify(signupPassword); rec.Code != http.StatusOK {
		t.Errorf("the live check sent the person away: %d", rec.Code)
	}
	confirmAssertPage(t, e.change(loginTestPassword, signupPassword, signupPassword), http.StatusOK, "Trocar senha", passwordMsgDone)
	if rec := serve(e.r, http.MethodGet, "/", withCookie(e.cookie)); rec.Code != http.StatusOK {
		t.Errorf("GET / after the change = %d, want 200: the password is fresh", rec.Code)
	}
}

func TestPasswordChangeRefusals(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		current, password, confirm string
		want                       map[string]string // field -> message prefix
	}{
		{"nothing typed", "", "", "",
			map[string]string{"current_password": passwordMsgRequired, "password": passwordMsgRequired}},
		{"a password that breaks the policy", loginTestPassword, "abc", "abc",
			map[string]string{"password": passwordMsgPolicy}},
		{"a confirmation that differs", loginTestPassword, signupPassword, signupPassword + "x",
			map[string]string{"password_confirm": resetMsgMismatch}},
		{"a wrong current password", passwordWrong, signupPassword, signupPassword,
			map[string]string{"current_password": passwordMsgWrongCurrent}},
		{"the current password again", loginTestPassword, loginTestPassword, loginTestPassword,
			map[string]string{"password": resetMsgHistory}},
		{"the previous password", loginTestPassword, passwordOld, passwordOld,
			map[string]string{"password": resetMsgHistory}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newPasswordEnv(t)
			other := loginAs(t, e.app, e.user)

			rec := e.change(tc.current, tc.password, tc.confirm)
			body := confirmAssertPage(t, rec, http.StatusUnprocessableEntity, "Trocar senha")
			passwordFormChecks(t, body)
			if n := strings.Count(body, `class="field-error"`); n != len(tc.want) {
				t.Errorf("%d field errors, want %d:\n%s", n, len(tc.want), body)
			}
			for field, msg := range tc.want {
				wantAll(t, field, body, `id="erro-`+field+`">`+msg, `aria-invalid="true"`)
			}
			if strings.Contains(body, `role="alert"`) {
				t.Error("a field error is also shown as an alert")
			}
			e.wantUnchanged(t, other)
			passwordWantNoSecrets(t, "the log", e.logs.String())
		})
	}
}

func TestPasswordChangeUnreadableHistoryIsAServerError(t *testing.T) {
	// A hash that cannot be read must never read as "no match".
	e := newPasswordEnvWith(t, passwordFresh, "not-an-argon2-hash")
	other := loginAs(t, e.app, e.user)

	rec := e.change(loginTestPassword, signupPassword, signupPassword)
	passwordWantServerError(t, rec, e.logs)
	e.wantUnchanged(t, other)
}

func TestPasswordChangeIsLimitedPerAccount(t *testing.T) {
	e := newPasswordEnv(t)
	e.app.Limits.PasswordCheck = auth.NewLimiter(2, time.Minute)

	for i := 1; i <= 2; i++ {
		if rec := e.change(passwordWrong, signupPassword, signupPassword); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("post %d: status = %d, want 422", i, rec.Code)
		}
	}
	// The third post is right, and still refused: no work is done for it.
	rec := e.change(loginTestPassword, signupPassword, signupPassword)
	body := confirmAssertPage(t, rec, http.StatusTooManyRequests, "Trocar senha", msgTooMany, `role="alert"`)
	passwordFormChecks(t, body)
	e.wantUnchanged(t)

	// The live check has a budget of its own.
	passwordItemState(t, e.verify(""), "pending")

	// Another account is not affected.
	form := url.Values{"current_password": {loginTestPassword}, "password": {signupPassword}, "password_confirm": {signupPassword}}
	if rec := post(e.r, passwordPathChange, form.Encode(), withCookie(passwordStranger(t, e))); rec.Code != http.StatusOK {
		t.Errorf("another account: status = %d, want 200", rec.Code)
	}
}

// --- POST /senha/verificar ---

func TestPasswordVerifyAnswersOnlyTheHistoryItem(t *testing.T) {
	e := newPasswordEnv(t)
	for _, tc := range []struct{ name, password, state string }{
		{"nothing typed", "", "pending"},
		{"a new password", signupPassword, "ok"},
		{"a password that breaks the policy, which is another item's rule", "abc", "ok"},
		{"the current password", loginTestPassword, "reused"},
		{"the previous password", passwordOld, "reused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			passwordItemState(t, e.verify(tc.password), tc.state)
		})
	}
	passwordWantNoSecrets(t, "the log", e.logs.String())
}

func TestPasswordVerifyLooksNowhereForInputThatCannotBeChecked(t *testing.T) {
	// The history holds a hash nobody can read, so any lookup would fail: an
	// answer shows the history was never read.
	e := newPasswordEnvWith(t, passwordFresh, "not-an-argon2-hash")

	for name, body := range map[string]string{
		"no field":       "",
		"empty":          "password=",
		"over 128 runes": "password=" + strings.Repeat("a", auth.MaxLength+1),
	} {
		t.Run(name, func(t *testing.T) {
			passwordItemState(t, e.verifyRaw(body), "pending")
		})
	}
	// At the limit it is looked up.
	passwordWantServerError(t, e.verifyRaw("password="+url.QueryEscape(strings.Repeat("é", auth.MaxLength))), e.logs)

	// A body that is not UTF-8 is refused before anything else.
	if rec := e.verifyRaw("password=%ff"); rec.Code != http.StatusBadRequest {
		t.Errorf("invalid UTF-8: status = %d, want 400", rec.Code)
	}
}

func TestPasswordVerifyUnreadableHistoryIsAServerError(t *testing.T) {
	e := newPasswordEnvWith(t, passwordFresh, "not-an-argon2-hash")

	rec := e.verify(signupPassword)
	passwordWantServerError(t, rec, e.logs)
	if strings.Contains(rec.Body.String(), "<li") {
		t.Errorf("the answer holds an item:\n%s", rec.Body)
	}
}

func TestPasswordVerifyIsLimitedPerAccount(t *testing.T) {
	e := newPasswordEnv(t)
	e.app.Limits.PasswordCheck = auth.NewLimiter(2, time.Minute)

	for i := 1; i <= 2; i++ {
		passwordItemState(t, e.verify(signupPassword), "ok")
	}
	rec := e.verify(signupPassword)
	if rec.Code != http.StatusTooManyRequests || strings.Contains(rec.Body.String(), "<li") {
		t.Errorf("third check = %d %q, want 429 and no item (HTMX leaves the page as it is)", rec.Code, rec.Body)
	}

	// The change form has a budget of its own, and another account is not affected.
	if rec := e.change(loginTestPassword, signupPassword, signupPassword); rec.Code != http.StatusOK {
		t.Errorf("change after the checks ran out = %d, want 200", rec.Code)
	}
	rec = post(e.r, passwordPathCheck, url.Values{"password": {signupPassword}}.Encode(), withCookie(passwordStranger(t, e)), hx)
	passwordItemState(t, rec, "ok")
}

// The signup and reset pages call the same partial without HistoryCheck: they
// get no live check, and no history item of the partial.
func TestPasswordFieldsWithoutAHistoryCheckStayStatic(t *testing.T) {
	app, store := signupStoreApp(t)
	r := app.Router()
	token := resetValidToken(t, store, resetUser(t, store).ID)
	for _, path := range []string{"/cadastro", "/senha/redefinir?token=" + token} {
		rec := serve(r, http.MethodGet, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `<ul class="checklist" id="password-checklist">`) {
			t.Errorf("%s: the checklist is not the plain one", path)
		}
		for _, absent := range []string{"hx-params", "hx-sync", "password-history", passwordPathCheck} {
			if strings.Contains(body, absent) {
				t.Errorf("%s holds %q", path, absent)
			}
		}
	}
}
