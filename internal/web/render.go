package web

import (
	"io/fs"
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

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

// Renderer renders full pages and HTMX fragments.
type Renderer struct {
	now func() time.Time
}

// NewRenderer is a stub.
func NewRenderer(fsys fs.FS, assets *Assets, loc *time.Location, log *slog.Logger) (*Renderer, error) {
	return &Renderer{now: time.Now}, nil
}

// Page is a stub.
func (r *Renderer) Page(c *gin.Context, status int, page string, data PageData) {}

// Partial is a stub.
func (r *Renderer) Partial(c *gin.Context, status int, name string, data any) {}

func dict(kv ...any) (map[string]any, error) { return nil, nil }
