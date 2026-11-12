package web

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const htmlContentType = "text/html; charset=utf-8"

// ViewUser is the signed-in user as the templates see it.
type ViewUser struct {
	ID              int64
	Name            string
	IsAdmin         bool
	CanCreateEvents bool
}

// PageData is the data every page template receives.
type PageData struct {
	Title string    // page title; the layout appends " · AGENDA CLUBBER"
	Nav   string    // key of the active sidebar entry
	User  *ViewUser // nil when logged out
	Data  any       // page-specific data
}

// Renderer renders full pages and HTMX fragments from the template tree:
//
//	layouts/base.html          defines "base", the whole document
//	layouts/<layout>.html      overrides blocks of base (for example "shell")
//	partials/*.html            named templates shared by pages and fragments
//	pages/<layout>/<page>.html defines "content" (and may define "head-extra")
//
// A page's layout is its parent directory, and its name is "<layout>/<page>".
// Every page is parsed into a set of its own, so two pages can define the same
// template names without clashing.
type Renderer struct {
	pages     map[string]*template.Template
	fragments *template.Template
	log       *slog.Logger
	now       func() time.Time
}

// NewRenderer parses the whole template tree. Any error (a missing base
// layout, a page in an unknown layout, a template that does not parse) is
// returned here, so the process fails at startup and not on the first request.
func NewRenderer(fsys fs.FS, assets *Assets, loc *time.Location, log *slog.Logger) (*Renderer, error) {
	if assets == nil || loc == nil || log == nil {
		return nil, errors.New("web: renderer needs assets, a time zone and a logger")
	}
	r := &Renderer{pages: make(map[string]*template.Template), log: log, now: time.Now}
	funcs := template.FuncMap{
		"asset": assets.URL,
		"year":  func() int { return r.now().In(loc).Year() },
		"dict":  dict,
	}

	// Glob only fails on a malformed pattern, and these are constants.
	partials, _ := fs.Glob(fsys, "partials/*.html")
	pages, _ := fs.Glob(fsys, "pages/*/*.html")

	base := template.New("").Funcs(funcs)
	if err := parseFiles(base, fsys, append([]string{"layouts/base.html"}, partials...)...); err != nil {
		return nil, err
	}
	r.fragments = template.New("").Funcs(funcs)
	if err := parseFiles(r.fragments, fsys, partials...); err != nil {
		return nil, err
	}

	for _, file := range pages {
		layout := path.Base(path.Dir(file))
		set, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("web: templates: %w", err)
		}
		// Later definitions replace earlier ones: the layout overrides blocks
		// of base, and the page defines "content".
		if layout != "base" {
			if err := parseFiles(set, fsys, "layouts/"+layout+".html"); err != nil {
				return nil, fmt.Errorf("web: templates: %s uses unknown layout %q: %w", file, layout, err)
			}
		}
		if err := parseFiles(set, fsys, file); err != nil {
			return nil, err
		}
		if set.Lookup("content") == nil {
			return nil, fmt.Errorf("web: templates: %s does not define \"content\"", file)
		}
		r.pages[layout+"/"+strings.TrimSuffix(path.Base(file), ".html")] = set
	}
	return r, nil
}

// parseFiles parses each file into set. A file's own template is named by its
// full path, not its base name, so errors point at the file and two files with
// the same base name in different directories do not overwrite each other.
func parseFiles(set *template.Template, fsys fs.FS, names ...string) error {
	for _, name := range names {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("web: templates: %w", err)
		}
		if _, err := set.New(name).Parse(string(data)); err != nil {
			return fmt.Errorf("web: templates: %s: %w", name, err)
		}
	}
	return nil
}

// Page renders the named page ("<layout>/<page>") inside its layout. It
// panics for a name that is not a page: that is a programming error, and the
// handler tests catch it. A rendering error is logged and answered with a
// plain 500, and nothing of the half-rendered page is sent.
func (r *Renderer) Page(c *gin.Context, status int, page string, data PageData) {
	set, ok := r.pages[page]
	if !ok {
		panic(fmt.Sprintf("web: unknown page %q", page))
	}
	r.write(c, status, set, "base", "page", page, data)
}

// Partial renders one named template of partials/ without any layout, for an
// HTMX response. It panics for an unknown name and answers errors like Page.
func (r *Renderer) Partial(c *gin.Context, status int, name string, data any) {
	if r.fragments.Lookup(name) == nil {
		panic(fmt.Sprintf("web: unknown partial %q", name))
	}
	r.write(c, status, r.fragments, name, "partial", name, data)
}

// write executes tmpl into a buffer first, so an error never leaves a
// truncated document on the wire.
func (r *Renderer) write(c *gin.Context, status int, set *template.Template, tmpl, kind, name string, data any) {
	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, tmpl, data); err != nil {
		r.log.Error("render failed", kind, name, "err", err)
		c.String(http.StatusInternalServerError, "erro interno")
		return
	}
	c.Data(status, htmlContentType, buf.Bytes())
}

// dict builds a map from key/value pairs, to pass several values to a
// partial: {{template "chip" (dict "Label" .Name "Active" true)}}.
func dict(kv ...any) (map[string]any, error) {
	if len(kv)%2 != 0 {
		return nil, errors.New("dict: odd number of arguments")
	}
	m := make(map[string]any, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			return nil, fmt.Errorf("dict: key %d is %T, want string", i/2, kv[i])
		}
		m[key] = kv[i+1]
	}
	return m, nil
}
