package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

const secret = "SECRET-outside-the-static-tree"

// fixtureFS is a static tree with a secret file one level above it, so a
// traversal bug returns real bytes instead of an innocent 404.
func fixtureFS(t *testing.T) fs.FS {
	t.Helper()
	root := fstest.MapFS{
		"go.mod":                   {Data: []byte(secret)},
		"static/css/base.css":      {Data: []byte("body{margin:0}")},
		"static/js/app.js":         {Data: []byte("console.log(1)")},
		"static/fonts/f.woff2":     {Data: []byte("wOF2-bytes")},
		"static/favicon.svg":       {Data: []byte("<svg xmlns='http://www.w3.org/2000/svg'/>")},
		"static/robots.txt":        {Data: []byte("User-agent: *")},
		"static/data.bin":          {Data: []byte{0, 1, 2, 3}},
		"static/page.html":         {Data: []byte("<h1>no</h1>")},
		"static/.hidden":           {Data: []byte(secret)},
		"static/dir/.secret/x.css": {Data: []byte(secret)},
		"static/dir/ok.css":        {Data: []byte("a{}")},
	}
	sub, err := fs.Sub(root, "static")
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

// assetsEngine mounts only the static routes of a, without the application
// middleware.
func assetsEngine(t *testing.T, a *Assets) *gin.Engine {
	t.Helper()
	r := gin.New()
	r.GET("/static/*filepath", a.Handler())
	r.HEAD("/static/*filepath", a.Handler())
	return r
}

func newFixtureAssets(t *testing.T) *Assets {
	t.Helper()
	a, err := NewAssets(fixtureFS(t))
	if err != nil {
		t.Fatalf("NewAssets: %v", err)
	}
	return a
}

func hash10(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:10]
}

func mustPanic(t *testing.T, want string, f func()) {
	t.Helper()
	defer func() {
		t.Helper()
		v := recover()
		if v == nil {
			t.Fatal("no panic")
		}
		if msg, _ := v.(string); !strings.Contains(msg, want) {
			t.Errorf("panic = %v, want it to mention %q", v, want)
		}
	}()
	f()
}

func TestAssetsURL(t *testing.T) {
	a := newFixtureAssets(t)

	got := a.URL("css/base.css")
	if !regexp.MustCompile(`^/static/css/base\.css\?v=[0-9a-f]{10}$`).MatchString(got) {
		t.Fatalf("URL = %q, want /static/css/base.css?v=<10 hex>", got)
	}
	if want := "/static/css/base.css?v=" + hash10([]byte("body{margin:0}")); got != want {
		t.Errorf("URL = %q, want %q (first 10 hex of the content's sha256)", got, want)
	}
	if other := a.URL("js/app.js"); strings.HasSuffix(other, "v="+hash10([]byte("body{margin:0}"))) {
		t.Errorf("different files share a hash: %q", other)
	}
}

func TestAssetsURLPanicsForNamesThatAreNotServed(t *testing.T) {
	a := newFixtureAssets(t)
	for _, name := range []string{
		"css/nope.css",      // unknown
		"css",               // a directory
		"/css/base.css",     // leading slash
		"../go.mod",         // outside the tree
		"page.html",         // html is never served
		".hidden",           // dot file
		"dir/.secret/x.css", // inside a dot directory
	} {
		t.Run(name, func(t *testing.T) {
			mustPanic(t, name, func() { a.URL(name) })
		})
	}
}

func TestAssetsServe(t *testing.T) {
	a := newFixtureAssets(t)
	r := assetsEngine(t, a)

	tests := []struct {
		name, contentType, body string
	}{
		{"css/base.css", "text/css; charset=utf-8", "body{margin:0}"},
		{"js/app.js", "text/javascript; charset=utf-8", "console.log(1)"},
		{"fonts/f.woff2", "font/woff2", "wOF2-bytes"},
		{"favicon.svg", "image/svg+xml", "<svg xmlns='http://www.w3.org/2000/svg'/>"},
		{"robots.txt", "text/plain; charset=utf-8", "User-agent: *"},
		{"data.bin", "application/octet-stream", "\x00\x01\x02\x03"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := hash10([]byte(tt.body))
			rec := do(r, http.MethodGet, "/static/"+tt.name+"?v="+h, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != tt.contentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
				t.Errorf("Cache-Control = %q, want public, max-age=31536000, immutable", got)
			}
			if got := rec.Header().Get("ETag"); got != `"`+h+`"` {
				t.Errorf("ETag = %q, want %q", got, `"`+h+`"`)
			}
			if rec.Body.String() != tt.body {
				t.Errorf("body = %q, want %q", rec.Body.String(), tt.body)
			}
		})
	}
}

func TestAssetsCacheControlDependsOnVersionQuery(t *testing.T) {
	a := newFixtureAssets(t)
	r := assetsEngine(t, a)
	h := hash10([]byte("body{margin:0}"))

	for _, tt := range []struct{ name, query, want string }{
		{"matching v", "?v=" + h, "public, max-age=31536000, immutable"},
		{"no v", "", "no-cache"},
		{"empty v", "?v=", "no-cache"},
		{"wrong v", "?v=0000000000", "no-cache"},
		{"v of another file", "?v=" + hash10([]byte("console.log(1)")), "no-cache"},
		{"unrelated query", "?x=" + h, "no-cache"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(r, http.MethodGet, "/static/css/base.css"+tt.query, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.want {
				t.Errorf("Cache-Control = %q, want %q", got, tt.want)
			}
			if got := rec.Header().Get("ETag"); got != `"`+h+`"` {
				t.Errorf("ETag = %q, want %q (sent whatever the query)", got, `"`+h+`"`)
			}
		})
	}
}

func TestAssetsConditionalGet(t *testing.T) {
	a := newFixtureAssets(t)
	r := assetsEngine(t, a)
	h := hash10([]byte("body{margin:0}"))
	etag := `"` + h + `"`
	withINM := func(v string) func(*http.Request) {
		return func(req *http.Request) { req.Header.Set("If-None-Match", v) }
	}

	t.Run("matching ETag is 304 without a body", func(t *testing.T) {
		rec := do(r, http.MethodGet, "/static/css/base.css?v="+h, nil, withINM(etag))
		if rec.Code != http.StatusNotModified {
			t.Fatalf("status = %d, want 304", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("304 carries a body: %q", rec.Body.String())
		}
		if got := rec.Header().Get("ETag"); got != etag {
			t.Errorf("ETag = %q, want %q", got, etag)
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
			t.Errorf("Cache-Control = %q on the 304", got)
		}
	})
	t.Run("matching ETag among several", func(t *testing.T) {
		rec := do(r, http.MethodGet, "/static/css/base.css", nil, withINM(`"other", `+etag))
		if rec.Code != http.StatusNotModified {
			t.Errorf("status = %d, want 304", rec.Code)
		}
	})
	t.Run("other ETag is a full 200", func(t *testing.T) {
		rec := do(r, http.MethodGet, "/static/css/base.css", nil, withINM(`"0000000000"`))
		if rec.Code != http.StatusOK || rec.Body.String() != "body{margin:0}" {
			t.Errorf("got %d %q, want 200 with the body", rec.Code, rec.Body.String())
		}
	})
}

func TestAssetsHead(t *testing.T) {
	a := newFixtureAssets(t)
	r := assetsEngine(t, a)
	h := hash10([]byte("body{margin:0}"))

	rec := do(r, http.MethodHead, "/static/css/base.css?v="+h, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD carries a body: %q", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "text/css; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got, want := rec.Header().Get("Content-Length"), strconv.Itoa(len("body{margin:0}")); got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if got := rec.Header().Get("ETag"); got != `"`+h+`"` {
		t.Errorf("ETag = %q", got)
	}
}

func TestAssetsNotFound(t *testing.T) {
	a := newFixtureAssets(t)
	r := assetsEngine(t, a)

	for _, target := range []string{
		"/static/css/",                    // directory
		"/static/css",                     // directory without the slash
		"/static/",                        // the root
		"/static/dir/",                    // directory
		"/static/css/missing.css",         // missing file
		"/static/../go.mod",               // traversal
		"/static/%2e%2e/go.mod",           // encoded traversal
		"/static/css/../../go.mod",        // traversal from a subdirectory
		"/static/css/../css/base.css",     // not normalized: only the exact name is served
		"/static//css/base.css",           // empty segment
		"/static/./css/base.css",          // dot segment
		"/static/.hidden",                 // dot file
		"/static/dir/.secret/x.css",       // inside a dot directory
		"/static/dir/%2esecret/x.css",     // encoded dot directory
		"/static/page.html",               // html is never served
		"/static/css%5Cbase.css",          // backslash is not a separator
		"/static/css/base.css/",           // trailing slash on a file
		"/static/css/base.css/..%2fx.css", // file as a directory
	} {
		t.Run(target, func(t *testing.T) {
			rec := do(r, http.MethodGet, target, nil)
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
			if strings.Contains(rec.Body.String(), secret) {
				t.Errorf("body leaks the secret file: %q", rec.Body.String())
			}
			if rec.Header().Get("ETag") != "" || strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
				t.Errorf("404 carries asset cache headers: %v", rec.Header())
			}
		})
	}
}

func TestStaticRoutesAreGetAndHeadOnly(t *testing.T) {
	r, _ := newRouter(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			if rec := do(r, method, "/static/css/base.css", nil); rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
	}
}

// TestEmbeddedStatic serves the real embedded tree through the application
// router, so it also covers the wiring in NewApp and Router.
func TestEmbeddedStatic(t *testing.T) {
	app := NewApp(config.Config{Env: "prod", Location: fortaleza(t)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r := app.Router()

	tests := []struct{ name, contentType string }{
		{"css/base.css", "text/css; charset=utf-8"},
		{"js/vendor/htmx-2.0.11.min.js", "text/javascript; charset=utf-8"},
		{"fonts/space-mono-latin-400-normal.woff2", "font/woff2"},
		{"favicon.svg", "image/svg+xml"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, err := fs.ReadFile(webfs.Static, "static/"+tt.name)
			if err != nil {
				t.Fatal(err)
			}
			h := hash10(want)
			if got, wantURL := app.Assets.URL(tt.name), "/static/"+tt.name+"?v="+h; got != wantURL {
				t.Fatalf("URL = %q, want %q", got, wantURL)
			}

			rec := do(r, http.MethodGet, app.Assets.URL(tt.name), nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); got != tt.contentType {
				t.Errorf("Content-Type = %q, want %q", got, tt.contentType)
			}
			if got := rec.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
				t.Errorf("Cache-Control = %q", got)
			}
			if !bytes.Equal(rec.Body.Bytes(), want) {
				t.Errorf("body differs from the embedded file (%d bytes, want %d)", rec.Body.Len(), len(want))
			}
			checkSecurityHeaders(t, rec.Header())

			cond := do(r, http.MethodGet, app.Assets.URL(tt.name), nil, func(req *http.Request) {
				req.Header.Set("If-None-Match", rec.Header().Get("ETag"))
			})
			if cond.Code != http.StatusNotModified {
				t.Errorf("conditional status = %d, want 304", cond.Code)
			}
			checkSecurityHeaders(t, cond.Header())
		})
	}
}
