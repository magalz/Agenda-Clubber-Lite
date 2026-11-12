package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

// renderAssets hashes a static tree with every file the base layout links.
func renderAssets(t *testing.T) *Assets {
	t.Helper()
	files := []string{
		"favicon.svg",
		"css/tokens.css", "css/base.css", "css/components.css",
		"js/app.js", "js/vendor/htmx-2.0.11.min.js", "js/vendor/alpine-csp-3.17.5.min.js",
	}
	static := fstest.MapFS{}
	for _, name := range files {
		static[name] = &fstest.MapFile{Data: []byte("content of " + name)}
	}
	assets, err := NewAssets(static)
	if err != nil {
		t.Fatalf("NewAssets: %v", err)
	}
	return assets
}

func fortaleza(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Fortaleza")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return loc
}

// withFiles adds text files to a MapFS.
func withFiles(m fstest.MapFS, files map[string]string) fstest.MapFS {
	for name, data := range files {
		m[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return m
}

// realTemplates is a template tree with the shipped base layout and footer.
func realTemplates(t *testing.T, extra map[string]string) fstest.MapFS {
	t.Helper()
	sub, err := fs.Sub(webfs.Templates, "templates")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	m := fstest.MapFS{}
	for _, name := range []string{"layouts/base.html", "partials/footer.html"} {
		data, err := fs.ReadFile(sub, name)
		if err != nil {
			t.Fatalf("shipped template %s: %v", name, err)
		}
		m[name] = &fstest.MapFile{Data: data}
	}
	return withFiles(m, extra)
}

// miniTemplates is a tiny tree that does not depend on the shipped layout.
func miniTemplates(extra map[string]string) fstest.MapFS {
	return withFiles(fstest.MapFS{}, map[string]string{
		"layouts/base.html":    `{{define "base"}}[{{template "content" .}}]{{end}}`,
		"partials/chip.html":   `{{define "chip"}}<span class="chip">{{.Label}}</span>{{end}}`,
		"partials/wrap.html":   `{{define "wrap"}}<div>{{template "chip" (dict "Label" .Name)}}</div>{{end}}`,
		"partials/broken.html": `{{define "broken"}}[{{.Name}}]{{end}}`,
	})
}

func newTestRenderer(t *testing.T, fsys fs.FS) (*Renderer, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	r, err := NewRenderer(fsys, renderAssets(t), fortaleza(t), slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	return r, logs
}

func newCtx() (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	return c, rec
}

func renderPage(t *testing.T, r *Renderer, page string, data PageData) *httptest.ResponseRecorder {
	t.Helper()
	c, rec := newCtx()
	r.Page(c, http.StatusOK, page, data)
	return rec
}

func panics(f func()) (panicked bool) {
	defer func() { panicked = recover() != nil }()
	f()
	return false
}

// inOrder fails unless every needle appears in body, in the given order.
func inOrder(t *testing.T, body string, needles ...string) {
	t.Helper()
	pos := 0
	for _, n := range needles {
		i := strings.Index(body[pos:], n)
		if i < 0 {
			t.Errorf("%q not found (in order) in:\n%s", n, body)
			return
		}
		pos += i + len(n)
	}
}

const basePage = `{{define "content"}}<h1>{{.Title}}</h1><p>{{.Data}}</p>{{end}}`

func TestPageRendersInsideBaseLayout(t *testing.T) {
	r, _ := newTestRenderer(t, realTemplates(t, map[string]string{"pages/base/x.html": basePage}))
	rec := renderPage(t, r, "base/x", PageData{Title: "Calendário", Data: "olá"})
	body := rec.Body.String()

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	assets := renderAssets(t)
	link := func(name string) string {
		return fmt.Sprintf(`<link rel="stylesheet" href="%s">`, assets.URL(name))
	}
	script := func(name string) string {
		return fmt.Sprintf(`<script src="%s" defer></script>`, assets.URL(name))
	}
	inOrder(t, body,
		`<!doctype html>`,
		`<html lang="pt-BR">`,
		`<meta charset="utf-8">`,
		`<meta name="viewport" content="width=device-width, initial-scale=1">`,
		`<meta name="color-scheme" content="dark">`,
		`<title>Calendário · AGENDA CLUBBER</title>`,
		fmt.Sprintf(`<link rel="icon" href="%s" type="image/svg+xml">`, assets.URL("favicon.svg")),
		link("css/tokens.css"),
		link("css/base.css"),
		link("css/components.css"),
		`<meta name="htmx-config"`,
		script("js/app.js"),
		script("js/vendor/htmx-2.0.11.min.js"),
		script("js/vendor/alpine-csp-3.17.5.min.js"),
		`</head>`,
		`<body>`,
		`<a class="skip-link" href="#conteudo">pular para o conteúdo</a>`,
		`<div class="page">`,
		`<main id="conteudo" class="container">`,
		`<h1>Calendário</h1><p>olá</p>`,
		`</main>`,
		`<footer class="footer container">`,
		`</footer>`,
		`</div>`,
		`</body>`,
	)
	if n := strings.Count(body, `<main id="conteudo"`); n != 1 {
		t.Errorf("%d <main id=\"conteudo\"> elements, want 1", n)
	}
	if !strings.Contains(assets.URL("css/base.css"), "?v=") {
		t.Errorf("asset URL has no version: %s", assets.URL("css/base.css"))
	}
}

func TestPageTitleWithoutTitleAndEscaping(t *testing.T) {
	r, _ := newTestRenderer(t, realTemplates(t, map[string]string{"pages/base/x.html": basePage}))

	if body := renderPage(t, r, "base/x", PageData{}).Body.String(); !strings.Contains(body, `<title>AGENDA CLUBBER</title>`) {
		t.Errorf("empty Title should give a bare brand title:\n%s", body)
	}
	body := renderPage(t, r, "base/x", PageData{Title: `<b>"x"</b>`, Data: "<script>alert(1)</script>"}).Body.String()
	if strings.Contains(body, "<b>") || strings.Contains(body, "<script>alert") {
		t.Errorf("user data is not escaped:\n%s", body)
	}
}

func TestHTMXConfigMetaIsExact(t *testing.T) {
	r, _ := newTestRenderer(t, realTemplates(t, map[string]string{"pages/base/x.html": basePage}))
	body := renderPage(t, r, "base/x", PageData{}).Body.String()

	const want = `<meta name="htmx-config" content='{"allowEval":false,"allowScriptTags":false,"includeIndicatorStyles":false,"selfRequestsOnly":true,"historyCacheSize":0,"refreshOnHistoryMiss":true}'>`
	if !strings.Contains(body, want) {
		t.Fatalf("htmx-config meta missing or altered by escaping:\n%s", body)
	}
	m := regexp.MustCompile(`<meta name="htmx-config" content='([^']*)'>`).FindStringSubmatch(body)
	var cfg map[string]any
	if err := json.Unmarshal([]byte(m[1]), &cfg); err != nil {
		t.Fatalf("htmx-config is not JSON: %v", err)
	}
	wantCfg := map[string]any{
		"allowEval": false, "allowScriptTags": false, "includeIndicatorStyles": false,
		"selfRequestsOnly": true, "historyCacheSize": float64(0), "refreshOnHistoryMiss": true,
	}
	if !reflect.DeepEqual(cfg, wantCfg) {
		t.Errorf("htmx-config = %v, want %v", cfg, wantCfg)
	}
}

func TestFooter(t *testing.T) {
	r, _ := newTestRenderer(t, realTemplates(t, map[string]string{"pages/base/x.html": basePage}))
	body := renderPage(t, r, "base/x", PageData{}).Body.String()

	year := strconv.Itoa(time.Now().In(fortaleza(t)).Year())
	for _, want := range []string{
		`<span><span class="footer__prompt">$</span> feito_por --dev=IgnisDev</span>`,
		`<span>contato: <a href="mailto:ignisdev@magalz.space">ignisdev@magalz.space</a> · ` + year + `</span>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("footer missing %q in:\n%s", want, body)
		}
	}
}

func TestYearUsesTheConfiguredZone(t *testing.T) {
	r, _ := newTestRenderer(t, realTemplates(t, map[string]string{"pages/base/x.html": basePage}))
	// 01:00 UTC on 1 January is still 31 December at 22:00 in Fortaleza (UTC-3).
	r.now = func() time.Time { return time.Date(2027, 1, 1, 1, 0, 0, 0, time.UTC) }

	body := renderPage(t, r, "base/x", PageData{}).Body.String()
	if !strings.Contains(body, "ignisdev@magalz.space</a> · 2026</span>") {
		t.Errorf("footer year is not the Fortaleza year (2026):\n%s", body)
	}
}

func TestPageHeadExtra(t *testing.T) {
	r, _ := newTestRenderer(t, realTemplates(t, map[string]string{
		"pages/base/x.html": `{{define "head-extra"}}<meta name="robots" content="noindex">{{end}}` + basePage,
		"pages/base/y.html": basePage,
	}))

	x := renderPage(t, r, "base/x", PageData{}).Body.String()
	inOrder(t, x, `<meta name="robots" content="noindex">`, `</head>`)
	if y := renderPage(t, r, "base/y", PageData{}).Body.String(); strings.Contains(y, "noindex") {
		t.Errorf("head-extra of one page leaked into another:\n%s", y)
	}
}

const demoLayout = `{{define "shell"}}<div class="demo-shell"><nav>nav:{{.Nav}}</nav><main id="conteudo">{{template "content" .}}</main>{{template "footer" .}}</div>{{end}}`

func TestCustomLayoutOverridesShell(t *testing.T) {
	r, _ := newTestRenderer(t, realTemplates(t, map[string]string{
		"layouts/demo.html": demoLayout,
		"pages/demo/y.html": `{{define "content"}}<p>demo {{.Title}}</p>{{end}}`,
		"pages/base/x.html": basePage,
	}))

	body := renderPage(t, r, "demo/y", PageData{Title: "Y", Nav: "inicio"}).Body.String()
	inOrder(t, body,
		`<title>Y · AGENDA CLUBBER</title>`,
		`<a class="skip-link" href="#conteudo">`,
		`<div class="demo-shell"><nav>nav:inicio</nav><main id="conteudo"><p>demo Y</p></main>`,
		`<footer class="footer container">`,
	)
	if strings.Contains(body, `<main id="conteudo" class="container">`) {
		t.Errorf("default shell rendered next to the override:\n%s", body)
	}
	if n := strings.Count(body, `<main id="conteudo"`); n != 1 {
		t.Errorf("%d <main id=\"conteudo\"> elements, want 1", n)
	}

	// The override stays inside its own page set.
	other := renderPage(t, r, "base/x", PageData{}).Body.String()
	if strings.Contains(other, "demo-shell") || !strings.Contains(other, `<main id="conteudo" class="container">`) {
		t.Errorf("a base page picked up the demo layout:\n%s", other)
	}
}

func TestSamePageNameInTwoLayouts(t *testing.T) {
	r, _ := newTestRenderer(t, realTemplates(t, map[string]string{
		"layouts/demo.html":    demoLayout,
		"pages/base/same.html": `{{define "content"}}one{{end}}`,
		"pages/demo/same.html": `{{define "content"}}two{{end}}`,
	}))
	if body := renderPage(t, r, "base/same", PageData{}).Body.String(); !strings.Contains(body, ">one</main>") {
		t.Errorf("base/same:\n%s", body)
	}
	if body := renderPage(t, r, "demo/same", PageData{}).Body.String(); !strings.Contains(body, ">two</main>") {
		t.Errorf("demo/same:\n%s", body)
	}
}

func TestNewRendererFailsFast(t *testing.T) {
	tests := []struct {
		name  string
		fsys  fs.FS
		wants string // substring of the error
	}{
		{
			name:  "unknown layout directory",
			fsys:  withFiles(miniTemplates(nil), map[string]string{"pages/nolayout/z.html": `{{define "content"}}z{{end}}`}),
			wants: "nolayout",
		},
		{
			name:  "parse error in a page",
			fsys:  withFiles(miniTemplates(nil), map[string]string{"pages/base/bad.html": `{{define "content"}}{{if}}{{end}}`}),
			wants: "pages/base/bad.html",
		},
		{
			name:  "parse error in a partial",
			fsys:  withFiles(miniTemplates(nil), map[string]string{"partials/bad.html": `{{define "bad"}}{{end`}),
			wants: "partials/bad.html",
		},
		{
			name:  "parse error in the base layout",
			fsys:  withFiles(miniTemplates(nil), map[string]string{"layouts/base.html": `{{define "base"}}{{end`}),
			wants: "base.html",
		},
		{
			name:  "unknown function in a page",
			fsys:  withFiles(miniTemplates(nil), map[string]string{"pages/base/f.html": `{{define "content"}}{{nope}}{{end}}`}),
			wants: "pages/base/f.html",
		},
		{
			name:  "page without content",
			fsys:  withFiles(miniTemplates(nil), map[string]string{"pages/base/empty.html": `{{define "head-extra"}}x{{end}}`}),
			wants: "pages/base/empty.html",
		},
		{
			name:  "no base layout",
			fsys:  fstest.MapFS{"partials/p.html": {Data: []byte(`{{define "p"}}p{{end}}`)}},
			wants: "base.html",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewRenderer(tt.fsys, renderAssets(t), fortaleza(t), slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
			if err == nil {
				t.Fatalf("NewRenderer succeeded, want an error (renderer %v)", r)
			}
			if !strings.Contains(err.Error(), tt.wants) {
				t.Errorf("error %q does not mention %q", err, tt.wants)
			}
		})
	}
}

func TestNewRendererRejectsMissingDependencies(t *testing.T) {
	log := slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))
	assets, loc, fsys := renderAssets(t), fortaleza(t), miniTemplates(nil)
	for name, call := range map[string]func() (*Renderer, error){
		"nil assets":   func() (*Renderer, error) { return NewRenderer(fsys, nil, loc, log) },
		"nil location": func() (*Renderer, error) { return NewRenderer(fsys, assets, nil, log) },
		"nil logger":   func() (*Renderer, error) { return NewRenderer(fsys, assets, loc, nil) },
	} {
		if r, err := call(); err == nil {
			t.Errorf("%s: NewRenderer succeeded (%v), want an error", name, r)
		}
	}
}

func TestPartial(t *testing.T) {
	r, _ := newTestRenderer(t, miniTemplates(nil))
	c, rec := newCtx()
	r.Partial(c, http.StatusCreated, "wrap", map[string]any{"Name": "<ana>"})

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got, want := rec.Body.String(), `<div><span class="chip">&lt;ana&gt;</span></div>`; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("a fragment must not carry the document: %s", rec.Body)
	}
}

func TestPartialRendersTheShippedFooter(t *testing.T) {
	r, _ := newTestRenderer(t, realTemplates(t, nil))
	c, rec := newCtx()
	r.Partial(c, http.StatusOK, "footer", nil)

	if !strings.HasPrefix(rec.Body.String(), `<footer class="footer container">`) || strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("footer fragment:\n%s", rec.Body)
	}
}

func TestExecutionErrorIs500(t *testing.T) {
	r, logs := newTestRenderer(t, withFiles(miniTemplates(nil), map[string]string{
		// A nil *ViewUser: the error happens after "[" is already written.
		"pages/base/boom.html": `{{define "content"}}{{.User.Name}}{{end}}`,
	}))

	rec := renderPage(t, r, "base/boom", PageData{})
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if got := rec.Body.String(); got != "erro interno" {
		t.Errorf("body = %q, want %q (nothing of the partial output may leak)", got, "erro interno")
	}
	var logged map[string]any
	for _, l := range logLines(t, logs) {
		if l["level"] == "ERROR" {
			logged = l
		}
	}
	if logged == nil {
		t.Fatalf("no ERROR log entry: %s", logs)
	}
	if logged["page"] != "base/boom" {
		t.Errorf("logged page = %v, want base/boom", logged["page"])
	}
	// "err", as in cmd/agenda: one key for errors across the service's logs.
	if e, _ := logged["err"].(string); !strings.Contains(e, "Name") {
		t.Errorf("logged err = %q, want the template error", e)
	}
	if _, ok := logged["error"]; ok {
		t.Errorf("render error is also logged under \"error\": %v", logged)
	}

	c, prec := newCtx()
	r.Partial(c, http.StatusOK, "broken", (*ViewUser)(nil))
	if prec.Code != http.StatusInternalServerError || prec.Body.String() != "erro interno" {
		t.Errorf("partial error: status %d body %q", prec.Code, prec.Body)
	}
}

func TestUnknownNamesPanic(t *testing.T) {
	r, _ := newTestRenderer(t, miniTemplates(nil))
	if !panics(func() { renderPage(t, r, "base/missing", PageData{}) }) {
		t.Error("Page with an unknown page name did not panic")
	}
	if !panics(func() {
		c, _ := newCtx()
		r.Partial(c, http.StatusOK, "missing", nil)
	}) {
		t.Error("Partial with an unknown name did not panic")
	}
}

func TestDict(t *testing.T) {
	got, err := dict("a", 1, "b", "two")
	if err != nil {
		t.Fatalf("dict: %v", err)
	}
	if want := map[string]any{"a": 1, "b": "two"}; !reflect.DeepEqual(got, want) {
		t.Errorf("dict = %v, want %v", got, want)
	}
	if got, err := dict(); err != nil || len(got) != 0 {
		t.Errorf("dict() = %v, %v, want an empty map", got, err)
	}
	if _, err := dict("a"); err == nil {
		t.Error("an odd number of arguments is not an error")
	}
	if _, err := dict(1, "a"); err == nil {
		t.Error("a non-string key is not an error")
	}
}

func TestDictErrorInTemplateIs500(t *testing.T) {
	r, _ := newTestRenderer(t, withFiles(miniTemplates(nil), map[string]string{
		"pages/base/d.html": `{{define "content"}}{{template "chip" (dict "Label")}}{{end}}`,
	}))
	if rec := renderPage(t, r, "base/d", PageData{}); rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

var (
	styleAttr   = regexp.MustCompile(`(?i)\sstyle\s*=`)
	handlerAttr = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	scriptTag   = regexp.MustCompile(`(?i)<script\b[^>]*>`)
	srcAttr     = regexp.MustCompile(`(?i)\ssrc\s*=`)
)

// cspViolations lists what the CSP would block in rendered HTML: style
// attributes, event handler attributes and inline scripts.
func cspViolations(body string) []string {
	var found []string
	if styleAttr.MatchString(body) {
		found = append(found, "style attribute")
	}
	if m := handlerAttr.FindString(body); m != "" {
		found = append(found, "event handler attribute "+strings.TrimSpace(m))
	}
	for _, tag := range scriptTag.FindAllString(body, -1) {
		if !srcAttr.MatchString(tag) {
			found = append(found, "inline script "+tag)
		}
	}
	return found
}

// The other tests hash a hand-written file list. This one uses the App's own
// Assets, so a misspelled asset name in the shipped base layout, or a script
// that is not in web/static, fails here (as a 500 on every page in production).
func TestShippedBaseRendersWithTheEmbeddedAssets(t *testing.T) {
	app := NewApp(config.Config{Env: "prod", Location: fortaleza(t)}, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	templates := realTemplates(t, map[string]string{"pages/base/x.html": basePage})
	logs := &bytes.Buffer{}
	r, err := NewRenderer(templates, app.Assets, fortaleza(t), slog.New(slog.NewJSONHandler(logs, nil)))
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	rec := renderPage(t, r, "base/x", PageData{Title: "T"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %q, log %s", rec.Code, rec.Body, logs)
	}
	body := rec.Body.String()
	for _, name := range []string{
		"favicon.svg", "css/tokens.css", "css/base.css", "css/components.css",
		"js/app.js", "js/vendor/htmx-2.0.11.min.js", "js/vendor/alpine-csp-3.17.5.min.js",
	} {
		if !strings.Contains(body, app.Assets.URL(name)) {
			t.Errorf("page does not link %s", name)
		}
	}
	if v := cspViolations(body); len(v) > 0 {
		t.Errorf("%s in:\n%s", strings.Join(v, "; "), body)
	}
}

func TestRenderedHTMLIsCSPSafe(t *testing.T) {
	r, _ := newTestRenderer(t, realTemplates(t, map[string]string{
		"layouts/demo.html": demoLayout,
		"pages/base/x.html": basePage,
		"pages/demo/y.html": basePage,
	}))
	for _, page := range []string{"base/x", "demo/y"} {
		body := renderPage(t, r, page, PageData{Title: "T", Nav: "n", Data: "d"}).Body.String()
		if !scriptTag.MatchString(body) {
			t.Fatalf("%s: no <script> found; the check would pass vacuously", page)
		}
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("%s: %s in:\n%s", page, strings.Join(v, "; "), body)
		}
	}

	// The check itself must catch each violation and accept clean markup.
	for _, bad := range []string{
		`<p style="color:red">x</p>`,
		`<button onclick="x()">x</button>`,
		"<img src=a\nonerror=x()>",
		`<script>alert(1)</script>`,
	} {
		if len(cspViolations(bad)) == 0 {
			t.Errorf("cspViolations lets %q through", bad)
		}
	}
	if v := cspViolations(`<script src="/a.js" defer></script><p class="x">on=1</p>`); len(v) != 0 {
		t.Errorf("cspViolations flags clean HTML: %v", v)
	}
}
