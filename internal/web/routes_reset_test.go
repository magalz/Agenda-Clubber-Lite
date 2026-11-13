package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/accounts"
	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const (
	// resetEmail has a local part ("ana.souza1") that a password can match.
	resetEmail = "ana.souza1@example.com"
	// resetNewPassword passes the policy and is in nobody's history.
	resetNewPassword = "Nova-Senha-Forte-7"

	resetForgotText  = "se houver uma conta ativa com este e-mail, enviamos um link para criar uma nova senha. ele vale por 1 hora."
	resetInvalidText = "este link expirou ou já foi usado."
	resetDoneText    = "senha alterada. entre com a nova senha."
	resetMsgHistory  = "use uma senha diferente das 2 últimas"
	resetMsgMismatch = "as senhas não conferem"

	resetLinkPrefix = testOrigin + "/senha/redefinir?token="
)

// resetForgotForm is what the forgot page offers: the form that asks for a link.
var resetForgotForm = []string{`<form method="post" action="/senha/esqueci"`, `name="email"`, `type="email"`, `autocomplete="email"`, "[→] ENVIAR LINK"}

// resetUser stores an active account (resetEmail) whose password is
// loginTestPassword. mutate adjusts the fixture before it is stored.
func resetUser(t *testing.T, store domain.Store, mutate ...func(*domain.User)) domain.User {
	t.Helper()
	withHash := func(u *domain.User) {
		u.Name = "Ana Souza"
		u.PasswordHash = loginWebHash()
	}
	return insertWebUser(t, store, resetEmail, append([]func(*domain.User){withHash}, mutate...)...)
}

// resetToken stores a token of the purpose for the user, issued at created and
// valid for the reset lifetime, and returns its raw form.
func resetToken(t *testing.T, store domain.Store, userID string, purpose domain.TokenPurpose, created time.Time) string {
	t.Helper()
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	tok := domain.Token{UserID: userID, Purpose: purpose, Hash: hash, ExpiresAt: created.Add(accounts.ResetTokenTTL), CreatedAt: created}
	if err := store.Repos().Tokens.Create(context.Background(), &tok); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return raw
}

// resetValidToken stores a reset token the person can use now.
func resetValidToken(t *testing.T, store domain.Store, userID string) string {
	t.Helper()
	return resetToken(t, store, userID, domain.TokenPurposeResetPassword, webTestNow)
}

// resetForgot asks for a link for the address from a client address.
func resetForgot(r http.Handler, email, ip string) *httptest.ResponseRecorder {
	return post(r, "/senha/esqueci", url.Values{"email": {email}}.Encode(), header("CF-Connecting-IP", ip))
}

// resetSubmit posts the new password the link's page asks for.
func resetSubmit(r http.Handler, token, password, confirm string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	form := url.Values{"token": {token}, "password": {password}, "password_confirm": {confirm}}
	return post(r, "/senha/redefinir", form.Encode(), mod...)
}

// resetMailedToken is the token in the link of a password reset email.
func resetMailedToken(t *testing.T, msg domain.OutboxMessage) string {
	t.Helper()
	i := strings.Index(msg.Text, resetLinkPrefix)
	if i < 0 {
		t.Fatalf("no reset link in %q", msg.Text)
	}
	return strings.Fields(msg.Text[i+len(resetLinkPrefix):])[0]
}

// resetPasswordHash is the password hash the store holds for the user.
func resetPasswordHash(t *testing.T, store domain.Store, id string) string {
	t.Helper()
	u, err := store.Repos().Users.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	return u.PasswordHash
}

// resetSignIn posts the login form, to prove which password the account has.
func resetSignIn(r http.Handler, password string) *httptest.ResponseRecorder {
	form := url.Values{"email": {resetEmail}, "password": {password}}
	return post(r, "/entrar", form.Encode())
}

// resetInvalidPage is what the reset page shows for a link that does not work.
func resetInvalidPage(t *testing.T, rec *httptest.ResponseRecorder, status int) string {
	t.Helper()
	body := confirmAssertPage(t, rec, status, "Nova senha", resetInvalidText, `role="alert"`, `href="/senha/esqueci"`)
	if strings.Contains(body, "[→] SALVAR NOVA SENHA") || strings.Contains(body, `name="password"`) {
		t.Errorf("the invalid-link page offers the password form:\n%s", body)
	}
	return body
}

// --- GET /senha/esqueci ---

func TestResetForgotShowsTheForm(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	rec := serve(r, http.MethodGet, "/senha/esqueci")
	body := confirmAssertPage(t, rec, http.StatusOK, "Esqueci a senha", resetForgotForm...)
	if strings.Contains(body, resetForgotText) || strings.Contains(body, `role="status"`) || strings.Contains(body, `role="alert"`) {
		t.Errorf("the empty form shows a message:\n%s", body)
	}
}

// --- POST /senha/esqueci ---

func TestResetForgotAnswersTheSameForEveryAddress(t *testing.T) {
	app, store := signupStoreApp(t)
	r := app.Router()
	resetUser(t, store)
	insertWebUser(t, store, "pendente@example.com", func(u *domain.User) {
		u.Status = domain.UserStatusPendingEmail
		u.EmailVerifiedAt = nil
	})

	var bodies []string
	for i, email := range []string{resetEmail, "pendente@example.com", "ninguem@example.com", "not an address", ""} {
		rec := resetForgot(r, email, fmt.Sprintf("198.51.100.%d", 10+i))
		body := confirmAssertPage(t, rec, http.StatusOK, "Esqueci a senha", resetForgotText, `role="status"`)
		if email != "" && strings.Contains(body, email) {
			t.Errorf("the page for %q echoes the address", email)
		}
		bodies = append(bodies, body)
	}
	for i, body := range bodies[1:] {
		if body != bodies[0] {
			t.Errorf("the page for address %d differs from the page for the registered one:\n%s\nversus\n%s", i+1, body, bodies[0])
		}
	}

	// Only the active account got a message, and it carries a link to the reset page.
	msgs := confirmOutbox(t, store)
	if len(msgs) != 1 || msgs[0].To != resetEmail || !strings.Contains(msgs[0].Text, resetLinkPrefix) {
		t.Fatalf("outbox = %+v, want one password reset email to %s", msgs, resetEmail)
	}
}

func TestResetForgotRateLimitPerIP(t *testing.T) {
	app, _ := signupStoreApp(t)
	r := app.Router()
	// Each request names another address, so only the IP key can trip.
	for i := 1; i <= 5; i++ {
		rec := resetForgot(r, fmt.Sprintf("u%d@example.com", i), "198.51.100.30")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
	rec := resetForgot(r, "u6@example.com", "198.51.100.30")
	body := confirmAssertPage(t, rec, http.StatusTooManyRequests, "Esqueci a senha", append([]string{msgTooMany, `role="alert"`}, resetForgotForm...)...)
	if strings.Contains(body, resetForgotText) {
		t.Error("the 429 page says a link was sent")
	}

	// Another client address is not affected.
	if rec := resetForgot(r, "u7@example.com", "198.51.100.31"); rec.Code != http.StatusOK {
		t.Errorf("other IP: status = %d, want 200", rec.Code)
	}
}

func TestResetForgotRateLimitPerEmail(t *testing.T) {
	app, _ := signupStoreApp(t)
	r := app.Router()
	// One address, spelled in different ways, from a new client address each
	// time: only the email key can trip.
	for i, email := range []string{"ana@example.com", "ANA@example.com", " ana@example.com ", "Ana@Example.com", "ana@EXAMPLE.COM"} {
		rec := resetForgot(r, email, fmt.Sprintf("198.51.100.%d", 40+i))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	rec := resetForgot(r, "ana@example.com", "198.51.100.50")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), msgTooMany) {
		t.Fatalf("6th request for one address: status = %d, body %q, want 429 with msgTooMany", rec.Code, rec.Body)
	}
	// Another address from a new client address is fine.
	if rec := resetForgot(r, "bia@example.com", "198.51.100.51"); rec.Code != http.StatusOK {
		t.Errorf("another address: status = %d, want 200", rec.Code)
	}
}

func TestResetForgotRefusedIPDoesNotSpendTheEmailBudget(t *testing.T) {
	app, _ := signupStoreApp(t)
	r := app.Router()
	for i := 1; i <= 5; i++ {
		resetForgot(r, fmt.Sprintf("u%d@example.com", i), "198.51.100.60")
	}
	// The address is named only while its client is refused ...
	for i := 0; i < 3; i++ {
		if rec := resetForgot(r, "ana@example.com", "198.51.100.60"); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("refused request %d: status = %d, want 429", i+1, rec.Code)
		}
	}
	// ... so its whole budget is still there for other clients.
	for i := 0; i < 5; i++ {
		if rec := resetForgot(r, "ana@example.com", fmt.Sprintf("198.51.100.%d", 61+i)); rec.Code != http.StatusOK {
			t.Fatalf("request %d from a new client: status = %d, want 200", i+1, rec.Code)
		}
	}
}

func TestResetForgotWithoutAnAddressSpendsNoEmailBudget(t *testing.T) {
	app, _ := signupStoreApp(t)
	r := app.Router()
	// Six requests with no address, from six clients: if the empty address kept
	// a budget of its own, the sixth would be refused.
	for i := 0; i < 6; i++ {
		if rec := resetForgot(r, "", fmt.Sprintf("198.51.100.%d", 70+i)); rec.Code != http.StatusOK {
			t.Fatalf("request %d without an address: status = %d, want 200", i+1, rec.Code)
		}
	}
}

func TestResetForgotBadBodyIs400(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	rec := post(r, "/senha/esqueci", "email=%ff", header("CF-Connecting-IP", "198.51.100.80"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestResetForgotServerErrorIsLoggedWithoutTheAddress(t *testing.T) {
	app := signupApp(t, signupFailingStore{})
	logs := captureLogs(app)

	rec := resetForgot(app.Router(), resetEmail, "198.51.100.90")
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
		t.Fatalf("status = %d, body %q, want 500 erro interno", rec.Code, rec.Body)
	}
	log := logs.String()
	if !strings.Contains(log, "the database is down") {
		t.Errorf("the error is not logged: %s", log)
	}
	if strings.Contains(log, resetEmail) || strings.Contains(rec.Body.String(), resetEmail) {
		t.Errorf("the address reached the log or the page: %s", log)
	}
}

// --- GET /senha/redefinir ---

func TestResetGetShowsThePasswordFormWithoutTouchingTheStore(t *testing.T) {
	// stubStore panics on any call, which the recovery turns into a 500: the
	// page answering 200 shows the request never reached the store.
	r := signupApp(t, stubStore{}).Router()
	rec := serve(r, http.MethodGet, "/senha/redefinir?token=x")
	body := confirmAssertPage(t, rec, http.StatusOK, "Nova senha",
		`<form method="post" action="/senha/redefinir"`, `type="hidden" name="token" value="x"`,
		`name="password"`, `name="password_confirm"`, `autocomplete="new-password"`, `id="password-checklist"`,
		`data-rule="history"`, "[→] SALVAR NOVA SENHA")
	if got, want := strings.Count(body, `class="checklist__item"`), len(passwordChecklist(true)); got != want {
		t.Errorf("%d checklist items, want %d", got, want)
	}
	if strings.Contains(body, resetInvalidText) {
		t.Error("the form page shows the invalid-link text")
	}
	for _, name := range []string{"password", "password_confirm"} {
		if tag := signupInput(t, body, name, ""); strings.Contains(tag, "value=") {
			t.Errorf("%s input has a value: %s", name, tag)
		}
	}
}

func TestResetGetEscapesTheToken(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	rec := serve(r, http.MethodGet, "/senha/redefinir?"+url.Values{"token": {`a"><b>x`}}.Encode())
	body := confirmAssertPage(t, rec, http.StatusOK, "Nova senha")
	if strings.Contains(body, `a"><b>x`) || !strings.Contains(body, `name="token" value="a&#34;&gt;&lt;b&gt;x"`) {
		t.Errorf("the token is not escaped in:\n%s", body)
	}
}

func TestResetGetWithoutATokenShowsTheInvalidLink(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	for _, target := range []string{"/senha/redefinir", "/senha/redefinir?token="} {
		resetInvalidPage(t, serve(r, http.MethodGet, target), http.StatusBadRequest)
	}
}

func TestResetGetDoesNotConsumeTheToken(t *testing.T) {
	app, store := signupStoreApp(t)
	r := app.Router()
	u := resetUser(t, store)
	raw := resetValidToken(t, store, u.ID)

	// A mail scanner opens the link, more than once.
	for i := 0; i < 3; i++ {
		if rec := serve(r, http.MethodGet, "/senha/redefinir?token="+raw); rec.Code != http.StatusOK {
			t.Fatalf("GET %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	if got := resetPasswordHash(t, store, u.ID); got != loginWebHash() {
		t.Fatal("the GETs changed the password")
	}
	rec := resetSubmit(r, raw, resetNewPassword, resetNewPassword)
	confirmAssertPage(t, rec, http.StatusOK, "Nova senha", resetDoneText)
}

// --- POST /senha/redefinir ---

func TestResetPostChangesThePasswordAndEndsEverySession(t *testing.T) {
	app, store := signupStoreApp(t)
	r := app.Router()
	u := resetUser(t, store)
	other := insertWebUser(t, store, "outra@example.com")
	raw := resetValidToken(t, store, u.ID)
	mine, elsewhere := loginAs(t, app, u), loginAs(t, app, u)
	bystander := loginAs(t, app, other)

	rec := resetSubmit(r, raw, resetNewPassword, resetNewPassword, withCookie(mine))
	body := confirmAssertPage(t, rec, http.StatusOK, "Nova senha", resetDoneText, `role="status"`, `href="/entrar"`)
	if strings.Contains(body, resetNewPassword) || strings.Contains(body, raw) {
		t.Errorf("the page echoes the password or the token:\n%s", body)
	}
	wantCleared(t, rec)

	if !sessionGone(t, app, mine.Value) || !sessionGone(t, app, elsewhere.Value) {
		t.Error("a session of the account survived the reset")
	}
	if sessionGone(t, app, bystander.Value) {
		t.Error("the reset ended the session of another account")
	}

	// The new password works, the old one does not.
	if rec := resetSignIn(r, resetNewPassword); rec.Code != http.StatusSeeOther {
		t.Errorf("sign in with the new password: status = %d, want 303", rec.Code)
	}
	if rec := resetSignIn(r, loginTestPassword); rec.Code == http.StatusSeeOther {
		t.Error("the old password still signs in")
	}

	// The link is good once.
	resetInvalidPage(t, resetSubmit(r, raw, resetNewPassword, resetNewPassword), http.StatusBadRequest)
}

func TestResetPostWithAnInvalidLinkShowsTheInvalidState(t *testing.T) {
	app, store := signupStoreApp(t)
	r := app.Router()
	u := resetUser(t, store)
	expired := resetToken(t, store, u.ID, domain.TokenPurposeResetPassword, webTestNow.Add(-2*accounts.ResetTokenTTL))
	wrongPurpose := resetToken(t, store, u.ID, domain.TokenPurposeVerifyEmail, webTestNow)
	pending := insertWebUser(t, store, "pendente@example.com", func(p *domain.User) {
		p.PasswordHash = loginWebHash()
		p.Status = domain.UserStatusPendingEmail
		p.EmailVerifiedAt = nil
	})
	pendingToken := resetValidToken(t, store, pending.ID)
	unknown, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	// A malformed token is refused before the store is asked, the others by the
	// store or the account's state; the person sees the same page.
	for name, token := range map[string]string{
		"empty": "", "malformed": "garbage", "unknown": unknown, "expired": expired,
		"another purpose": wrongPurpose, "an account that is not active": pendingToken,
	} {
		rec := resetSubmit(r, token, resetNewPassword, resetNewPassword)
		body := resetInvalidPage(t, rec, http.StatusBadRequest)
		if token != "" && strings.Contains(body, token) {
			t.Errorf("%s: the page echoes the token", name)
		}
		wantNoCookie(t, rec)
	}
	if got := resetPasswordHash(t, store, u.ID); got != loginWebHash() {
		t.Error("an invalid link changed the password")
	}
	if got := resetPasswordHash(t, store, pending.ID); got != loginWebHash() {
		t.Error("the link of an account that is not active changed its password")
	}
}

func TestResetPostFieldErrorsKeepTheLinkUsable(t *testing.T) {
	app, store := signupStoreApp(t)
	r := app.Router()
	u := resetUser(t, store)
	raw := resetValidToken(t, store, u.ID)

	for _, c := range []struct {
		name, password, confirm string
		errField, errText       string
	}{
		{"too weak", "Weak-Sentinel-pw", "Weak-Sentinel-pw", "password", "requisitos da senha: " + auth.RuleLabels[auth.RuleDigit]},
		{"no match", resetNewPassword, resetNewPassword + "x", "password_confirm", resetMsgMismatch},
		{"empty", "", "", "password", domain.MsgRequired},
		{"the local part of the e-mail", "Ana.Souza1", "Ana.Souza1", "password", "requisitos da senha: " + auth.RuleLabels[auth.RuleNotEmail]},
		{"the current password", loginTestPassword, loginTestPassword, "password", resetMsgHistory},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := resetSubmit(r, raw, c.password, c.confirm)
			body := confirmAssertPage(t, rec, http.StatusUnprocessableEntity, "Nova senha",
				`<form method="post" action="/senha/redefinir"`, `type="hidden" name="token" value="`+raw+`"`,
				`id="erro-`+c.errField+`"`, c.errText, "[→] SALVAR NOVA SENHA")
			if tag := signupInput(t, body, c.errField, ""); !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, "erro-"+c.errField) {
				t.Errorf("%s input = %s, want invalid and described by its error", c.errField, tag)
			}
			// No password comes back, in a value or anywhere else.
			for _, name := range []string{"password", "password_confirm"} {
				if tag := signupInput(t, body, name, ""); strings.Contains(tag, "value=") {
					t.Errorf("%s input has a value: %s", name, tag)
				}
			}
			if c.password != "" && strings.Contains(body, c.password) {
				t.Errorf("the page echoes the password:\n%s", body)
			}
			if got := signupInputs(body, "token", raw); len(got) != 1 {
				t.Errorf("%d token inputs, want 1", len(got))
			}
			wantNoCookie(t, rec)
		})
	}
	if got := resetPasswordHash(t, store, u.ID); got != loginWebHash() {
		t.Fatal("a refused password changed the account's password")
	}

	// Every refusal left the link unused.
	rec := resetSubmit(r, raw, resetNewPassword, resetNewPassword)
	confirmAssertPage(t, rec, http.StatusOK, "Nova senha", resetDoneText)
}

func TestResetPostBadBodyIs400(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	rec := post(r, "/senha/redefinir", "token=%ff")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestResetPostServerErrorIsLoggedWithoutTheTokenOrThePassword(t *testing.T) {
	app := signupApp(t, signupFailingStore{})
	logs := captureLogs(app)
	raw, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	rec := resetSubmit(app.Router(), raw, resetNewPassword, resetNewPassword)
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
		t.Fatalf("status = %d, body %q, want 500 erro interno", rec.Code, rec.Body)
	}
	log := logs.String()
	if !strings.Contains(log, "the database is down") {
		t.Errorf("the error is not logged: %s", log)
	}
	for _, secret := range []string{raw, resetNewPassword} {
		if strings.Contains(log, secret) || strings.Contains(rec.Body.String(), secret) {
			t.Errorf("a secret reached the log or the page: %s", log)
		}
	}
}

func TestResetPostWithUnreadableHistoryIsAServerError(t *testing.T) {
	app, store := signupStoreApp(t)
	logs := captureLogs(app)
	r := app.Router()
	// The default fixture hash is not an argon2 hash, so the history cannot be
	// compared: that is a failure of the server, and no password gets through.
	u := insertWebUser(t, store, resetEmail)
	raw := resetValidToken(t, store, u.ID)

	rec := resetSubmit(r, raw, resetNewPassword, resetNewPassword)
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
		t.Fatalf("status = %d, body %q, want 500 erro interno", rec.Code, rec.Body)
	}
	if got := resetPasswordHash(t, store, u.ID); got != "placeholder-hash" {
		t.Errorf("password hash = %q, want it untouched", got)
	}
	log := logs.String()
	for _, secret := range []string{raw, resetNewPassword, resetEmail} {
		if strings.Contains(log, secret) {
			t.Errorf("a secret reached the log: %s", log)
		}
	}
}

// --- the whole flow ---

func TestResetFlowKeepsTheTokenTheAddressAndThePasswordOutOfTheLogs(t *testing.T) {
	app, store := signupStoreApp(t)
	logs := captureLogs(app)
	r := app.Router()
	u := resetUser(t, store)
	session := loginAs(t, app, u)

	resetForgot(r, resetEmail, "198.51.100.100")
	msgs := confirmOutbox(t, store)
	if len(msgs) != 1 {
		t.Fatalf("outbox = %+v, want one password reset email", msgs)
	}
	token := resetMailedToken(t, msgs[0])

	if rec := serve(r, http.MethodGet, "/senha/redefinir?token="+token); rec.Code != http.StatusOK {
		t.Fatalf("GET the link: status = %d, want 200", rec.Code)
	}
	// A refused password first, then the good one.
	resetSubmit(r, token, "Weak-Sentinel-pw", "Weak-Sentinel-pw")
	rec := resetSubmit(r, token, resetNewPassword, resetNewPassword, withCookie(session))
	confirmAssertPage(t, rec, http.StatusOK, "Nova senha", resetDoneText)
	wantCleared(t, rec)
	if !sessionGone(t, app, session.Value) {
		t.Error("the session survived the reset")
	}
	if rec := resetSignIn(r, resetNewPassword); rec.Code != http.StatusSeeOther {
		t.Errorf("sign in with the new password: status = %d, want 303", rec.Code)
	}

	log := logs.String()
	for _, secret := range []string{token, resetEmail, resetNewPassword, "Weak-Sentinel-pw", loginTestPassword} {
		if strings.Contains(log, secret) {
			t.Errorf("the log holds %q: %s", secret, log)
		}
	}
}
