package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// answerNotFound answers like a path no route serves, the way Gin does it for
// one (serveError): its status, its body and a Content-Type of exactly
// text/plain, with no other header of its own, so nobody can tell a page they may
// not see (a collective they may not manage, an event that does not exist) from
// a path that does not exist. Gin ignores a failed write too: the client is gone
// and there is nothing left to answer.
func answerNotFound(c *gin.Context) {
	c.Header("Content-Type", "text/plain")
	c.Status(http.StatusNotFound)
	_, _ = c.Writer.WriteString("404 page not found")
}
