package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestSignedInUserMissingIsA500 checks the miss of signedInUser: a handler of an
// authed route reached without a user (a route registered outside the authed
// group) answers 500 "erro interno" and logs which flow asked, without the user.
func TestSignedInUserMissingIsA500(t *testing.T) {
	app := newTestApp(t)
	var logs bytes.Buffer
	app.Log = slog.New(slog.NewJSONHandler(&logs, nil))

	r := gin.New()
	reached := false
	r.GET("/x", func(c *gin.Context) {
		if _, ok := app.signedInUser(c, "events"); ok {
			reached = true
			c.String(http.StatusOK, "ok")
		}
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))

	if reached || w.Code != http.StatusInternalServerError || w.Body.String() != "erro interno" {
		t.Errorf("got %d %q (reached %v), want 500 \"erro interno\"", w.Code, w.Body.String(), reached)
	}
	if !strings.Contains(logs.String(), `"flow":"events"`) {
		t.Errorf("log %q does not name the flow", logs.String())
	}
}
