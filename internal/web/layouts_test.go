package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/config"
)

// navItems are the sidebar entries in order: Nav key, label and target.
var navItems = []struct{ key, label, href string }{
	{"calendario", "Calendário", "/"},
	{"criar-evento", "Criar Evento", "/eventos/novo"},
	{"meus-eventos", "Meus Eventos", "/meus-eventos"},
	{"coletivos", "Meus Coletivos", "/coletivos"},
	{"perfil", "Editar Perfil", "/perfil"},
	{"admin", "Admin", "/admin"},
}

// renderHome renders the shipped app/home page through the App's renderer.
func renderHome(t *testing.T, app *App, nav string, user *ViewUser) string {
	t.Helper()
	rec := renderPage(t, app.Render, "app/home", PageData{Title: "Calendário", Nav: nav, User: user, Data: calHomeData()})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q", rec.Code, rec.Body)
	}
	return rec.Body.String()
}

// tagAttrs returns the attributes of the first <tag ...> whose opening tag
// matches the pattern, or fails the test.
func tagAttrs(t *testing.T, body, pattern string) string {
	t.Helper()
	m := regexp.MustCompile(pattern).FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("no match for %s in:\n%s", pattern, body)
	}
	return m[1]
}

// linkAttrs returns the attributes of the <a> whose text is label.
func linkAttrs(t *testing.T, body, label string) string {
	t.Helper()
	return tagAttrs(t, body, `<a ([^>]*)>\s*`+regexp.QuoteMeta(label)+`\s*</a>`)
}

func wantAll(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s: missing %q in %q", what, w, got)
		}
	}
}

// The home page needs a session, so the shell of a logged-out visitor is
// checked on the renderer; TestHomeServesTheShellToASignedInUser checks the
// route.
func TestAppShellLoggedOut(t *testing.T) {
	app := newTestApp(t)
	rec := renderPage(t, app.Render, "app/home", PageData{Title: "Calendário", Nav: "calendario", Data: calHomeData()})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != htmlContentType {
		t.Errorf("Content-Type = %q, want %q", got, htmlContentType)
	}
	body := rec.Body.String()

	if !strings.Contains(body, "<title>Calendário · AGENDA CLUBBER</title>") {
		t.Errorf("title is not %q", "Calendário · AGENDA CLUBBER")
	}
	if n := strings.Count(body, `<main id="conteudo"`); n != 1 {
		t.Errorf("%d <main id=\"conteudo\">, want exactly 1", n)
	}

	// The drawer rules in components.css hang on this order, and on the backdrop
	// being the aside's next sibling.
	inOrder(t, body,
		`<div class="shell" x-data="shell"`,
		`<header class="topbar">`,
		`<aside class="sidebar"`,
		`</aside>`,
		`<div class="sidebar-backdrop"`,
		`<main id="conteudo" class="shell__main" :inert="mainInert">`,
		"feito_por --dev=IgnisDev", // the footer sits inside main
		`</main>`,
	)
	if !regexp.MustCompile(`</aside>\s*<div class="sidebar-backdrop"[^>]*></div>\s*<main `).MatchString(body) {
		t.Error("the backdrop does not directly follow the aside, and the main directly follow the backdrop")
	}
	wantAll(t, "shell", tagAttrs(t, body, `<div class="shell" ([^>]*)>`), `@keydown.escape.window="close"`)
	wantAll(t, "aside", tagAttrs(t, body, `<aside class="sidebar" ([^>]*)>`),
		`id="menu-lateral"`, `:class="sidebarClass"`, `aria-label="menu principal"`)
	wantAll(t, "backdrop", tagAttrs(t, body, `<div class="sidebar-backdrop" ([^>]*)>`), `@click="close"`)

	// The topbar carries the wordmark and the drawer toggle.
	topbar := tagAttrs(t, body, `(?s)<header class="topbar">(.*?)</header>`)
	wantAll(t, "topbar", topbar, `<a class="wordmark" href="/">`, `[≡] menu`)
	wantAll(t, "toggle", tagAttrs(t, topbar, `<button ([^>]*)>`),
		`type="button"`, `class="btn btn--ghost btn--sm"`, `x-ref="toggle"`, `aria-controls="menu-lateral"`,
		`:aria-expanded="expanded"`, `@click="toggle"`)
	wantAll(t, "toggle label", tagAttrs(t, topbar, `(?s)<button [^>]*x-text="toggleLabel"[^>]*>(.*?)</button>`), "[≡] menu")

	// Logged out: Calendário is the current page, the others are not.
	if n := strings.Count(body, `aria-current="page"`); n != 1 {
		t.Errorf("%d aria-current=\"page\", want 1", n)
	}
	wantAll(t, "Calendário", linkAttrs(t, body, "Calendário"), `class="nav-item"`, `href="/"`, `aria-current="page"`)

	// Criar Evento is disabled, with no href, and points at its hint.
	criar := linkAttrs(t, body, "Criar Evento")
	wantAll(t, "Criar Evento", criar, `class="nav-item"`, `aria-disabled="true"`, `aria-describedby="criar-evento-dica"`)
	if strings.Contains(criar, "href") {
		t.Errorf("disabled Criar Evento has an href: %q", criar)
	}
	wantAll(t, "hint", body, `class="hint" id="criar-evento-dica"`, "só administradores de coletivos aprovados criam eventos")

	// No session: no admin section, no logout.
	for _, bad := range []string{"/admin", `action="/sair"`, "nav-item--logout"} {
		if strings.Contains(body, bad) {
			t.Errorf("logged-out page contains %q", bad)
		}
	}

	// The calendar fills the page's content.
	wantAll(t, "home", body, `class="section-heading"`, `~/ `, "calendário", `<table class="calendar__grid"`, `id="calendar-day"`)
	if strings.Contains(body, "o calendário chega na próxima fase") {
		t.Error("the page still shows the calendar placeholder")
	}

	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}
}

func TestAppShellByUser(t *testing.T) {
	const name = "Ana & <Co>"
	tests := []struct {
		name string
		user *ViewUser
	}{
		{"logged out", nil},
		{"member who cannot create events", &ViewUser{ID: "u1", Name: name}},
		{"owner", &ViewUser{ID: "u2", Name: name, CanCreateEvents: true}},
		{"admin who cannot create events", &ViewUser{ID: "u3", Name: name, IsAdmin: true}},
		{"admin and owner", &ViewUser{ID: "u4", Name: name, IsAdmin: true, CanCreateEvents: true}},
	}
	app := newTestApp(t)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := renderHome(t, app, "calendario", tt.user)
			signedIn := tt.user != nil
			admin := signedIn && tt.user.IsAdmin
			canCreate := signedIn && tt.user.CanCreateEvents

			// Section labels and items, in order.
			if admin {
				inOrder(t, body, "/agenda", "Calendário", "Criar Evento", "Meus Eventos",
					"/conta", "Meus Coletivos", "Editar Perfil", "/admin", ">Admin</a>")
				wantAll(t, "Admin", linkAttrs(t, body, "Admin"), `class="nav-item"`, `href="/admin"`)
			} else {
				inOrder(t, body, "/agenda", "Calendário", "Criar Evento", "Meus Eventos",
					"/conta", "Meus Coletivos", "Editar Perfil")
				if strings.Contains(body, "/admin") || strings.Contains(body, ">Admin<") {
					t.Error("the admin section is shown to a non-admin")
				}
			}
			for _, item := range navItems[2:5] {
				wantAll(t, item.label, linkAttrs(t, body, item.label), `class="nav-item"`, `href="`+item.href+`"`)
			}

			criar := linkAttrs(t, body, "Criar Evento")
			if canCreate {
				wantAll(t, "Criar Evento", criar, `class="nav-item"`, `href="/eventos/novo"`)
				for _, bad := range []string{"aria-disabled", "criar-evento-dica"} {
					if strings.Contains(criar, bad) || strings.Contains(body, bad) {
						t.Errorf("enabled Criar Evento still has %q", bad)
					}
				}
			} else {
				wantAll(t, "Criar Evento", criar, `aria-disabled="true"`, `aria-describedby="criar-evento-dica"`)
				if strings.Contains(criar, "href") {
					t.Errorf("disabled Criar Evento has an href: %q", criar)
				}
				wantAll(t, "hint", body, `id="criar-evento-dica"`, "só administradores de coletivos aprovados criam eventos")
			}

			if signedIn {
				footer := tagAttrs(t, body, `(?s)<div class="sidebar__footer">(.*?)</div>`)
				wantAll(t, "sidebar footer", footer, "Ana &amp; &lt;Co&gt;",
					`<form method="post" action="/sair">`,
					`<button type="submit" class="nav-item nav-item--logout">[→] sair</button>`)
				if strings.Contains(body, "<Co>") {
					t.Error("the user name is not escaped")
				}
			} else {
				for _, bad := range []string{`action="/sair"`, "sidebar__footer"} {
					if strings.Contains(body, bad) {
						t.Errorf("logged-out page contains %q", bad)
					}
				}
			}

			if v := cspViolations(body); len(v) > 0 {
				t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
			}
		})
	}
}

func TestAppShellMarksTheActiveItem(t *testing.T) {
	app := newTestApp(t)
	user := &ViewUser{ID: "u1", Name: "Ana", IsAdmin: true, CanCreateEvents: true}

	for _, active := range navItems {
		t.Run(active.key, func(t *testing.T) {
			body := renderHome(t, app, active.key, user)
			if n := strings.Count(body, `aria-current="page"`); n != 1 {
				t.Fatalf("%d aria-current=\"page\", want 1", n)
			}
			for _, item := range navItems {
				has := strings.Contains(linkAttrs(t, body, item.label), `aria-current="page"`)
				if has != (item.key == active.key) {
					t.Errorf("%s: aria-current = %v, want %v", item.label, has, item.key == active.key)
				}
			}
		})
	}

	// The calendar marks today with aria-current="date", so only the page marker counts here.
	if body := renderHome(t, app, "", user); strings.Contains(body, `aria-current="page"`) {
		t.Error("a page without a Nav key marks an item as current")
	}
}

// The Alpine CSP build has no expression evaluator: every Alpine attribute in a
// template must name a property or method of a component whose x-data scope
// encloses it. Alpine merges nested scopes, so the shell's members are available
// inside every component on the page. Checked on each page with Alpine markup
// that has a fixture here: the calendar, the event form's create and edit, and
// the profile page.
func TestAlpineAttributesNameRegisteredMembers(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	scopes := dynScopes(js)
	if len(scopes) == 0 {
		t.Fatal("dynScopes found no Alpine.data components in app.js")
	}
	attr := regexp.MustCompile(`\s(x-[a-z-]+|@[a-z.]+|:[a-z-]+)="([^"]*)"`)
	ident := regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

	app := newTestApp(t)
	forms := ewRender(t, newEvfEnv(t))
	pages := []struct {
		name    string
		body    string
		widgets bool // the event form must carry all four widget components
	}{
		{"home", renderHome(t, app, "calendario", &ViewUser{ID: "u1", Name: "Ana", IsAdmin: true, CanCreateEvents: true}), false},
		{"create form", forms.create, true},
		{"edit form", forms.edit, true},
		{"perfil", newProfileEnv(t).show().Body.String(), false},
	}

	for _, p := range pages {
		t.Run(p.name, func(t *testing.T) {
			if p.widgets {
				for _, c := range ewComponents {
					if !strings.Contains(p.body, `x-data="`+c+`"`) {
						t.Errorf("the form has no x-data=%q", c)
					}
				}
			}

			// Each x-data element opens a scope that runs to the end of its tag.
			type scope struct {
				start, end int
				name       string
			}
			var xdata []scope
			for _, m := range attr.FindAllStringSubmatchIndex(p.body, -1) {
				if p.body[m[2]:m[3]] != "x-data" {
					continue
				}
				name := p.body[m[4]:m[5]]
				if !ident.MatchString(name) {
					t.Errorf(`x-data="%s" is not a component name`, name)
					continue
				}
				if !strings.Contains(js, "Alpine.data('"+name+"'") {
					t.Errorf("x-data=%q is not registered with Alpine.data in app.js", name)
					continue
				}
				start := strings.LastIndex(p.body[:m[0]+1], "<")
				end, void := voidTagEnd(p.body, start)
				if !void {
					end = dynClose(t, p.body, start)
				}
				xdata = append(xdata, scope{start: start, end: end, name: name})
			}
			if len(xdata) == 0 {
				t.Fatal("no x-data element found; the check would pass vacuously")
			}

			found := 0
			for _, m := range attr.FindAllStringSubmatchIndex(p.body, -1) {
				name, value := p.body[m[2]:m[3]], p.body[m[4]:m[5]]
				found++
				if !ident.MatchString(value) {
					t.Errorf(`%s="%s" is an expression; the CSP build only takes a property or method name`, name, value)
					continue
				}
				if name == "x-data" || name == "x-ref" {
					continue
				}
				var enclosing []string
				members := map[string]bool{}
				for _, s := range xdata {
					if s.start <= m[0] && m[0] < s.end {
						enclosing = append(enclosing, s.name)
						for _, member := range scopes[s.name] {
							members[member] = true
						}
					}
				}
				if len(enclosing) == 0 {
					t.Errorf(`%s="%s" is outside every x-data scope`, name, value)
					continue
				}
				if !members[value] {
					t.Errorf(`%s="%s" is not a member of any enclosing component %v`, name, value, enclosing)
				}
			}
			if found < 5 {
				t.Errorf("only %d Alpine attributes found; the check would be weak", found)
			}
		})
	}
}

// voidTagEnd reports where the element that starts at start ends when it is a
// void or self-closing tag (x-data on an <input>, say): such an element has no
// closing tag, so its scope ends at its own '>'. ok is false for a regular
// element, which dynClose must close instead. This also lets pages whose only
// Alpine markup is a void element (the profile's phone field, the signup form)
// carry the member check the ACL-97 review noted they lacked.
func voidTagEnd(body string, start int) (end int, ok bool) {
	gt := strings.IndexByte(body[start:], '>')
	if gt < 0 {
		return 0, false
	}
	tag := body[start+1 : start+gt]
	if strings.HasSuffix(tag, "/") {
		return start + gt + 1, true
	}
	name := tag
	if i := strings.IndexAny(name, " \t\n"); i >= 0 {
		name = name[:i]
	}
	switch strings.ToLower(name) {
	case "area", "base", "br", "col", "embed", "hr", "img", "input", "link", "meta", "param", "source", "track", "wbr":
		return start + gt + 1, true
	}
	return 0, false
}

// shellSource is the shell component of app.js, from Alpine.data('shell' to its closing. The checks below read only
// it: a string the module's other components also hold would pass them whatever the shell does.
func shellSource(t *testing.T, js string) string {
	t.Helper()
	shell := regexp.MustCompile(`(?s)Alpine\.data\('shell'.*?\n    \}\);\n`).FindString(js)
	if shell == "" {
		t.Fatalf("app.js has no shell component (Alpine.data('shell', ...) closed at 4 spaces)")
	}
	return shell
}

// shellMember is the member of shell called name (indented by 8 spaces), from its signature to its closing brace.
func shellMember(t *testing.T, js, name string) string {
	t.Helper()
	member := regexp.MustCompile(`(?s)\n        (?:async )?` + regexp.QuoteMeta(name) + `\(.*?\n        \},\n`).FindString(shellSource(t, js))
	if member == "" {
		t.Fatalf("shell has no %s member", name)
	}
	return member
}

// wantBefore fails unless first comes before second in got.
func wantBefore(t *testing.T, what, got, first, second string) {
	t.Helper()
	a, b := strings.Index(got, first), strings.Index(got, second)
	if a < 0 || b < 0 || a > b {
		t.Errorf("%s: %q must come before %q (at %d and %d): %s", what, first, second, a, b, got)
	}
}

// While the drawer covers the page, main is inert so its controls are not tabbable or reachable by assistive tech.
// The binding is a bare property name: the CSP build evaluates no expression in a template.
func TestAppShellBindsMainInertOnMain(t *testing.T) {
	app := newTestApp(t)
	body := renderHome(t, app, "calendario", nil)
	wantAll(t, "main", tagAttrs(t, body, `<main id="conteudo" ([^>]*)>`), `class="shell__main"`, `:inert="mainInert"`)

	// The existing keyboard and aria contract stays untouched: the template still names the shell's own members.
	wantAll(t, "shell", tagAttrs(t, body, `<div class="shell" ([^>]*)>`), `@keydown.escape.window="close"`)
	wantAll(t, "topbar", tagAttrs(t, body, `(?s)<header class="topbar">(.*?)</header>`),
		`aria-controls="menu-lateral"`, `:aria-expanded="expanded"`, `@click="toggle"`)
}

// mainInert is derived, not stored: the shell tracks the breakpoint flag its matchMedia handler already uses, so
// crossing to the wide sidebar closes the drawer and lifts the inert in the same reactive update.
func TestAppShellDerivesMainInertFromOpenAndBreakpoint(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	scopes := dynScopes(js)
	for _, member := range []string{"mainInert", "wide", "onDrawerKeydown"} {
		if !slices.Contains(scopes["shell"], member) {
			t.Errorf("shell has no %s member (%v)", member, scopes["shell"])
		}
	}

	shell := shellSource(t, js)
	wantAll(t, "shell", shell, "wide: false,", "get mainInert() {", "return this.open && !this.wide;")
	wantAll(t, "init", shellMember(t, js, "init"),
		"this.wide = query.matches", "this.wide = event.matches", "if (event.matches) this.open = false;")
}

// Go cannot run the script, so pin the contract in its source: a window keydown listener owned by the shell, active
// only while the drawer is open in drawer mode, wrapping Tab and Shift-Tab at the drawer's first and last tab stops.
func TestAppShellTrapsTabInsideTheDrawer(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	wantAll(t, "app.js", js, "const FOCUSABLE =")
	wantAll(t, "init", shellMember(t, js, "init"),
		"onKeydown = (event) => this.onDrawerKeydown(event);", "window.addEventListener('keydown', onKeydown)")
	wantAll(t, "destroy", shellMember(t, js, "destroy"), "window.removeEventListener('keydown', onKeydown)")

	handler := shellMember(t, js, "onDrawerKeydown")
	wantAll(t, "onDrawerKeydown", handler,
		"event.key !== 'Tab'", "this.mainInert", "querySelector('#menu-lateral')", "querySelectorAll(FOCUSABLE)",
		"event.shiftKey && active === first", "!event.shiftKey && active === last",
		"event.preventDefault();", "last.focus();", "first.focus();")
	wantBefore(t, "onDrawerKeydown", handler, "event.shiftKey && active === first", "last.focus();")
	wantBefore(t, "onDrawerKeydown", handler, "!event.shiftKey && active === last", "first.focus();")
}

func TestAuthLayout(t *testing.T) {
	r, logs := newTestRenderer(t, shippedTemplates(t, []string{"auth"}, map[string]string{
		"pages/auth/x.html": `{{define "content"}}<p>formulário de teste</p>{{end}}`,
	}))
	rec := renderPage(t, r, "auth/x", PageData{Title: "Entrar"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q, log %s", rec.Code, rec.Body, logs)
	}
	body := rec.Body.String()

	if !strings.Contains(body, "<title>Entrar · AGENDA CLUBBER</title>") {
		t.Errorf("title is not %q", "Entrar · AGENDA CLUBBER")
	}
	if n := strings.Count(body, `<main id="conteudo"`); n != 1 {
		t.Errorf("%d <main id=\"conteudo\">, want exactly 1", n)
	}
	inOrder(t, body,
		`<main id="conteudo" class="auth crt-flicker">`, // the one-shot CRT flicker on a fresh screen
		`<a class="wordmark" href="/">`,
		`<div class="panel">`,
		`<span class="panel__dots" aria-hidden="true"><span></span><span></span><span></span></span>`,
		`<h1 class="panel__title">Entrar</h1>`,
		`<div class="panel__body">`,
		"<p>formulário de teste</p>",
		`</main>`,
		"feito_por --dev=IgnisDev", // the footer follows main
	)
	for _, bad := range []string{"sidebar", "topbar", "x-data"} {
		if strings.Contains(body, bad) {
			t.Errorf("the auth layout contains %q", bad)
		}
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}
}

func TestWordmarkContract(t *testing.T) {
	app := newTestApp(t)
	c, rec := newCtx()
	app.Render.Partial(c, http.StatusOK, "wordmark", nil)
	want := `<a class="wordmark" href="/"><span class="wordmark__mark" aria-hidden="true">◢◤</span>` +
		`<span>AGENDA<span class="wordmark__sep">_</span>CLUBBER</span>` +
		`<span class="cursor-blink" aria-hidden="true"></span></a>`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("wordmark = %q, want %q", got, want)
	}
}

// config.Load always sets Location, so a Config without one is a programming
// error that must not fall back to a silent default.
func TestNewAppRefusesANilLocation(t *testing.T) {
	mustPanic(t, "Location", func() {
		NewApp(config.Config{Env: "prod"}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), stubStore{})
	})
}

// A nil store would panic on the first request that loads a session, so it is
// refused at startup instead.
func TestNewAppRefusesANilStore(t *testing.T) {
	mustPanic(t, "Store", func() {
		NewApp(testConfig(t), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)), nil)
	})
}

// A native checkbox or radio is its own click target: the label around it is larger, but a 20px box still leaves a
// 20px target under WCAG 2.5.8. Go cannot measure a browser, so pin the source value: 1.5rem is 24px at this app's
// 16px root (neither base.css nor tokens.css overrides the root font size).
func TestCheckAndRadioInputsMeetThe24pxTarget(t *testing.T) {
	css := dynRead(t, "static/css/components.css")
	rule := regexp.MustCompile(`(?s)\.check input,\s*\.radio-card input\s*\{([^}]*)\}`).FindStringSubmatch(css)
	if rule == nil {
		t.Fatal("components.css has no .check input, .radio-card input rule")
	}
	for _, prop := range []string{"width", "height"} {
		t.Run(prop, func(t *testing.T) {
			value := regexp.MustCompile(`(?m)^\s*` + prop + `:\s*([^;]+);`).FindStringSubmatch(rule[1])
			if value == nil {
				t.Fatalf(".check input has no %s declaration", prop)
			}
			got := strings.TrimSpace(value[1])
			length := regexp.MustCompile(`^(\d+(?:\.\d+)?)(rem|px)$`).FindStringSubmatch(got)
			if length == nil {
				t.Fatalf(".check input %s = %q is not a plain px/rem length", prop, got)
			}
			px, err := strconv.ParseFloat(length[1], 64)
			if err != nil {
				t.Fatal(err)
			}
			if length[2] == "rem" {
				px *= 16
			}
			if px < 24 {
				t.Errorf(".check input %s = %q (%.0fpx), want 1.5rem (24px at the 16px root)", prop, got, px)
			}
			if got != "1.5rem" {
				t.Errorf(".check input %s = %q, want the pinned 1.5rem", prop, got)
			}
		})
	}
}
