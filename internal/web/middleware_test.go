package web

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// wantCSP is a literal copy of the policy in AGENTS.md. It is deliberately not
// ContentSecurityPolicy, so an edit to the constant fails the test.
const wantCSP = "default-src 'self'; script-src 'self'; style-src 'self'; font-src 'self'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

var wantSecurityHeaders = map[string]string{
	"Content-Security-Policy":    wantCSP,
	"X-Frame-Options":            "DENY",
	"X-Content-Type-Options":     "nosniff",
	"Referrer-Policy":            "strict-origin-when-cross-origin",
	"Permissions-Policy":         "camera=(), microphone=(), geolocation=(), payment=()",
	"Cross-Origin-Opener-Policy": "same-origin",
}

// checkSecurityHeaders fails unless every security header is present exactly
// once with its exact value.
func checkSecurityHeaders(t *testing.T, h http.Header) {
	t.Helper()
	for name, want := range wantSecurityHeaders {
		if got := h.Values(name); len(got) != 1 || got[0] != want {
			t.Errorf("%s = %q, want exactly [%q]", name, got, want)
		}
	}
}

// do sends a request with a body through the handler.
func do(h http.Handler, method, target string, body io.Reader, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	for _, m := range mod {
		m(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// chunked makes a request look like a chunked upload: no declared length, so
// limitBody cannot reject it up front.
func chunked(req *http.Request) {
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
}

func TestContentSecurityPolicyConstant(t *testing.T) {
	if ContentSecurityPolicy != wantCSP {
		t.Errorf("ContentSecurityPolicy = %q, want %q", ContentSecurityPolicy, wantCSP)
	}
}

func TestSecurityHeadersOnEveryResponse(t *testing.T) {
	r, _ := newRouter(t)
	r.GET("/boom", func(*gin.Context) { panic("kaboom") })
	r.POST("/echo", func(c *gin.Context) { c.String(http.StatusOK, "echo") })

	tests := []struct {
		name   string
		method string
		target string
		body   io.Reader
		want   int
	}{
		{"healthz", http.MethodGet, "/healthz", nil, http.StatusOK},
		{"unknown route", http.MethodGet, "/nope", nil, http.StatusNotFound},
		{"wrong method", http.MethodPost, "/healthz", nil, http.StatusNotFound},
		// Gin answers these two with a redirect before any middleware runs
		// unless RedirectTrailingSlash is off.
		{"trailing slash", http.MethodGet, "/healthz/", nil, http.StatusNotFound},
		{"static without slash", http.MethodGet, "/static", nil, http.StatusNotFound},
		{"recovered panic", http.MethodGet, "/boom", nil, http.StatusInternalServerError},
		{"static file", http.MethodGet, "/static/css/base.css", nil, http.StatusOK},
		{"static file not found", http.MethodGet, "/static/css/missing.css", nil, http.StatusNotFound},
		{"body over the cap", http.MethodPost, "/echo", strings.NewReader(strings.Repeat("x", maxBodyBytes+1)), http.StatusRequestEntityTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(r, tt.method, tt.target, tt.body, withOrigin)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d", rec.Code, tt.want)
			}
			checkSecurityHeaders(t, rec.Header())
		})
	}
}

// bodyProbe is a POST/GET handler that reports how reading the body went.
func bodyProbe(c *gin.Context) {
	b, err := io.ReadAll(c.Request.Body)
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		c.String(http.StatusOK, "max-bytes-error limit=%d", tooLarge.Limit)
	case err != nil:
		c.String(http.StatusOK, "other-error %v", err)
	default:
		c.String(http.StatusOK, "read=%d", len(b))
	}
}

func TestLimitBody(t *testing.T) {
	newBodyRouter := func(t *testing.T) *gin.Engine {
		t.Helper()
		r, _ := newRouter(t)
		for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPut} {
			r.Handle(m, "/probe", bodyProbe)
		}
		return r
	}

	t.Run("declared length over the cap is 413 and the handler never runs", func(t *testing.T) {
		r, _ := newRouter(t)
		called := false
		r.POST("/probe", func(*gin.Context) { called = true })

		rec := do(r, http.MethodPost, "/probe", strings.NewReader(strings.Repeat("x", maxBodyBytes+1)), withOrigin)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("status = %d, want 413", rec.Code)
		}
		if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Errorf("Content-Type = %q, want text/plain; charset=utf-8", got)
		}
		if rec.Body.String() != "corpo grande demais" {
			t.Errorf("body = %q, want %q", rec.Body.String(), "corpo grande demais")
		}
		if called {
			t.Error("handler ran for an oversized request")
		}
	})

	t.Run("body of exactly the cap is read in full", func(t *testing.T) {
		rec := do(newBodyRouter(t), http.MethodPost, "/probe", strings.NewReader(strings.Repeat("x", maxBodyBytes)), withOrigin)
		if want := fmt.Sprintf("read=%d", maxBodyBytes); rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("got %d %q, want 200 %q", rec.Code, rec.Body.String(), want)
		}
	})

	t.Run("chunked body over the cap fails the read", func(t *testing.T) {
		rec := do(newBodyRouter(t), http.MethodPost, "/probe", strings.NewReader(strings.Repeat("x", 2*maxBodyBytes)), withOrigin, chunked)
		if want := fmt.Sprintf("max-bytes-error limit=%d", maxBodyBytes); rec.Code != http.StatusOK || rec.Body.String() != want {
			t.Errorf("got %d %q, want 200 %q", rec.Code, rec.Body.String(), want)
		}
	})

	t.Run("chunked body within the cap is read in full", func(t *testing.T) {
		rec := do(newBodyRouter(t), http.MethodPut, "/probe", strings.NewReader(strings.Repeat("x", 100)), withOrigin, chunked)
		if rec.Code != http.StatusOK || rec.Body.String() != "read=100" {
			t.Errorf("got %d %q, want 200 read=100", rec.Code, rec.Body.String())
		}
	})

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		t.Run(method+" is not capped", func(t *testing.T) {
			body := strings.Repeat("x", 2*maxBodyBytes)
			rec := do(newBodyRouter(t), method, "/probe", strings.NewReader(body))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if want := fmt.Sprintf("read=%d", len(body)); method != http.MethodHead && rec.Body.String() != want {
				t.Errorf("body = %q, want %q", rec.Body.String(), want)
			}
		})
	}
}
