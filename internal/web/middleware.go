package web

import "github.com/gin-gonic/gin"

// ContentSecurityPolicy is the policy sent on every response. It matches the
// one in AGENTS.md exactly; templates therefore carry no inline script, no
// inline style and no event-handler attributes.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// maxBodyBytes caps the request body (AGENTS.md: 64 KiB).
const maxBodyBytes = 64 << 10

// securityHeaders is a stub until the implementation commit.
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

// limitBody is a stub until the implementation commit.
func limitBody(int64) gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}
