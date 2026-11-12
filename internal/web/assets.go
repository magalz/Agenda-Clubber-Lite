package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// hashLen is the number of hex characters of the content's sha256 used
	// as the version of an asset.
	hashLen = 10

	immutableCache = "public, max-age=31536000, immutable"
)

// contentTypes is explicit on purpose: mime.TypeByExtension reads the
// registry on Windows and answers differently per machine. An extension that
// is not listed is served as application/octet-stream (under nosniff).
var contentTypes = map[string]string{
	".css":   "text/css; charset=utf-8",
	".js":    "text/javascript; charset=utf-8",
	".woff2": "font/woff2",
	".svg":   "image/svg+xml",
	".txt":   "text/plain; charset=utf-8",
}

// Assets serves the embedded static files and builds their versioned URLs.
type Assets struct {
	fsys   fs.FS
	hashes map[string]string // slash-separated name -> first hashLen hex of sha256
}

// NewAssets hashes every file of fsys once. Only files that may be served are
// registered: a path with a segment starting with "." and any .html file are
// left out, so they get no URL and the handler answers 404 for them.
func NewAssets(fsys fs.FS) (*Assets, error) {
	hashes := make(map[string]string)
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || strings.EqualFold(path.Ext(name), ".html") {
			return nil
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		hashes[name] = hex.EncodeToString(sum[:])[:hashLen]
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk static files: %w", err)
	}
	return &Assets{fsys: fsys, hashes: hashes}, nil
}

// URL returns the versioned URL of a static file, for example
// "/static/css/base.css?v=0123456789". It panics for a name that is not
// served, so a typo in a template fails when the template is rendered (in
// tests) and never ships as a dead link.
func (a *Assets) URL(name string) string {
	hash, ok := a.hashes[name]
	if !ok {
		panic(fmt.Sprintf("web: unknown static asset %q", name))
	}
	return "/static/" + name + "?v=" + hash
}

// Handler serves GET and HEAD /static/*filepath. Mount it on both methods.
//
// It is an allowlist, not a path cleaner: only the exact names registered by
// NewAssets are served. Gin hands the path over as sent ("/../go.mod" stays
// that way), and none of those is a registered name, so directories, missing
// files, traversal, hidden files and .html all end in 404.
func (a *Assets) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		name := strings.TrimPrefix(c.Param("filepath"), "/")
		hash, ok := a.hashes[name]
		if !ok {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		data, err := fs.ReadFile(a.fsys, name)
		if err != nil {
			// It was readable when NewAssets hashed it.
			panic(fmt.Sprintf("web: reading static asset %q: %v", name, err))
		}

		contentType, ok := contentTypes[strings.ToLower(path.Ext(name))]
		if !ok {
			contentType = "application/octet-stream"
		}
		h := c.Writer.Header()
		h.Set("Content-Type", contentType)
		h.Set("ETag", `"`+hash+`"`)
		if c.Query("v") == hash {
			h.Set("Cache-Control", immutableCache)
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		// With no modification time, ServeContent answers If-None-Match with
		// 304, sends no body for HEAD and honors Range.
		http.ServeContent(c.Writer, c.Request, name, time.Time{}, bytes.NewReader(data))
	}
}
