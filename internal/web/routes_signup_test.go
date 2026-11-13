package web

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
)

// signupCIDR covers 192.0.2.1, the remote address httptest gives every request.
// The App trusts CF-Connecting-IP only from such a proxy, so a test picks the
// client address of a request with that header.
const signupCIDR = "192.0.2.0/24"

// signupPassword is a password the policy accepts and that no page text
// contains: if it shows up in a response, a handler wrote a password back.
const signupPassword = "Zq7#kLm-Sentinel-91"

// signupApp builds the production-mode App of these tests around the store.
func signupApp(t *testing.T, store domain.Store) *App {
	t.Helper()
	app := NewApp(testConfig(t, signupCIDR), slog.New(slog.NewJSONHandler(&strings.Builder{}, nil)), store)
	app.Now = func() time.Time { return webTestNow }
	return app
}

// signupStoreApp is signupApp on an empty MongoDB database of its own. It skips
// locally, and fails in CI, without MONGO_TEST_URI.
func signupStoreApp(t *testing.T) (*App, domain.Store) {
	t.Helper()
	store := mongotest.New(t)
	return signupApp(t, store), store
}

// signupValues is a form the service accepts: a new collective and an owner.
// mod changes it.
func signupValues(mod ...func(url.Values)) url.Values {
	v := url.Values{
		"name":             {"Ana Souza"},
		"email":            {"ana@example.com"},
		"phone":            {"(81) 98765-4321"},
		"phone_whatsapp":   {"1"},
		"password":         {signupPassword},
		"password_confirm": {signupPassword},
		"collective_kind":  {"coletivo"},
		"collective_id":    {""},
		"collective_name":  {"Coletivo Novo Teste"},
		"role":             {"owner"},
	}
	for _, m := range mod {
		m(v)
	}
	return v
}

// signupInvalid makes a form fail validation, so a test on the stub store never
// reaches the database: only the limiters can then refuse the request.
func signupInvalid(v url.Values) { v.Set("name", "") }

// signupPost posts the form from a client address.
func signupPost(r http.Handler, v url.Values, ip string) *httptest.ResponseRecorder {
	return post(r, "/cadastro", v.Encode(), header("CF-Connecting-IP", ip))
}

// signupSuggest asks for suggestions from a client address.
func signupSuggest(r http.Handler, q url.Values, ip string) *httptest.ResponseRecorder {
	return serve(r, http.MethodGet, "/coletivos/sugestoes?"+q.Encode(), header("CF-Connecting-IP", ip))
}

var signupInputTag = regexp.MustCompile(`<input\b[^>]*>`)

// signupInputs returns the <input> tags named name, narrowed to those holding
// value when it is not empty.
func signupInputs(body, name, value string) []string {
	var found []string
	for _, tag := range signupInputTag.FindAllString(body, -1) {
		if !strings.Contains(tag, ` name="`+name+`"`) {
			continue
		}
		if value != "" && !strings.Contains(tag, ` value="`+value+`"`) {
			continue
		}
		found = append(found, tag)
	}
	return found
}

// signupInput returns the one <input> named name that holds value.
func signupInput(t *testing.T, body, name, value string) string {
	t.Helper()
	tags := signupInputs(body, name, value)
	if len(tags) != 1 {
		t.Fatalf("got %d <input name=%q value=%q>, want 1 in:\n%s", len(tags), name, value, body)
	}
	return tags[0]
}

// signupInsertCollective stores a collective in the given state.
func signupInsertCollective(t *testing.T, store domain.Store, name string, kind domain.CollectiveKind, status domain.CollectiveStatus) domain.Collective {
	t.Helper()
	c := domain.Collective{
		Name:      name,
		NameKey:   domain.NameKey(name),
		Kind:      kind,
		Status:    status,
		CreatedAt: webTestNow,
		UpdatedAt: webTestNow,
	}
	if err := store.Repos().Collectives.Create(context.Background(), &c); err != nil {
		t.Fatalf("insert collective %q: %v", name, err)
	}
	return c
}

// signupFailingStore is a store whose transactions fail, so a signup that
// passed validation ends in a server error.
type signupFailingStore struct{ stubStore }

func (signupFailingStore) WithTx(context.Context, func(context.Context, domain.Repos) error) error {
	return errors.New("the database is down")
}

// --- the checklist ---

func TestPasswordChecklist(t *testing.T) {
	rules := auth.Rules()

	plain := passwordChecklist(false)
	if len(plain) != len(rules) {
		t.Fatalf("passwordChecklist(false) has %d items, want %d", len(plain), len(rules))
	}
	for i, r := range rules {
		if plain[i].Rule != string(r) || plain[i].Label != auth.RuleLabels[r] {
			t.Errorf("item %d = %+v, want rule %q with label %q", i, plain[i], r, auth.RuleLabels[r])
		}
	}

	withHistory := passwordChecklist(true)
	if len(withHistory) != len(rules)+1 {
		t.Fatalf("passwordChecklist(true) has %d items, want %d", len(withHistory), len(rules)+1)
	}
	last := withHistory[len(withHistory)-1]
	if last.Rule != string(auth.RuleHistory) || last.Label != auth.RuleLabels[auth.RuleHistory] {
		t.Errorf("last item = %+v, want the history rule", last)
	}

	// A call does not leak into the next one.
	if again := passwordChecklist(false); len(again) != len(rules) {
		t.Errorf("passwordChecklist(false) after a call with history has %d items, want %d", len(again), len(rules))
	}
}

func TestPasswordFieldsPartial(t *testing.T) {
	app := newTestApp(t)
	render := func(t *testing.T, errs domain.FieldErrors, autocomplete string) string {
		t.Helper()
		defer func() {
			t.Helper()
			if v := recover(); v != nil {
				t.Fatalf("render password-fields: %v", v)
			}
		}()
		c, rec := newCtx()
		app.Render.Partial(c, http.StatusOK, "password-fields", map[string]any{
			"Errors":       errs,
			"Checklist":    passwordChecklist(true),
			"Autocomplete": autocomplete,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %q", rec.Code, rec.Body)
		}
		return rec.Body.String()
	}

	t.Run("without errors", func(t *testing.T) {
		body := render(t, nil, "new-password")
		for _, name := range []string{"password", "password_confirm"} {
			tag := signupInput(t, body, name, "")
			for _, want := range []string{`type="password"`, ` id="` + name + `"`, `autocomplete="new-password"`, ` required`} {
				if !strings.Contains(tag, want) {
					t.Errorf("%s input %s lacks %q", name, tag, want)
				}
			}
			if strings.Contains(tag, "value=") || strings.Contains(tag, "aria-invalid") {
				t.Errorf("%s input %s has a value or aria-invalid", name, tag)
			}
		}
		if tag := signupInput(t, body, "password", ""); !strings.Contains(tag, `aria-describedby="password-checklist"`) {
			t.Errorf("password input %s does not point at the checklist", tag)
		}
		if got := strings.Count(body, `class="checklist__item"`); got != len(passwordChecklist(true)) {
			t.Errorf("%d checklist items, want %d", got, len(passwordChecklist(true)))
		}
		if strings.Contains(body, "field-error") {
			t.Errorf("a field error without errors: %s", body)
		}
		for _, r := range passwordChecklist(true) {
			item := `<li class="checklist__item" data-rule="` + r.Rule + `">` + r.Label + `<span class="sr-only"> (pendente)</span></li>`
			if !strings.Contains(body, item) {
				t.Errorf("missing checklist item %s in:\n%s", item, body)
			}
		}
		if !strings.Contains(body, `<ul class="checklist" id="password-checklist"`) {
			t.Errorf("missing the checklist list:\n%s", body)
		}
	})

	t.Run("with errors", func(t *testing.T) {
		body := render(t, domain.FieldErrors{"password": "senha fraca", "password_confirm": "as senhas não conferem"}, "current-password")
		pw := signupInput(t, body, "password", "")
		for _, want := range []string{`aria-invalid="true"`, `autocomplete="current-password"`, "password-checklist", "erro-password"} {
			if !strings.Contains(pw, want) {
				t.Errorf("password input %s lacks %q", pw, want)
			}
		}
		confirm := signupInput(t, body, "password_confirm", "")
		for _, want := range []string{`aria-invalid="true"`, `aria-describedby="erro-password_confirm"`} {
			if !strings.Contains(confirm, want) {
				t.Errorf("confirmation input %s lacks %q", confirm, want)
			}
		}
		for _, want := range []string{`id="erro-password"`, `id="erro-password_confirm"`, "senha fraca", "as senhas não conferem"} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q in:\n%s", want, body)
			}
		}
	})
}

// --- the pages ---

func TestSignupFormPage(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	rec := serve(r, http.MethodGet, "/cadastro")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != htmlContentType {
		t.Errorf("Content-Type = %q, want %q", ct, htmlContentType)
	}
	body := rec.Body.String()
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}
	for _, want := range []string{"<title>Criar conta · AGENDA CLUBBER</title>", `<h1 class="panel__title">Criar conta</h1>`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}

	// It works without JavaScript: a plain form post.
	form := regexp.MustCompile(`<form\b[^>]*>`).FindString(body)
	if !strings.Contains(form, `method="post"`) || !strings.Contains(form, `action="/cadastro"`) {
		t.Errorf("form tag = %q, want a post to /cadastro", form)
	}

	// Every text field has its own label.
	for _, id := range []string{"name", "email", "phone", "password", "password_confirm", "collective_name"} {
		if !regexp.MustCompile(`<label\b[^>]*\sfor="` + id + `"`).MatchString(body) {
			t.Errorf("no <label for=%q>", id)
		}
		if tag := signupInput(t, body, id, ""); !strings.Contains(tag, ` id="`+id+`"`) {
			t.Errorf("input %s has no id %q", tag, id)
		}
	}
	// The two radio groups have a legend, and each option a label around it.
	if got := strings.Count(body, "<legend"); got != 2 {
		t.Errorf("%d <legend>, want 2 (tipo, papel)", got)
	}
	for _, c := range []struct{ name, value, label string }{
		{"collective_kind", "coletivo", "Coletivo"},
		{"collective_kind", "produtora", "Produtora"},
		{"role", "owner", "Dono"},
		{"role", "member", "Membro"},
	} {
		tag := signupInput(t, body, c.name, c.value)
		if !strings.Contains(tag, `type="radio"`) {
			t.Errorf("%s is not a radio", tag)
		}
		if !strings.Contains(body, c.label) {
			t.Errorf("missing the label %q", c.label)
		}
	}
	if tag := signupInput(t, body, "phone_whatsapp", "1"); !strings.Contains(tag, `type="checkbox"`) {
		t.Errorf("phone_whatsapp = %s, want a checkbox", tag)
	}
	if tag := signupInput(t, body, "phone", ""); !strings.Contains(tag, `inputmode="tel"`) || !strings.Contains(tag, `autocomplete="tel"`) {
		t.Errorf("phone input = %s, want inputmode and autocomplete tel", tag)
	}
	if tag := signupInput(t, body, "collective_id", ""); !strings.Contains(tag, `type="hidden"`) || strings.Contains(tag, `value="`) && !strings.Contains(tag, `value=""`) {
		t.Errorf("collective_id = %s, want an empty hidden input", tag)
	}

	// No password value, and the checklist is pending.
	for _, name := range []string{"password", "password_confirm"} {
		tag := signupInput(t, body, name, "")
		if strings.Contains(tag, "value=") {
			t.Errorf("%s input has a value: %s", name, tag)
		}
		if !strings.Contains(tag, `autocomplete="new-password"`) || !strings.Contains(tag, "required") {
			t.Errorf("%s input = %s, want new-password and required", name, tag)
		}
	}
	rules := auth.Rules()
	if got := strings.Count(body, `class="checklist__item"`); got != len(rules) {
		t.Errorf("%d checklist items, want %d (no history)", got, len(rules))
	}
	if got := strings.Count(body, " (pendente)"); got != len(rules) {
		t.Errorf("%d pending items, want %d", got, len(rules))
	}
	if strings.Contains(body, `data-rule="history"`) {
		t.Error("the signup checklist lists the history rule")
	}

	// The documented order, hints and the empty suggestions container.
	inOrder(t, body,
		`name="name"`, `name="email"`, `name="phone"`, `name="phone_whatsapp"`,
		`name="password"`, `name="password_confirm"`, `id="password-checklist"`,
		`name="collective_kind"`, `name="collective_name"`, `name="collective_id"`, `id="collective-suggestions-box"`,
		`name="role"`, "[→] CRIAR CONTA", "já tenho conta",
	)
	for _, want := range []string{
		"DDD + número, sem +55",
		"comece a digitar para ver os já cadastrados, ou escreva um nome novo",
		"quem cria um coletivo novo entra como dono",
		`href="/entrar"`,
		`<div id="collective-suggestions-box"></div>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, `role="alert"`) || strings.Contains(body, `class="field-error"`) {
		t.Error("a fresh form shows a notice or an error")
	}
}

func TestSignupSentPage(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	rec := serve(r, http.MethodGet, "/cadastro/enviado")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}
	for _, want := range []string{
		"<title>Confira seu e-mail · AGENDA CLUBBER</title>",
		"se este e-mail ainda não tinha conta, enviamos um link de confirmação que vale por 24 horas. se já tinha, enviamos instruções para entrar.",
		`href="/entrar"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

// --- POST /cadastro ---

func TestSignupCreatesAnAccount(t *testing.T) {
	app, store := signupStoreApp(t)
	r := app.Router()

	rec := signupPost(r, signupValues(), "198.51.100.1")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/cadastro/enviado" {
		t.Fatalf("status = %d, Location %q, body %q, want 303 /cadastro/enviado", rec.Code, rec.Header().Get("Location"), rec.Body)
	}

	u, err := store.Repos().Users.ByEmail(context.Background(), "ana@example.com")
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}
	if u.Status != domain.UserStatusPendingEmail || u.Name != "Ana Souza" || u.Phone != "81987654321" {
		t.Errorf("user = %+v, want a pending_email Ana Souza with the digits of the phone", u)
	}
	msgs, err := store.Repos().Outbox.Claim(context.Background(), webTestNow, time.Minute, 10)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if len(msgs) != 1 || msgs[0].To != "ana@example.com" || !strings.Contains(msgs[0].Text, testOrigin+"/confirmar?token=") {
		t.Fatalf("outbox = %+v, want one verification email with the confirmation link", msgs)
	}

	// An HTMX post gets the redirect as a header.
	hxRec := post(r, "/cadastro", signupValues(func(v url.Values) {
		v.Set("email", "bia@example.com")
		v.Set("collective_name", "Outro Coletivo Teste")
	}).Encode(),
		header("CF-Connecting-IP", "198.51.100.2"), hx)
	if hxRec.Code != http.StatusNoContent || hxRec.Header().Get("HX-Redirect") != "/cadastro/enviado" {
		t.Errorf("HTMX status = %d, HX-Redirect %q, want 204 /cadastro/enviado", hxRec.Code, hxRec.Header().Get("HX-Redirect"))
	}
}

func TestSignupWithARegisteredEmailLooksTheSame(t *testing.T) {
	app, store := signupStoreApp(t)
	existing := insertWebUser(t, store, "ana@example.com")

	rec := signupPost(app.Router(), signupValues(), "198.51.100.1")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/cadastro/enviado" {
		t.Fatalf("status = %d, Location %q, body %q, want 303 /cadastro/enviado", rec.Code, rec.Header().Get("Location"), rec.Body)
	}
	u, err := store.Repos().Users.ByEmail(context.Background(), "ana@example.com")
	if err != nil {
		t.Fatalf("ByEmail: %v", err)
	}
	if u.ID != existing.ID || u.Status != domain.UserStatusActive {
		t.Errorf("user = %+v, want the existing active account untouched", u)
	}
}

func TestSignupFieldErrors(t *testing.T) {
	app := signupApp(t, stubStore{})
	r := app.Router()

	t.Run("every field", func(t *testing.T) {
		rec := signupPost(r, url.Values{
			"name":             {strings.Repeat("a", domain.UserNameMax+1)},
			"email":            {`not "an" <email>`},
			"phone":            {"123"},
			"phone_whatsapp":   {"1"},
			"password":         {signupPassword},
			"password_confirm": {signupPassword + "x"},
			"collective_name":  {"x"},
		}, "198.51.100.1")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, body %q, want 422", rec.Code, rec.Body)
		}
		body := rec.Body.String()
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
		}

		// Each message is next to its field, and the field says it is invalid.
		for _, c := range []struct{ key, field string }{
			{"name", "name"}, {"email", "email"}, {"phone", "phone"}, {"password_confirm", "password_confirm"},
			{"collective_kind", "collective_kind"}, {"collective", "collective_name"}, {"role", "role"},
		} {
			errID := `id="erro-` + c.key + `"`
			if !strings.Contains(body, errID) {
				t.Errorf("no %s", errID)
				continue
			}
			if c.key == "collective_kind" || c.key == "role" {
				group := regexp.MustCompile(`<fieldset\b[^>]*>`).FindAllString(body, -1)
				found := false
				for _, g := range group {
					if strings.Contains(g, "erro-"+c.key) && strings.Contains(g, `aria-invalid="true"`) {
						found = true
					}
				}
				if !found {
					t.Errorf("no fieldset is invalid and described by erro-%s: %v", c.key, group)
				}
				continue
			}
			tag := signupInput(t, body, c.field, "")
			if !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, "erro-"+c.key) {
				t.Errorf("input %s is not invalid and described by erro-%s", tag, c.key)
			}
		}
		// The collective message sits under the collective_name input.
		inOrder(t, body, `name="collective_name"`, `id="erro-collective"`, `name="collective_id"`)
		if strings.Contains(body, `id="erro-password"`) {
			t.Error("a password error for a valid password")
		}

		// What was typed comes back, escaped, except the passwords.
		for _, want := range []string{
			`value="not &#34;an&#34; &lt;email&gt;"`, `value="` + strings.Repeat("a", domain.UserNameMax+1) + `"`, `value="123"`, `value="x"`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing the kept value %s", want)
			}
		}
		if tag := signupInput(t, body, "phone_whatsapp", "1"); !strings.Contains(tag, " checked") {
			t.Errorf("the WhatsApp box lost its tick: %s", tag)
		}
		if strings.Contains(body, signupPassword) {
			t.Error("the response carries a typed password")
		}
		for _, name := range []string{"password", "password_confirm"} {
			if tag := signupInput(t, body, name, ""); strings.Contains(tag, "value=") {
				t.Errorf("%s input has a value: %s", name, tag)
			}
		}
	})

	t.Run("a weak password", func(t *testing.T) {
		rec := signupPost(r, signupValues(func(v url.Values) {
			v.Set("password", "abc")
			v.Set("password_confirm", "abc")
		}), "198.51.100.2")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `id="erro-password"`) {
			t.Errorf("no password error in:\n%s", body)
		}
		if tag := signupInput(t, body, "password", ""); !strings.Contains(tag, `aria-invalid="true"`) || !strings.Contains(tag, "password-checklist") || !strings.Contains(tag, "erro-password") {
			t.Errorf("password input = %s, want invalid and described by the checklist and the error", tag)
		}
		if strings.Contains(body, `value="abc"`) {
			t.Error("the weak password came back in a value")
		}
	})

	t.Run("choices stay chosen", func(t *testing.T) {
		rec := signupPost(r, signupValues(func(v url.Values) {
			v.Set("name", "")
			v.Set("collective_kind", "produtora")
			v.Set("role", "member")
			v.Set("collective_id", "abc123")
		}), "198.51.100.3")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		body := rec.Body.String()
		for _, c := range []struct{ name, value string }{{"collective_kind", "produtora"}, {"role", "member"}} {
			if tag := signupInput(t, body, c.name, c.value); !strings.Contains(tag, " checked") {
				t.Errorf("%s=%s lost its choice: %s", c.name, c.value, tag)
			}
		}
		for _, c := range []struct{ name, value string }{{"collective_kind", "coletivo"}, {"role", "owner"}} {
			if tag := signupInput(t, body, c.name, c.value); strings.Contains(tag, " checked") {
				t.Errorf("%s=%s is checked: %s", c.name, c.value, tag)
			}
		}
		// The collective a person picked from the list stays picked.
		if tag := signupInput(t, body, "collective_id", ""); !strings.Contains(tag, `type="hidden"`) || !strings.Contains(tag, `value="abc123"`) {
			t.Errorf("collective_id = %s, want the picked id kept", tag)
		}
	})
}

func TestSignupBadBodyIs400(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	rec := post(r, "/cadastro", "name=%ff&email=a%40b.c", header("CF-Connecting-IP", "198.51.100.1"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestSignupServerErrorIsLoggedWithoutTheForm(t *testing.T) {
	app := signupApp(t, signupFailingStore{})
	logs := captureLogs(app)
	rec := signupPost(app.Router(), signupValues(), "198.51.100.1")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body %q, want 500", rec.Code, rec.Body)
	}
	log := logs.String()
	if !strings.Contains(log, "the database is down") {
		t.Errorf("the error is not logged: %s", log)
	}
	for _, secret := range []string{signupPassword, "ana@example.com", "Ana Souza", "Coletivo Novo Teste", "81987654321"} {
		if strings.Contains(log, secret) {
			t.Errorf("the log holds form value %q: %s", secret, log)
		}
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("the 500 page holds form value %q", secret)
		}
	}
}

func TestSignupRateLimitPerIP(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	// Each post uses another email: only the IP key trips.
	for i := 1; i <= 5; i++ {
		rec := signupPost(r, signupValues(signupInvalid, func(v url.Values) { v.Set("email", fmt.Sprintf("u%d@example.com", i)) }), "198.51.100.7")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("post %d: status = %d, want 422", i, rec.Code)
		}
	}
	rec := signupPost(r, signupValues(signupInvalid, func(v url.Values) { v.Set("email", "u6@example.com") }), "198.51.100.7")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th post: status = %d, want 429", rec.Code)
	}
	body := rec.Body.String()
	inOrder(t, body, `role="alert"`, msgTooMany, "<form")
	if !strings.Contains(body, `value="u6@example.com"`) {
		t.Error("the 429 page lost what was typed")
	}
	if strings.Contains(body, signupPassword) {
		t.Error("the 429 page carries a typed password")
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in the 429 page", strings.Join(v, "; "))
	}

	// Another client address is not affected.
	if rec := signupPost(r, signupValues(signupInvalid, func(v url.Values) { v.Set("email", "u7@example.com") }), "198.51.100.8"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("other IP: status = %d, want 422", rec.Code)
	}
}

func TestSignupRateLimitPerEmail(t *testing.T) {
	r := signupApp(t, stubStore{}).Router()
	// The same address, spelled in different ways, from a new client address
	// each time: only the email key trips.
	spellings := []string{"ana@example.com", "ANA@example.com", " ana@example.com ", "Ana@Example.com", "ana@EXAMPLE.COM"}
	for i, email := range spellings {
		rec := signupPost(r, signupValues(signupInvalid, func(v url.Values) { v.Set("email", email) }), fmt.Sprintf("198.51.100.%d", 10+i))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("post %d: status = %d, want 422", i+1, rec.Code)
		}
	}
	rec := signupPost(r, signupValues(signupInvalid, func(v url.Values) { v.Set("email", "ana@example.com") }), "198.51.100.20")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th post for one email: status = %d, want 429", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), msgTooMany) || strings.Contains(rec.Body.String(), signupPassword) {
		t.Errorf("429 page = %s, want msgTooMany and no password", rec.Body)
	}

	// Another address from a new client address is fine: the 429 came from the
	// email key, not from the client address.
	if rec := signupPost(r, signupValues(signupInvalid, func(v url.Values) { v.Set("email", "bia@example.com") }), "198.51.100.21"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("another email: status = %d, want 422", rec.Code)
	}

	// A post without an email has no email key to spend.
	for i := 0; i < 5; i++ {
		rec := signupPost(r, signupValues(func(v url.Values) { v.Set("email", "") }), fmt.Sprintf("198.51.100.%d", 30+i))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("post %d without an email: status = %d, want 422", i+1, rec.Code)
		}
	}
	if rec := signupPost(r, signupValues(func(v url.Values) { v.Set("email", "") }), "198.51.100.40"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("6th post without an email: status = %d, want 422", rec.Code)
	}
}

// --- GET /coletivos/sugestoes ---

func TestSignupSuggestions(t *testing.T) {
	app, store := signupStoreApp(t)
	logs := captureLogs(app)
	r := app.Router()

	alpha := signupInsertCollective(t, store, "Baile Alfa", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
	signupInsertCollective(t, store, "Baile Beta", domain.CollectiveKindColetivo, domain.CollectiveStatusPending)
	signupInsertCollective(t, store, "Noite Alfa", domain.CollectiveKindProdutora, domain.CollectiveStatusApproved)
	signupInsertCollective(t, store, "<b>Bold</b> Crew", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
	for i := 1; i <= 9; i++ {
		signupInsertCollective(t, store, fmt.Sprintf("Festa %d", i), domain.CollectiveKindColetivo, domain.CollectiveStatusApproved)
	}
	get := func(q, tipo string) *httptest.ResponseRecorder {
		v := url.Values{"q": {q}}
		if tipo != "" {
			v.Set("tipo", tipo)
		}
		return signupSuggest(r, v, "198.51.100.1")
	}
	items := func(body string) int { return strings.Count(body, "data-collective-id=") }

	t.Run("short queries give the empty partial", func(t *testing.T) {
		for _, q := range []string{"", "b", "  b ", " "} {
			rec := get(q, "")
			if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "" {
				t.Errorf("q=%q: status %d, body %q, want 200 and nothing", q, rec.Code, rec.Body)
			}
		}
		// Two runes of one letter each is long enough.
		if rec := get("ba", ""); items(rec.Body.String()) == 0 {
			t.Errorf("q=ba found nothing: %s", rec.Body)
		}
		// A query longer than any name matches nothing, without asking the store.
		if rec := get(strings.Repeat("a", domain.CollectiveNameMax+1), ""); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "" {
			t.Errorf("long q: status %d, body %q, want 200 and nothing", rec.Code, rec.Body)
		}
	})

	t.Run("only approved collectives", func(t *testing.T) {
		rec := get("baile", "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, body %q", rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); ct != htmlContentType {
			t.Errorf("Content-Type = %q, want %q", ct, htmlContentType)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "Baile Alfa") || strings.Contains(body, "Baile Beta") {
			t.Errorf("body = %s, want the approved collective only", body)
		}
		for _, want := range []string{
			`<ul id="collective-suggestions" class="suggestions" role="listbox">`,
			`<li class="suggestions__item" role="option">`,
			`<button type="button" data-collective-id="` + alpha.ID + `" data-collective-name="Baile Alfa">Baile Alfa</button>`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q in:\n%s", want, body)
			}
		}
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
		}
		if got := items(body); got != 1 {
			t.Errorf("%d suggestions, want 1", got)
		}
	})

	t.Run("nothing found gives nothing", func(t *testing.T) {
		rec := get("zzzz", "")
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "" {
			t.Errorf("status %d, body %q, want 200 and nothing", rec.Code, rec.Body)
		}
	})

	t.Run("tipo filters", func(t *testing.T) {
		both := get("alfa", "").Body.String()
		if !strings.Contains(both, "Baile Alfa") || !strings.Contains(both, "Noite Alfa") {
			t.Fatalf("without tipo: %s, want both", both)
		}
		if body := get("alfa", "produtora").Body.String(); !strings.Contains(body, "Noite Alfa") || strings.Contains(body, "Baile Alfa") {
			t.Errorf("tipo=produtora: %s", body)
		}
		if body := get("alfa", "coletivo").Body.String(); !strings.Contains(body, "Baile Alfa") || strings.Contains(body, "Noite Alfa") {
			t.Errorf("tipo=coletivo: %s", body)
		}
	})

	t.Run("an unknown tipo is 400", func(t *testing.T) {
		for _, tipo := range []string{"outro", "Coletivo", "coletivo,produtora"} {
			if rec := get("baile", tipo); rec.Code != http.StatusBadRequest {
				t.Errorf("tipo=%q: status = %d, want 400", tipo, rec.Code)
			}
		}
		// The kind is checked before the length, so a short query does not hide it.
		if rec := get("b", "outro"); rec.Code != http.StatusBadRequest {
			t.Errorf("short q with a bad tipo: status = %d, want 400", rec.Code)
		}
	})

	t.Run("at most eight", func(t *testing.T) {
		if got := items(get("festa", "").Body.String()); got != 8 {
			t.Errorf("%d suggestions, want 8", got)
		}
	})

	t.Run("names are escaped", func(t *testing.T) {
		body := get("bold", "").Body.String()
		if strings.Contains(body, "<b>") || strings.Contains(body, "</b>") {
			t.Errorf("a name became markup: %s", body)
		}
		for _, want := range []string{`data-collective-name="&lt;b&gt;Bold&lt;/b&gt; Crew"`, `&lt;b&gt;Bold&lt;/b&gt; Crew</button>`} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q in:\n%s", want, body)
			}
		}
	})

	t.Run("a query that is not text is 400", func(t *testing.T) {
		rec := serve(r, http.MethodGet, "/coletivos/sugestoes?q=%ff%fe", header("CF-Connecting-IP", "198.51.100.1"))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", rec.Code)
		}
	})

	t.Run("the query is not logged", func(t *testing.T) {
		if rec := get("segredo-da-busca", ""); rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}
		if strings.Contains(logs.String(), "segredo-da-busca") {
			t.Errorf("the log holds the query: %s", logs)
		}
	})
}

func TestSignupSuggestionsRateLimit(t *testing.T) {
	// A one-rune query never reaches the store, so this needs no database.
	r := signupApp(t, stubStore{}).Router()
	q := url.Values{"q": {"a"}}
	for i := 1; i <= 60; i++ {
		if rec := signupSuggest(r, q, "198.51.100.1"); rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
		}
	}
	rec := signupSuggest(r, q, "198.51.100.1")
	if rec.Code != http.StatusTooManyRequests || rec.Body.Len() != 0 {
		t.Errorf("61st request: status %d, body %q, want 429 and an empty body", rec.Code, rec.Body)
	}
	if rec := signupSuggest(r, q, "198.51.100.2"); rec.Code != http.StatusOK {
		t.Errorf("other IP: status = %d, want 200", rec.Code)
	}
}
