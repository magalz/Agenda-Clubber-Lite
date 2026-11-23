package web

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The account of every profile test, and the address it wants to move to.
const (
	profileEmail    = "ana.perfil@example.com"
	profileNewEmail = "nova.perfil@example.com"
	profileName     = "Ana Souza"
	profilePhone    = "(81) 98765-4321"
	profileDigits   = "81987654321"

	profilePath       = "/perfil"
	profileEmailPath  = "/perfil/email"
	profileConfirmURL = "/confirmar-email"
)

// The fixed texts the page must show for the five ?msg= codes. The URL never
// supplies text, and an unknown code shows none of these.
const (
	profileMsgSaved   = "perfil salvo."
	profileMsgPending = "se o e-mail estiver disponível, enviamos um link de confirmação. ele vale por 24 horas."
	profileMsgChanged = "e-mail confirmado. seu endereço foi atualizado."
	profileMsgInvalid = "este link expirou ou já foi usado."
	profileMsgInUse   = "este e-mail já está em uso por outra conta. peça a troca novamente."
)

var profileNotices = []struct{ code, text string }{
	{msgPerfilSaved, profileMsgSaved},
	{msgEmailPending, profileMsgPending},
	{msgEmailChanged, profileMsgChanged},
	{msgEmailInvalid, profileMsgInvalid},
	{msgEmailInUse, profileMsgInUse},
}

// profileEnv is an App on a database of its own with one signed-in account.
type profileEnv struct {
	app    *App
	store  domain.Store
	r      http.Handler
	logs   *bytes.Buffer
	user   domain.User
	cookie *http.Cookie
}

func newProfileEnv(t *testing.T) profileEnv {
	t.Helper()
	app, store := signupStoreApp(t)
	logs := captureLogs(app)
	u := insertWebUser(t, store, profileEmail, func(u *domain.User) {
		u.Name = profileName
		u.Phone = profileDigits
		u.PhoneIsWhatsApp = true
	})
	return profileEnv{app: app, store: store, r: app.Router(), logs: logs, user: u, cookie: loginAs(t, app, u)}
}

// show is GET /perfil, with a notice code when one is asked for.
func (e profileEnv) show(msg ...string) *httptest.ResponseRecorder {
	target := profilePath
	if len(msg) > 0 {
		target += "?msg=" + url.QueryEscape(msg[0])
	}
	return serve(e.r, http.MethodGet, target, withCookie(e.cookie))
}

// saveForm is a profile post with the account's own data.
func saveForm(name, phone string, whatsapp bool) url.Values {
	v := url.Values{"name": {name}, "phone": {phone}}
	if whatsapp {
		v.Set("phone_is_whatsapp", "1")
	}
	return v
}

func (e profileEnv) save(form url.Values, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	return post(e.r, profilePath, form.Encode(), append([]func(*http.Request){withCookie(e.cookie)}, mods...)...)
}

func (e profileEnv) askEmail(email string, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	return post(e.r, profileEmailPath, url.Values{"email": {email}}.Encode(), append([]func(*http.Request){withCookie(e.cookie)}, mods...)...)
}

// confirm clicks the mailed link, with or without a session.
func (e profileEnv) confirm(raw string, cookie bool) *httptest.ResponseRecorder {
	target := profileConfirmURL + "?token=" + url.QueryEscape(raw)
	if !cookie {
		return serve(e.r, http.MethodGet, target)
	}
	return serve(e.r, http.MethodGet, target, withCookie(e.cookie))
}

func (e profileEnv) storedUser(t *testing.T) domain.User {
	t.Helper()
	u, err := e.store.Repos().Users.ByID(context.Background(), e.user.ID)
	if err != nil {
		t.Fatalf("ByID: %v", err)
	}
	return u
}

// profileOutbox claims what is waiting at the test clock; call it once per test.
func profileOutbox(t *testing.T, store domain.Store) []domain.OutboxMessage {
	t.Helper()
	msgs, err := store.Repos().Outbox.Claim(context.Background(), webTestNow, time.Minute, 10)
	if err != nil {
		t.Fatalf("Claim outbox: %v", err)
	}
	return msgs
}

// profileLinkToken pulls the raw token out of the confirmation email.
var profileLinkTokenPattern = regexp.MustCompile(`confirmar-email\?token=([^\s<"]+)`)

func profileLinkToken(t *testing.T, msgs []domain.OutboxMessage) string {
	t.Helper()
	for _, m := range msgs {
		if match := profileLinkTokenPattern.FindStringSubmatch(m.Text); match != nil {
			return match[1]
		}
	}
	t.Fatal("no confirmation link in the outbox")
	return ""
}

// profileWantRedirect checks the plain-post redirect the flow promises.
func profileWantRedirect(t *testing.T, rec *httptest.ResponseRecorder, to string) {
	t.Helper()
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != to {
		t.Errorf("status = %d, Location = %q, want 303 to %q", rec.Code, rec.Header().Get("Location"), to)
	}
}

// --- GET /perfil ---

func TestProfilePageShowsTheSignedInPerson(t *testing.T) {
	e := newProfileEnv(t)
	body := confirmAssertPage(t, e.show(), http.StatusOK, "Editar Perfil",
		`<form method="post" action="/perfil"`,
		`name="name" value="`+profileName+`"`,
		`name="phone" value="`+profileDigits+`"`,
		`name="phone_is_whatsapp" value="1" checked`,
		`id="email-atual" value="`+profileEmail+`" readonly`,
		`<form method="post" action="/perfil/email"`,
		`name="email"`,
		`href="/senha/trocar"`,
		`~/ `, `$ `)
	if !strings.Contains(body, `href="/perfil"`) || !strings.Contains(body, `aria-current="page"`) {
		t.Error("the profile page does not mark Editar Perfil as the current nav item")
	}
}

func TestProfilePageNeedsASession(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	rec := serve(r, http.MethodGet, profilePath)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/entrar?next=%2Fperfil" {
		t.Errorf("GET = %d to %q, want 303 to the login with ?next=", rec.Code, rec.Header().Get("Location"))
	}
	rec = serve(r, http.MethodGet, profilePath, hx)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("HX-Redirect") != "/entrar" {
		t.Errorf("HTMX GET = %d with HX-Redirect %q, want 401 with /entrar", rec.Code, rec.Header().Get("HX-Redirect"))
	}
}

func TestProfileNoticesComeFromTheFixedTable(t *testing.T) {
	e := newProfileEnv(t)
	for _, n := range profileNotices {
		t.Run(n.code, func(t *testing.T) {
			body := confirmAssertPage(t, e.show(n.code), http.StatusOK, "Editar Perfil", n.text)
			if strings.Contains(body, "?"+n.code) || strings.Contains(body, "msg="+n.code) {
				t.Errorf("the page echoes the code %q", n.code)
			}
		})
	}
	body := confirmAssertPage(t, e.show("nao-existe"), http.StatusOK, "Editar Perfil")
	for _, n := range profileNotices {
		if strings.Contains(body, n.text) {
			t.Errorf("an unknown code shows the text %q", n.text)
		}
	}
	if strings.Contains(body, "nao-existe") {
		t.Error("the page echoes an unknown code")
	}
}

func TestProfilePostsNeedTheAppsOrigin(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	for _, path := range []string{profilePath, profileEmailPath} {
		for name, mod := range map[string]func(*http.Request){
			"no Origin":    func(*http.Request) {},
			"other Origin": header("Origin", "https://evil.test"),
		} {
			rec := do(r, http.MethodPost, path, strings.NewReader("name=x"),
				header("Content-Type", "application/x-www-form-urlencoded"), mod)
			if rec.Code != http.StatusForbidden {
				t.Errorf("POST %s with %s = %d, want 403", path, name, rec.Code)
			}
		}
	}
}

// --- POST /perfil ---

func TestProfileSaveUpdatesTheAccountAndRedirects(t *testing.T) {
	e := newProfileEnv(t)
	rec := e.save(saveForm("Ana Maria Souza", "(81) 99999-1234", false))
	profileWantRedirect(t, rec, profilePath+"?msg="+msgPerfilSaved)

	u := e.storedUser(t)
	if u.Name != "Ana Maria Souza" || u.Phone != "81999991234" || u.PhoneIsWhatsApp {
		t.Errorf("stored profile = %q %q whatsapp=%v", u.Name, u.Phone, u.PhoneIsWhatsApp)
	}
	if !u.UpdatedAt.Equal(webTestNow) {
		t.Errorf("UpdatedAt = %v, want %v", u.UpdatedAt, webTestNow)
	}
	if sessionGone(t, e.app, e.cookie.Value) {
		t.Error("saving the profile ended the session")
	}
}

func TestProfileSaveRefusesBadFieldsAndKeepsThem(t *testing.T) {
	for _, tc := range []struct {
		name, field, message, kept string
	}{
		{"a missing name", "name", domain.MsgRequired, `name="phone" value="` + profileDigits + `"`},
		{"a bad phone", "phone", "informe de 10 a 11 dígitos", `name="phone" value="abc"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newProfileEnv(t)
			posted := saveForm(profileName, profileDigits, true)
			if tc.field == "name" {
				posted.Set("name", "")
			} else {
				posted.Set("phone", "abc")
			}
			rec := e.save(posted)
			body := confirmAssertPage(t, rec, http.StatusUnprocessableEntity, "Editar Perfil",
				`id="erro-`+tc.field+`">`+tc.message, tc.kept, `aria-invalid="true"`)
			if strings.Contains(body, profileMsgSaved) {
				t.Error("a refused form also claims the profile was saved")
			}

			// The account is untouched: a refused form writes nothing.
			u := e.storedUser(t)
			if u.Name != profileName || u.Phone != profileDigits || !u.PhoneIsWhatsApp {
				t.Errorf("a refused form changed the account: %q %q whatsapp=%v", u.Name, u.Phone, u.PhoneIsWhatsApp)
			}
		})
	}
}

func TestProfileSaveRedirectsHTMXRequests(t *testing.T) {
	e := newProfileEnv(t)
	rec := e.save(saveForm("Ana Maria Souza", profilePhone, true), hx)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != profilePath+"?msg="+msgPerfilSaved {
		t.Errorf("HTMX save = %d with HX-Redirect %q, want 204 and the notice redirect", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if got := e.storedUser(t).Name; got != "Ana Maria Souza" {
		t.Errorf("stored name = %q, want the posted one", got)
	}
}

// --- POST /perfil/email ---

func TestProfileEmailChangeEnqueuesAndRedirects(t *testing.T) {
	e := newProfileEnv(t)
	rec := e.askEmail(profileNewEmail)
	profileWantRedirect(t, rec, profilePath+"?msg="+msgEmailPending)

	msgs := profileOutbox(t, e.store)
	if len(msgs) != 2 {
		t.Fatalf("%d queued emails, want a confirmation and a notice", len(msgs))
	}
	var toNew, toOld bool
	for _, m := range msgs {
		switch m.To {
		case profileNewEmail:
			toNew = strings.Contains(m.Text, profileConfirmURL)
		case profileEmail:
			toOld = true
		}
	}
	if !toNew || !toOld {
		t.Errorf("queued to new=%v old=%v, want both addresses", toNew, toOld)
	}

	// Nothing changes until the link is followed.
	if got := e.storedUser(t).Email; got != profileEmail {
		t.Errorf("email = %q, want it unchanged until confirmation", got)
	}
	if strings.Contains(rec.Body.String(), profileNewEmail) {
		t.Error("the redirect names the new address")
	}
}

func TestProfileEmailChangeLimitsTheForm(t *testing.T) {
	e := newProfileEnv(t)
	e.app.Limits.EmailAction = auth.NewLimiter(1, time.Hour)

	profileWantRedirect(t, e.askEmail(profileNewEmail), profilePath+"?msg="+msgEmailPending)
	body := confirmAssertPage(t, e.askEmail("outra@example.com"), http.StatusTooManyRequests, "Editar Perfil",
		msgTooMany, `role="alert"`, `name="email" value="outra@example.com"`, profileName)
	if msgs := profileOutbox(t, e.store); len(msgs) != 2 {
		t.Errorf("%d queued emails after a refused post, want 2", len(msgs))
	}
	if strings.Contains(body, profileMsgPending) {
		t.Error("a refused post claims a link was sent")
	}
}

func TestProfileEmailChangeRefusesTheCurrentAddress(t *testing.T) {
	e := newProfileEnv(t)
	body := confirmAssertPage(t, e.askEmail(profileEmail), http.StatusUnprocessableEntity, "Editar Perfil",
		`id="erro-email">este já é o seu e-mail`, `name="email" value="`+profileEmail+`"`, profileName)
	if strings.Contains(body, profileMsgPending) {
		t.Error("a refused email claims a link was sent")
	}
	if msgs := profileOutbox(t, e.store); len(msgs) != 0 {
		t.Errorf("%d queued emails for the current address, want none", len(msgs))
	}
}

// --- GET /confirmar-email ---

func TestProfileConfirmChangesTheEmailAndRedirects(t *testing.T) {
	e := newProfileEnv(t)
	rec := e.askEmail(profileNewEmail)
	profileWantRedirect(t, rec, profilePath+"?msg="+msgEmailPending)
	raw := profileLinkToken(t, profileOutbox(t, e.store))

	profileWantRedirect(t, e.confirm(raw, true), profilePath+"?msg="+msgEmailChanged)
	if got := e.storedUser(t).Email; got != profileNewEmail {
		t.Errorf("stored email = %q, want %q", got, profileNewEmail)
	}
}

func TestProfileConfirmWorksForAnAnonymousVisitor(t *testing.T) {
	e := newProfileEnv(t)
	e.askEmail(profileNewEmail)
	raw := profileLinkToken(t, profileOutbox(t, e.store))

	// The link is public: no cookie reaches it, and it still swaps the address.
	profileWantRedirect(t, e.confirm(raw, false), profilePath+"?msg="+msgEmailChanged)
	if got := e.storedUser(t).Email; got != profileNewEmail {
		t.Errorf("stored email = %q, want %q", got, profileNewEmail)
	}
}

func TestProfileConfirmRefusesAnInvalidLink(t *testing.T) {
	e := newProfileEnv(t)
	for _, raw := range []string{"", "not-a-token"} {
		profileWantRedirect(t, e.confirm(raw, true), profilePath+"?msg="+msgEmailInvalid)
	}
	if got := e.storedUser(t).Email; got != profileEmail {
		t.Errorf("an invalid link changed the email to %q", got)
	}
}

func TestProfileConfirmReportsAnInterimTakenAddress(t *testing.T) {
	e := newProfileEnv(t)
	e.askEmail(profileNewEmail)
	raw := profileLinkToken(t, profileOutbox(t, e.store))

	// Another account takes the address while the link is on its way.
	insertWebUser(t, e.store, profileNewEmail)

	profileWantRedirect(t, e.confirm(raw, true), profilePath+"?msg="+msgEmailInUse)
	if got := e.storedUser(t).Email; got != profileEmail {
		t.Errorf("email = %q, want the original address", got)
	}
	other, err := e.store.Repos().Users.ByEmail(context.Background(), profileNewEmail)
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}
	if other.Email != profileNewEmail {
		t.Errorf("the other account's email = %q", other.Email)
	}
}

// --- what the page does not say ---

func TestProfilePagesShowNoOtherAddress(t *testing.T) {
	e := newProfileEnv(t)
	insertWebUser(t, e.store, "alheio@example.com", func(u *domain.User) { u.Name = "Outra Pessoa" })
	body := confirmAssertPage(t, e.show(msgEmailPending), http.StatusOK, "Editar Perfil", profileMsgPending)
	for _, bad := range []string{"alheio@example.com", "Outra Pessoa", loginTestPassword, signupPassword} {
		if strings.Contains(body, bad) {
			t.Errorf("the page shows %q", bad)
		}
	}
}
