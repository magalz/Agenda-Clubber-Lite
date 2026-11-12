package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// ContentSecurityPolicy is the policy sent on every response. It matches the
// one in AGENTS.md exactly; templates therefore carry no inline script, no
// inline style and no event-handler attributes.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// maxBodyBytes caps the request body (AGENTS.md: 64 KiB).
const maxBodyBytes = 64 << 10

// securityHeaders sets the security headers before the handlers run, so they
// are on every response that passes through the chain: 404s, recovered panics
// and the 413 from limitBody included.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		c.Next()
	}
}

// limitBody caps the request body of every method that can carry one. A
// declared Content-Length over max is refused with 413 before the handler runs;
// a body of unknown length (chunked) makes the handler's read fail with
// *http.MaxBytesError once it passes max.
func limitBody(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		if c.Request.ContentLength > max {
			c.String(http.StatusRequestEntityTooLarge, "corpo grande demais")
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		c.Next()
	}
}
