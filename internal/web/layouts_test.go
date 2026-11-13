package web

import (
	"bytes"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	webfs "github.com/magalz/agenda-clubber-lite/web"
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
	rec := renderPage(t, app.Render, "app/home", PageData{Title: "Calendário", Nav: nav, User: user})
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
	rec := renderPage(t, app.Render, "app/home", PageData{Title: "Calendário", Nav: "calendario"})
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
		`<main id="conteudo" class="shell__main">`,
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

	// The placeholder the calendar will replace.
	wantAll(t, "home", body, `class="section-heading"`, `~/ `, "calendário", `class="empty"`, "o calendário chega na próxima fase")

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

	if body := renderHome(t, app, "", user); strings.Contains(body, "aria-current") {
		t.Error("a page without a Nav key marks an item as current")
	}
}

// The Alpine CSP build has no expression evaluator: every Alpine attribute in a
// template must name a property or method that app.js registers.
func TestAlpineAttributesNameRegisteredMembers(t *testing.T) {
	js, err := fs.ReadFile(webfs.Static, "static/js/app.js")
	if err != nil {
		t.Fatal(err)
	}
	app := newTestApp(t)
	body := renderHome(t, app, "calendario", &ViewUser{ID: "u1", Name: "Ana", IsAdmin: true, CanCreateEvents: true})

	attr := regexp.MustCompile(`\s(x-[a-z-]+|@[a-z.]+|:[a-z-]+)="([^"]*)"`)
	ident := regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
	matches := attr.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		t.Fatal("no Alpine attribute found; the check would pass vacuously")
	}
	for _, m := range matches {
		name, value := m[1], m[2]
		if !ident.MatchString(value) {
			t.Errorf(`%s="%s" is an expression; the CSP build only takes a property or method name`, name, value)
			continue
		}
		switch {
		case name == "x-data":
			if !strings.Contains(string(js), "Alpine.data('"+value+"'") {
				t.Errorf("x-data=%q is not registered with Alpine.data in app.js", value)
			}
		case name == "x-ref":
		default:
			// A member is a property ("open:"), a getter ("get expanded()") or a method ("toggle()").
			member := regexp.MustCompile(`(?m)^\s*(get\s+)?` + regexp.QuoteMeta(value) + `\s*[(:]`)
			if !member.Match(js) {
				t.Errorf(`%s="%s" is not a member of the component in app.js`, name, value)
			}
		}
	}
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
