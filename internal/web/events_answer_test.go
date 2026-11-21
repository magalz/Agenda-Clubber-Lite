package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/events"
)

func TestEventsAnswer(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
		msg    string
	}{
		{"not allowed", events.ErrNotAllowed, http.StatusForbidden, approvalsMsgNotAllowed},
		{"blocked", events.ErrBlocked, http.StatusConflict, eventsMsgBlocked},
		{"changed", events.ErrChanged, http.StatusConflict, eventsMsgChanged},
		{"wrapped changed", fmt.Errorf("events: update: %w", events.ErrChanged), http.StatusConflict, eventsMsgChanged},
		{"not found", domain.ErrNotFound, http.StatusNotFound, ""},
		{"wrapped not found", fmt.Errorf("events: read event: %w", domain.ErrNotFound), http.StatusNotFound, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, msg, ok := eventsAnswer(tt.err)
			if !ok || status != tt.status || msg != tt.msg {
				t.Errorf("eventsAnswer = (%d, %q, %v), want (%d, %q, true)", status, msg, ok, tt.status, tt.msg)
			}
		})
	}

	for _, err := range []error{errors.New("boom"), domain.FieldErrors{"name": "x"}, nil} {
		if status, msg, ok := eventsAnswer(err); ok {
			t.Errorf("eventsAnswer(%v) = (%d, %q, true), want ok false", err, status, msg)
		}
	}
}

// TestAnswerNotFoundMatchesTheRouter checks that answerNotFound is
// indistinguishable from a path no route serves: same status, body and headers.
func TestAnswerNotFoundMatchesTheRouter(t *testing.T) {
	r := gin.New()
	r.GET("/answered", answerNotFound)

	router := httptest.NewRecorder()
	r.ServeHTTP(router, httptest.NewRequest(http.MethodGet, "/no-such-path", nil))
	answered := httptest.NewRecorder()
	r.ServeHTTP(answered, httptest.NewRequest(http.MethodGet, "/answered", nil))

	if answered.Code != router.Code || answered.Body.String() != router.Body.String() {
		t.Errorf("answerNotFound = %d %q, router = %d %q", answered.Code, answered.Body.String(), router.Code, router.Body.String())
	}
	if fmt.Sprint(answered.Header()) != fmt.Sprint(router.Header()) {
		t.Errorf("headers: answerNotFound %v, router %v", answered.Header(), router.Header())
	}
}

// TestEventPathsCoexist proves that Gin routes the event paths of Phase 4 to
// the right handler: the static segments (novo, dia) next to the :id parameter,
// and :id followed by editar and excluir. The tasks register these paths in
// separate files, so a conflict would only show at startup.
func TestEventPathsCoexist(t *testing.T) {
	r := gin.New()
	handle := func(name string) gin.HandlerFunc {
		return func(c *gin.Context) { c.String(http.StatusOK, name+" "+c.Param("id")) }
	}
	r.GET("/eventos/novo", handle("form-new"))
	r.POST("/eventos/novo", handle("create"))
	r.GET("/eventos/dia", handle("day"))
	r.GET("/eventos/:id", handle("detail"))
	r.GET("/eventos/:id/editar", handle("form-edit"))
	r.POST("/eventos/:id/editar", handle("update"))
	r.POST("/eventos/:id/excluir", handle("delete"))

	for _, tc := range []struct{ method, path, want string }{
		{http.MethodGet, "/eventos/novo", "form-new "},
		{http.MethodPost, "/eventos/novo", "create "},
		{http.MethodGet, "/eventos/dia", "day "},
		{http.MethodGet, "/eventos/6735f0c2a1b2c3d4e5f60718", "detail 6735f0c2a1b2c3d4e5f60718"},
		{http.MethodGet, "/eventos/nov", "detail nov"},
		{http.MethodGet, "/eventos/6735f0c2a1b2c3d4e5f60718/editar", "form-edit 6735f0c2a1b2c3d4e5f60718"},
		{http.MethodPost, "/eventos/6735f0c2a1b2c3d4e5f60718/editar", "update 6735f0c2a1b2c3d4e5f60718"},
		{http.MethodPost, "/eventos/6735f0c2a1b2c3d4e5f60718/excluir", "delete 6735f0c2a1b2c3d4e5f60718"},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != http.StatusOK || w.Body.String() != tc.want {
			t.Errorf("%s %s = %d %q, want 200 %q", tc.method, tc.path, w.Code, w.Body.String(), tc.want)
		}
	}
}
