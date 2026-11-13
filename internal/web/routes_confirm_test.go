package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/accounts"
	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
)

// confirmCIDR covers 192.0.2.1, the remote address httptest gives every request:
// the App then trusts CF-Connecting-IP, so a test picks the client address.
const confirmCIDR = "192.0.2.0/24"

const (
	confirmInvalidText = "este link de confirmação expirou ou já foi usado."
	confirmSentText    = "se houver um cadastro aguardando confirmação para este e-mail, enviamos um novo link. ele vale por 24 horas."
	confirmWaitingText = "e-mail confirmado. seu cadastro agora aguarda a aprovação do admin. você recebe um e-mail quando ele for aprovado."
)

// confirmApp builds the production-mode App of these tests around the store.
func confirmApp(t *testing.T, store domain.Store) *App {
	t.Helper()
	app := NewApp(testConfig(t, confirmCIDR), slog.New(slog.NewJSONHandler(&strings.Builder{}, nil)), store)
	app.Now = func() time.Time { return webTestNow }
	return app
}

// confirmStoreApp is confirmApp on an empty MongoDB database of its own. It
// skips locally, and fails in CI, without MONGO_TEST_URI.
func confirmStoreApp(t *testing.T) (*App, domain.Store) {
	t.Helper()
	store := mongotest.New(t)
	return confirmApp(t, store), store
}

// confirmPendingUser stores an account that has not confirmed its email yet.
func confirmPendingUser(t *testing.T, store domain.Store, email string) domain.User {
	t.Helper()
	return insertWebUser(t, store, email, func(u *domain.User) {
		u.Name = "Ana Souza"
		u.Status = domain.UserStatusPendingEmail
		u.EmailVerifiedAt = nil
	})
}

// confirmToken stores a verification token for the user and returns its raw form.
func confirmToken(t *testing.T, store domain.Store, userID string) string {
	t.Helper()
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	tok := domain.Token{UserID: userID, Purpose: domain.TokenPurposeVerifyEmail, Hash: hash, ExpiresAt: webTestNow.Add(accounts.SignupTokenTTL), CreatedAt: webTestNow}
	if err := store.Repos().Tokens.Create(context.Background(), &tok); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return raw
}

// confirmPost posts the form from a client address.
func confirmPost(r http.Handler, target string, v url.Values, ip string) *httptest.ResponseRecorder {
	return post(r, target, v.Encode(), header("CF-Connecting-IP", ip))
}

// confirmResend asks for a new link for the address from a client address.
func confirmResend(r http.Handler, email, ip string) *httptest.ResponseRecorder {
	return confirmPost(r, "/confirmar/reenviar", url.Values{"email": {email}}, ip)
}

// confirmOutbox claims the messages waiting at the test clock. It leases them,
// so call it once per test.
func confirmOutbox(t *testing.T, store domain.Store) []domain.OutboxMessage {
	t.Helper()
	msgs, err := store.Repos().Outbox.Claim(context.Background(), webTestNow, time.Minute, 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return msgs
}

// confirmUserStatus is the status the store holds for the user.
func confirmUserStatus(t *testing.T, store domain.Store, id string) domain.UserStatus {
	t.Helper()
	u, err := store.Repos().Users.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	return u.Status
}

// confirmAssertPage checks the parts every page of the flow shares: the status,
// the title, no CSP violation, and the given texts.
func confirmAssertPage(t *testing.T, rec *httptest.ResponseRecorder, status int, title string, wants ...string) string {
	t.Helper()
	body := rec.Body.String()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body:\n%s", rec.Code, status, body)
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}
	wantAll(t, "page", body, append([]string{"<title>" + title + " · AGENDA CLUBBER</title>"}, wants...)...)
	return body
}

// confirmResendForm is what the invalid-link state offers: the form that asks
// for a new link.
var confirmResendForm = []string{`action="/confirmar/reenviar"`, `name="email"`, "[→] REENVIAR CONFIRMAÇÃO"}

// confirmFailingStore is a store whose transactions fail, so a request that
// passed validation ends in a server error.
type confirmFailingStore struct{ domain.Store }

func (confirmFailingStore) WithTx(context.Context, func(context.Context, domain.Repos) error) error {
	return errors.New("the database is down")
}

// --- GET /confirmar ---

func TestConfirmGetShowsTheButtonWithoutTouchingTheStore(t *testing.T) {
	// stubStore panics on any call, which the recovery turns into a 500: the
	// page answering 200 shows the request never reached the store.
	r := confirmApp(t, stubStore{}).Router()
	rec := serve(r, http.MethodGet, "/confirmar?token=x")
	body := confirmAssertPage(t, rec, http.StatusOK, "Confirmar e-mail",
		`<form method="post" action="/confirmar"`, `type="hidden" name="token" value="x"`, "[→] CONFIRMAR E-MAIL")
	if strings.Contains(body, confirmInvalidText) {
		t.Error("the confirm page shows the invalid-link text")
	}
	if strings.Contains(body, `name="email"`) {
		t.Error("the confirm page offers the resend form")
	}
}

func TestConfirmGetEscapesTheToken(t *testing.T) {
	r := confirmApp(t, stubStore{}).Router()
	rec := serve(r, http.MethodGet, "/confirmar?"+url.Values{"token": {`a"><b>x`}}.Encode())
	body := confirmAssertPage(t, rec, http.StatusOK, "Confirmar e-mail")
	if strings.Contains(body, `a"><b>x`) || !strings.Contains(body, `name="token" value="a&#34;&gt;&lt;b&gt;x"`) {
		t.Errorf("the token is not escaped in:\n%s", body)
	}
}

func TestConfirmGetWithoutATokenShowsTheInvalidLink(t *testing.T) {
	r := confirmApp(t, stubStore{}).Router()
	for _, target := range []string{"/confirmar", "/confirmar?token="} {
		rec := serve(r, http.MethodGet, target)
		body := confirmAssertPage(t, rec, http.StatusBadRequest, "Confirmar e-mail", append([]string{confirmInvalidText}, confirmResendForm...)...)
		if strings.Contains(body, "[→] CONFIRMAR E-MAIL") {
			t.Errorf("%s: the invalid-link page offers the confirm button", target)
		}
	}
}

func TestConfirmGetDoesNotConsumeTheToken(t *testing.T) {
	app, store := confirmStoreApp(t)
	r := app.Router()
	u := confirmPendingUser(t, store, "ana@example.com")
	raw := confirmToken(t, store, u.ID)

	// A mail scanner opens the link, more than once.
	for i := 0; i < 3; i++ {
		if rec := serve(r, http.MethodGet, "/confirmar?token="+raw); rec.Code != http.StatusOK {
			t.Fatalf("GET %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	if got := confirmUserStatus(t, store, u.ID); got != domain.UserStatusPendingEmail {
		t.Fatalf("status after the GETs = %q, want pending_email", got)
	}
	rec := confirmPost(r, "/confirmar", url.Values{"token": {raw}}, "198.51.100.1")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/aguardando" {
		t.Fatalf("POST after the GETs: status = %d, Location %q, want 303 /aguardando", rec.Code, rec.Header().Get("Location"))
	}
}

// --- POST /confirmar ---

func TestConfirmPostConfirmsTheAccountAndNotifiesTheAdmins(t *testing.T) {
	app, store := confirmStoreApp(t)
	r := app.Router()
	u := confirmPendingUser(t, store, "ana@example.com")
	insertWebUser(t, store, "admin@example.com", func(u *domain.User) { u.IsAdmin = true })
	raw := confirmToken(t, store, u.ID)

	rec := confirmPost(r, "/confirmar", url.Values{"token": {raw}}, "198.51.100.1")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/aguardando" {
		t.Fatalf("status = %d, Location %q, body %q, want 303 /aguardando", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	if got := confirmUserStatus(t, store, u.ID); got != domain.UserStatusPendingApproval {
		t.Errorf("status = %q, want pending_approval", got)
	}
	msgs := confirmOutbox(t, store)
	if len(msgs) != 1 || msgs[0].To != "admin@example.com" || !strings.Contains(msgs[0].Text, testOrigin+"/admin") {
		t.Errorf("outbox = %+v, want one notice to the admin with the admin link", msgs)
	}

	// The link is good once.
	rec = confirmPost(r, "/confirmar", url.Values{"token": {raw}}, "198.51.100.1")
	confirmAssertPage(t, rec, http.StatusBadRequest, "Confirmar e-mail", append([]string{confirmInvalidText}, confirmResendForm...)...)
}

func TestConfirmPostFromHTMXRedirectsWithAHeader(t *testing.T) {
	app, store := confirmStoreApp(t)
	u := confirmPendingUser(t, store, "ana@example.com")
	raw := confirmToken(t, store, u.ID)

	rec := post(app.Router(), "/confirmar", url.Values{"token": {raw}}.Encode(), header("CF-Connecting-IP", "198.51.100.1"), hx)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/aguardando" {
		t.Errorf("status = %d, HX-Redirect %q, want 204 /aguardando", rec.Code, rec.Header().Get("HX-Redirect"))
	}
}

func TestConfirmPostWithAnInvalidTokenShowsTheResendForm(t *testing.T) {
	app, store := confirmStoreApp(t)
	r := app.Router()
	u := confirmPendingUser(t, store, "ana@example.com")
	unknown, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	// A malformed token is refused before the store is asked, an unknown one
	// by the store; the person sees the same page.
	for _, token := range []string{"", "garbage", unknown} {
		rec := confirmPost(r, "/confirmar", url.Values{"token": {token}}, "198.51.100.1")
		body := confirmAssertPage(t, rec, http.StatusBadRequest, "Confirmar e-mail", append([]string{confirmInvalidText, `role="alert"`}, confirmResendForm...)...)
		if token != "" && strings.Contains(body, token) {
			t.Errorf("token %q: the page echoes the token", token)
		}
	}
	if got := confirmUserStatus(t, store, u.ID); got != domain.UserStatusPendingEmail {
		t.Errorf("status = %q, want pending_email", got)
	}
}

func TestConfirmPostBadBodyIs400(t *testing.T) {
	r := confirmApp(t, stubStore{}).Router()
	rec := post(r, "/confirmar", "token=%ff", header("CF-Connecting-IP", "198.51.100.1"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestConfirmPostServerErrorIsLoggedWithoutTheToken(t *testing.T) {
	_, store := confirmStoreApp(t)
	app := confirmApp(t, confirmFailingStore{store})
	logs := captureLogs(app)
	raw, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	rec := confirmPost(app.Router(), "/confirmar", url.Values{"token": {raw}}, "198.51.100.1")
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
		t.Fatalf("status = %d, body %q, want 500 erro interno", rec.Code, rec.Body)
	}
	log := logs.String()
	if !strings.Contains(log, "the database is down") {
		t.Errorf("the error is not logged: %s", log)
	}
	if strings.Contains(log, raw) || strings.Contains(rec.Body.String(), raw) {
		t.Errorf("the token reached the log or the page: %s", log)
	}
}

// --- POST /confirmar/reenviar ---

func TestConfirmResendAnswersTheSameForEveryAddress(t *testing.T) {
	app, store := confirmStoreApp(t)
	r := app.Router()
	pending := confirmPendingUser(t, store, "ana@example.com")
	old := confirmToken(t, store, pending.ID)
	insertWebUser(t, store, "ativo@example.com")

	var bodies []string
	for i, email := range []string{"ana@example.com", "ninguem@example.com", "ativo@example.com", "not an address"} {
		rec := confirmResend(r, email, fmt.Sprintf("198.51.100.%d", 10+i))
		body := confirmAssertPage(t, rec, http.StatusOK, "Confirmar e-mail", confirmSentText, `role="status"`)
		if strings.Contains(body, email) {
			t.Errorf("the page for %q echoes the address", email)
		}
		bodies = append(bodies, body)
	}
	for i, body := range bodies[1:] {
		if body != bodies[0] {
			t.Errorf("the page for address %d differs from the page for the registered one:\n%s\nversus\n%s", i+1, body, bodies[0])
		}
	}

	// Only the account that waits for its confirmation got a message, and its
	// old link no longer works.
	msgs := confirmOutbox(t, store)
	if len(msgs) != 1 || msgs[0].To != "ana@example.com" || !strings.Contains(msgs[0].Text, testOrigin+"/confirmar?token=") {
		t.Fatalf("outbox = %+v, want one verification email to ana@example.com", msgs)
	}
	if strings.Contains(msgs[0].Text, old) {
		t.Error("the new email carries the old token")
	}
	rec := confirmPost(r, "/confirmar", url.Values{"token": {old}}, "198.51.100.20")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("old link: status = %d, want 400", rec.Code)
	}
}

func TestConfirmResendRateLimitPerIP(t *testing.T) {
	app, _ := confirmStoreApp(t)
	r := app.Router()
	// Each request names another address, so only the IP key can trip.
	for i := 1; i <= 5; i++ {
		rec := confirmResend(r, fmt.Sprintf("u%d@example.com", i), "198.51.100.30")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
	rec := confirmResend(r, "u6@example.com", "198.51.100.30")
	body := confirmAssertPage(t, rec, http.StatusTooManyRequests, "Confirmar e-mail", append([]string{msgTooMany, `role="alert"`}, confirmResendForm...)...)
	if strings.Contains(body, confirmSentText) {
		t.Error("the 429 page says a link was sent")
	}

	// Another client address is not affected.
	if rec := confirmResend(r, "u7@example.com", "198.51.100.31"); rec.Code != http.StatusOK {
		t.Errorf("other IP: status = %d, want 200", rec.Code)
	}
}

func TestConfirmResendRateLimitPerEmail(t *testing.T) {
	app, _ := confirmStoreApp(t)
	r := app.Router()
	// One address, spelled in different ways, from a new client address each
	// time: only the email key can trip.
	for i, email := range []string{"ana@example.com", "ANA@example.com", " ana@example.com ", "Ana@Example.com", "ana@EXAMPLE.COM"} {
		rec := confirmResend(r, email, fmt.Sprintf("198.51.100.%d", 40+i))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i+1, rec.Code)
		}
	}
	rec := confirmResend(r, "ana@example.com", "198.51.100.50")
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), msgTooMany) {
		t.Fatalf("6th request for one address: status = %d, body %q, want 429 with msgTooMany", rec.Code, rec.Body)
	}
	// Another address from a new client address is fine.
	if rec := confirmResend(r, "bia@example.com", "198.51.100.51"); rec.Code != http.StatusOK {
		t.Errorf("another address: status = %d, want 200", rec.Code)
	}
}

func TestConfirmResendRefusedIPDoesNotSpendTheEmailBudget(t *testing.T) {
	app, _ := confirmStoreApp(t)
	r := app.Router()
	for i := 1; i <= 5; i++ {
		confirmResend(r, fmt.Sprintf("u%d@example.com", i), "198.51.100.60")
	}
	// The address is named only while its client is refused ...
	for i := 0; i < 3; i++ {
		if rec := confirmResend(r, "ana@example.com", "198.51.100.60"); rec.Code != http.StatusTooManyRequests {
			t.Fatalf("refused request %d: status = %d, want 429", i+1, rec.Code)
		}
	}
	// ... so its whole budget is still there for other clients.
	for i := 0; i < 5; i++ {
		if rec := confirmResend(r, "ana@example.com", fmt.Sprintf("198.51.100.%d", 61+i)); rec.Code != http.StatusOK {
			t.Fatalf("request %d from a new client: status = %d, want 200", i+1, rec.Code)
		}
	}
}

func TestConfirmResendWithoutAnAddressSpendsNoEmailBudget(t *testing.T) {
	app, _ := confirmStoreApp(t)
	r := app.Router()
	// Four requests with no address, from four clients, then the budget of the
	// empty address is not what stops a fifth.
	for i := 0; i < 6; i++ {
		if rec := confirmResend(r, "", fmt.Sprintf("198.51.100.%d", 70+i)); rec.Code != http.StatusOK {
			t.Fatalf("request %d without an address: status = %d, want 200", i+1, rec.Code)
		}
	}
}

func TestConfirmResendBadBodyIs400(t *testing.T) {
	r := confirmApp(t, stubStore{}).Router()
	rec := post(r, "/confirmar/reenviar", "email=%ff", header("CF-Connecting-IP", "198.51.100.80"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestConfirmResendServerErrorIsLoggedWithoutTheAddress(t *testing.T) {
	_, store := confirmStoreApp(t)
	confirmPendingUser(t, store, "ana@example.com")
	app := confirmApp(t, confirmFailingStore{store})
	logs := captureLogs(app)

	rec := confirmResend(app.Router(), "ana@example.com", "198.51.100.90")
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "erro interno" {
		t.Fatalf("status = %d, body %q, want 500 erro interno", rec.Code, rec.Body)
	}
	log := logs.String()
	if !strings.Contains(log, "the database is down") {
		t.Errorf("the error is not logged: %s", log)
	}
	if strings.Contains(log, "ana@example.com") || strings.Contains(rec.Body.String(), "ana@example.com") {
		t.Errorf("the address reached the log or the page: %s", log)
	}
}

// --- GET /aguardando ---

func TestAguardandoPage(t *testing.T) {
	r := confirmApp(t, stubStore{}).Router()
	rec := serve(r, http.MethodGet, "/aguardando")
	confirmAssertPage(t, rec, http.StatusOK, "Cadastro em análise", confirmWaitingText, `href="/entrar"`)
}

// --- the whole flow ---

func TestConfirmFlowKeepsTheTokenAndTheAddressOutOfTheLogs(t *testing.T) {
	app, store := confirmStoreApp(t)
	logs := captureLogs(app)
	r := app.Router()
	u := confirmPendingUser(t, store, "ana@example.com")
	insertWebUser(t, store, "admin@example.com", func(u *domain.User) { u.IsAdmin = true })
	raw := confirmToken(t, store, u.ID)

	confirmResend(r, "ana@example.com", "198.51.100.100")
	msgs := confirmOutbox(t, store)
	if len(msgs) != 1 {
		t.Fatalf("outbox = %+v, want one verification email", msgs)
	}
	i := strings.Index(msgs[0].Text, testOrigin+"/confirmar?token=")
	if i < 0 {
		t.Fatalf("no link in %q", msgs[0].Text)
	}
	fresh := strings.Fields(msgs[0].Text[i+len(testOrigin+"/confirmar?token="):])[0]

	for _, token := range []string{raw, fresh} {
		serve(r, http.MethodGet, "/confirmar?token="+token)
		confirmPost(r, "/confirmar", url.Values{"token": {token}}, "198.51.100.100")
	}
	if got := confirmUserStatus(t, store, u.ID); got != domain.UserStatusPendingApproval {
		t.Errorf("status = %q, want pending_approval", got)
	}
	log := logs.String()
	for _, secret := range []string{raw, fresh, "ana@example.com"} {
		if strings.Contains(log, secret) {
			t.Errorf("the log holds %q: %s", secret, log)
		}
	}
}
