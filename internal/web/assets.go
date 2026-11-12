package web

import (
	"io/fs"

	"github.com/gin-gonic/gin"
)

// Assets serves the embedded static files with content-hash cache busting.
type Assets struct{}

// NewAssets is a stub until the implementation commit.
func NewAssets(fs.FS) (*Assets, error) {
	return &Assets{}, nil
}

// URL is a stub until the implementation commit.
func (a *Assets) URL(string) string {
	return ""
}

// Handler is a stub until the implementation commit.
func (a *Assets) Handler() gin.HandlerFunc {
	return func(*gin.Context) {}
}
