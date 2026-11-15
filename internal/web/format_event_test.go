package web

import (
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

func TestFmtEventType(t *testing.T) {
	tests := []struct {
		typ   domain.EventType
		other string
		want  string
	}{
		{domain.EventTypeFesta, "", "Festa"},
		{domain.EventTypeRave, "", "Rave"},
		{domain.EventTypeFeira, "ignored when the type is not outros", "Feira"},
		{domain.EventTypeOutros, "Sarau", "Sarau"},
		{domain.EventTypeOutros, "", "Outros"},
	}
	for _, tt := range tests {
		if got := fmtEventType(tt.typ, tt.other); got != tt.want {
			t.Errorf("fmtEventType(%q, %q) = %q, want %q", tt.typ, tt.other, got, tt.want)
		}
	}
}

func TestFmtEventStyle(t *testing.T) {
	tests := []struct {
		style, substyle, other string
		want                   string
	}{
		{"techno", "", "", "techno"},
		{"techno", "acid techno", "", "techno · acid techno"},
		{"techno", domain.StyleOther, "", "techno · outros"},
		{"house", "", "ignored when the style is not outros", "house"},
		{domain.StyleOther, "", "brega funk", "brega funk"},
		{domain.StyleOther, "", "", "outros"},
	}
	for _, tt := range tests {
		if got := fmtEventStyle(tt.style, tt.substyle, tt.other); got != tt.want {
			t.Errorf("fmtEventStyle(%q, %q, %q) = %q, want %q", tt.style, tt.substyle, tt.other, got, tt.want)
		}
	}
}

// TestEventFormatFuncsAreRegistered renders eventType and eventStyle through the
// real NewRenderer path, so a typo in a registered name fails here.
func TestEventFormatFuncsAreRegistered(t *testing.T) {
	data := domain.Event{Type: domain.EventTypeOutros, TypeOther: "Sarau", Style: "techno", Substyle: "acid techno"}
	fsys := withFiles(fstest.MapFS{}, map[string]string{
		"layouts/base.html":     `{{define "base"}}{{template "content" .}}{{end}}`,
		"pages/base/event.html": `{{define "content"}}{{eventType .Data.Type .Data.TypeOther}}|{{eventStyle .Data.Style .Data.Substyle .Data.StyleOther}}{{end}}`,
	})
	r, _ := newTestRenderer(t, fsys)
	page := renderPage(t, r, "base/event", PageData{Data: data})
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d: %s", page.Code, page.Body)
	}
	if got, want := page.Body.String(), "Sarau|techno · acid techno"; got != want {
		t.Errorf("page = %q, want %q", got, want)
	}
}
