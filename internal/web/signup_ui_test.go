package web

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

// The signup page's script hooks: the phone mask, the live password checklist
// and the collective autocomplete. The scripts themselves run in a browser;
// these tests pin the markup they rely on, the CSP-build rules the markup must
// follow, and that the page still posts without JavaScript.

// signupUIBody is the signup page as an anonymous visitor gets it.
func signupUIBody(t *testing.T) string {
	t.Helper()
	rec := serve(signupApp(t, stubStore{}).Router(), http.MethodGet, "/cadastro")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /cadastro: status = %d, body %q", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// signupUIAppJS is the shipped web/static/js/app.js.
func signupUIAppJS(t *testing.T) string {
	t.Helper()
	data, err := fs.ReadFile(webfs.Static, "static/js/app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	return string(data)
}

// alpineAttr matches an Alpine directive or binding attribute and its value.
var alpineAttr = regexp.MustCompile(`\s(x-[a-z][a-z0-9.:-]*|@[a-z][a-z0-9.:-]*|:[a-z][a-z0-9-]*)="([^"]*)"`)

// alpineName is the only expression the Alpine CSP build evaluates here: the
// name of a property or method registered with Alpine.data().
var alpineName = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// renderPartial executes a partial the way a handler does.
func renderPartial(t *testing.T, app *App, name string, data any) string {
	t.Helper()
	defer func() {
		t.Helper()
		if v := recover(); v != nil {
			t.Fatalf("render %s: %v", name, v)
		}
	}()
	c, rec := newCtx()
	app.Render.Partial(c, http.StatusOK, name, data)
	if rec.Code != http.StatusOK {
		t.Fatalf("render %s: status = %d, body %q", name, rec.Code, rec.Body)
	}
	return rec.Body.String()
}

func TestSignupUIMarkup(t *testing.T) {
	body := signupUIBody(t)

	t.Run("no CSP violations", func(t *testing.T) {
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
		}
	})

	t.Run("Alpine expressions are plain names of registered components", func(t *testing.T) {
		js := signupUIAppJS(t)
		attrs := alpineAttr.FindAllStringSubmatch(body, -1)
		if len(attrs) == 0 {
			t.Fatal("the page has no Alpine attribute")
		}
		for _, a := range attrs {
			if !alpineName.MatchString(a[2]) {
				t.Errorf("%s=%q is not a plain name: the Alpine CSP build cannot evaluate it", a[1], a[2])
			}
			if a[1] == "x-data" && !strings.Contains(js, "Alpine.data('"+a[2]+"'") {
				t.Errorf("x-data=%q is not registered with Alpine.data() in app.js", a[2])
			}
		}
	})

	t.Run("the three components", func(t *testing.T) {
		for _, name := range []string{"phoneMask", "passwordFields", "collectivePicker"} {
			if got := strings.Count(body, `x-data="`+name+`"`); got != 1 {
				t.Errorf(`%d x-data="%s", want 1`, got, name)
			}
		}
		if tag := signupInput(t, body, "phone", ""); !strings.Contains(tag, `x-data="phoneMask"`) {
			t.Errorf("phone input %s is not the phoneMask component", tag)
		}
		// The picker spans the kind radios, the name, the hidden id, the suggestions and the role radios.
		inOrder(t, body,
			`x-data="collectivePicker"`, `name="collective_kind"`, `name="collective_name"`, `name="collective_id"`,
			`id="collective-suggestions-box"`, `name="role"`, "[→] CRIAR CONTA",
		)
		// The password component spans both inputs, the live message and the checklist.
		inOrder(t, body, `x-data="passwordFields"`, `name="password"`, `name="password_confirm"`, `aria-live="polite"`, `id="password-checklist"`)
	})

	t.Run("collective name is a combobox", func(t *testing.T) {
		tag := signupInput(t, body, "collective_name", "")
		for _, want := range []string{
			` id="collective_name"`, `role="combobox"`, `aria-autocomplete="list"`, `aria-expanded="false"`,
			`aria-controls="collective-suggestions"`, `autocomplete="off"`, ` required`,
		} {
			if !strings.Contains(tag, want) {
				t.Errorf("collective_name input %s lacks %q", tag, want)
			}
		}
		if !strings.Contains(body, `<div id="collective-suggestions-box"></div>`) {
			t.Error("the suggestions box is not an empty div")
		}
		// The input keeps its hint and error references.
		if !strings.Contains(tag, `aria-describedby="collective-name-hint`) {
			t.Errorf("collective_name input %s lost its hint", tag)
		}
	})

	t.Run("password component", func(t *testing.T) {
		if !strings.Contains(body, `data-email-input="email"`) {
			t.Error("the password component does not name the email input")
		}
		if strings.Contains(body, "data-email=") {
			t.Error("the signup page passes a fixed email")
		}
		live := regexp.MustCompile(`<p\b[^>]*\saria-live="polite"[^>]*></p>`).FindAllString(body, -1)
		if len(live) != 1 {
			t.Fatalf("%d empty polite live regions, want 1 (the confirmation message):\n%s", len(live), body)
		}
		if strings.Contains(live[0], " hidden") {
			t.Errorf("the live region %s starts hidden, so a screen reader may not announce it", live[0])
		}
		if strings.Contains(body, "as senhas conferem") || strings.Contains(body, "as senhas não conferem") {
			t.Error("the confirmation message has text before anything is typed")
		}
	})

	t.Run("the role radios start enabled", func(t *testing.T) {
		for _, value := range []string{"owner", "member"} {
			tag := signupInput(t, body, "role", value)
			if regexp.MustCompile(`\sdisabled\b`).MatchString(tag) {
				t.Errorf("role radio %s is disabled in the served HTML", tag)
			}
		}
		if !strings.Contains(body, `id="role-hint"`) {
			t.Error("missing the role hint")
		}
	})

	t.Run("posts without JavaScript", func(t *testing.T) {
		form := regexp.MustCompile(`<form\b[^>]*>`).FindString(body)
		if !strings.Contains(form, `method="post"`) || !strings.Contains(form, `action="/cadastro"`) {
			t.Errorf("form tag = %q, want a post to /cadastro", form)
		}
		for _, name := range []string{
			"name", "email", "phone", "phone_whatsapp", "password", "password_confirm",
			"collective_kind", "collective_name", "collective_id", "role",
		} {
			if len(signupInputs(body, name, "")) == 0 {
				t.Errorf("no input named %q", name)
			}
		}
		if tag := signupInput(t, body, "collective_id", ""); !strings.Contains(tag, `type="hidden"`) || strings.Contains(tag, `x-`) {
			t.Errorf("collective_id = %s, want a plain hidden input", tag)
		}
		for _, name := range []string{"collective_kind", "role"} {
			for _, tag := range signupInputs(body, name, "") {
				if !strings.Contains(tag, " required") {
					t.Errorf("radio %s is not required", tag)
				}
			}
		}
		if !strings.Contains(body, `type="submit"`) {
			t.Error("no submit button")
		}
	})
}

func TestSignupUIScript(t *testing.T) {
	js := signupUIAppJS(t)

	// The password rules are Unicode-aware, like auth.CheckPolicy: the script
	// must use the same character classes, not [a-z] or \d.
	for _, want := range []string{
		"Alpine.data('phoneMask'", "Alpine.data('passwordFields'", "Alpine.data('collectivePicker'",
		`\p{Ll}`, `\p{Lu}`, `\p{Nd}`, `\p{L}`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
	// A page script may not evaluate strings (the CSP forbids it) or write HTML from user data.
	for _, banned := range []string{"eval(", "new Function", "document.write", "outerHTML"} {
		if strings.Contains(js, banned) {
			t.Errorf("app.js contains %q", banned)
		}
	}
}

func TestSignupSuggestionsOptionIDs(t *testing.T) {
	app := newTestApp(t)
	collectives := []domain.Collective{
		{ID: "id-alfa", Name: "Baile Alfa"},
		{ID: "id-beta", Name: "<b>Beta</b> & Cia"},
		{ID: "id-gama", Name: "Gama"},
	}
	body := renderPartial(t, app, "signup-suggestions", collectives)

	if !strings.Contains(body, `<ul id="collective-suggestions" class="suggestions" role="listbox">`) {
		t.Errorf("missing the listbox in:\n%s", body)
	}
	for i, c := range collectives {
		id := "collective-suggestion-" + string(rune('0'+i))
		item := regexp.MustCompile(`<li\b[^>]*\bid="` + id + `"[^>]*>`).FindString(body)
		if item == "" {
			t.Errorf("no <li id=%q> in:\n%s", id, body)
			continue
		}
		for _, want := range []string{`role="option"`, `class="suggestions__item"`} {
			if !strings.Contains(item, want) {
				t.Errorf("%s lacks %q", item, want)
			}
		}
		if !strings.Contains(body, `data-collective-id="`+c.ID+`"`) {
			t.Errorf("no data-collective-id=%q in:\n%s", c.ID, body)
		}
	}
	if got := strings.Count(body, `role="option"`); got != len(collectives) {
		t.Errorf("%d options, want %d", got, len(collectives))
	}
	if got := strings.Count(body, "data-collective-id="); got != len(collectives) {
		t.Errorf("%d data-collective-id, want %d", got, len(collectives))
	}
	// The button is not a tab stop: focus stays in the input, which points at the option with aria-activedescendant.
	for _, btn := range regexp.MustCompile(`<button\b[^>]*>`).FindAllString(body, -1) {
		if !strings.Contains(btn, `type="button"`) || !strings.Contains(btn, `tabindex="-1"`) {
			t.Errorf("%s is not a type=button, tabindex=-1 button", btn)
		}
	}
	if !strings.Contains(body, `data-collective-name="&lt;b&gt;Beta&lt;/b&gt; &amp; Cia"`) || strings.Contains(body, "<b>") {
		t.Errorf("a name was not escaped in:\n%s", body)
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}

	for name, empty := range map[string][]domain.Collective{"nil": nil, "empty": {}} {
		if got := strings.TrimSpace(renderPartial(t, app, "signup-suggestions", empty)); got != "" {
			t.Errorf("%s list renders %q, want nothing", name, got)
		}
	}
}

func TestPasswordFieldsOptionalKeys(t *testing.T) {
	app := newTestApp(t)
	call := func(extra map[string]any) string {
		data := map[string]any{
			"Errors":       domain.FieldErrors(nil),
			"Checklist":    passwordChecklist(true),
			"Autocomplete": "new-password",
		}
		for k, v := range extra {
			data[k] = v
		}
		return renderPartial(t, app, "password-fields", data)
	}

	t.Run("only the original keys", func(t *testing.T) {
		body := call(nil)
		if strings.Contains(body, "no value") || strings.Contains(body, "data-email") {
			t.Errorf("an absent optional key leaks into:\n%s", body)
		}
		for _, want := range []string{
			`x-data="passwordFields"`, `id="password"`, `id="password_confirm"`, `id="password-checklist"`, `aria-live="polite"`,
			`<li class="checklist__item" data-rule="history">`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("missing %q in:\n%s", want, body)
			}
		}
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
		}
	})

	t.Run("EmailInput names an input to read live", func(t *testing.T) {
		body := call(map[string]any{"EmailInput": "email"})
		if !strings.Contains(body, `data-email-input="email"`) || strings.Contains(body, "data-email=") {
			t.Errorf("want data-email-input only in:\n%s", body)
		}
	})

	t.Run("Email is a fixed address, escaped", func(t *testing.T) {
		body := call(map[string]any{"Email": `a"b@example.com`})
		if !strings.Contains(body, `data-email="a&#34;b@example.com"`) || strings.Contains(body, "data-email-input") {
			t.Errorf("want an escaped data-email only in:\n%s", body)
		}
	})
}
