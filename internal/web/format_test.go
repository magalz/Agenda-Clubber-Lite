package web

import (
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// utc parses an RFC 3339 instant and returns it in UTC, as the store does.
func utc(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v.UTC()
}

func TestFmtDate(t *testing.T) {
	loc := fortaleza(t)
	tests := []struct{ name, at, want string }{
		{"same day", "2026-11-14T22:00:00Z", "14/11/2026"},
		{"still the previous day in Fortaleza", "2026-11-15T02:30:00Z", "14/11/2026"},
		{"midnight in Fortaleza starts the new day", "2026-11-15T03:00:00Z", "15/11/2026"},
		{"zero padded", "2026-03-05T12:00:00Z", "05/03/2026"},
		{"year boundary", "2027-01-01T02:59:00Z", "31/12/2026"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fmtDate(utc(t, tt.at), loc); got != tt.want {
				t.Errorf("fmtDate(%s) = %q, want %q", tt.at, got, tt.want)
			}
		})
	}
}

func TestFmtIgnoresTheZoneOfTheInput(t *testing.T) {
	loc := fortaleza(t)
	tokyo := time.FixedZone("JST", 9*60*60)
	// 2026-11-15T10:30 in Tokyo is 2026-11-15T01:30Z, 22:30 on the 14th in Fortaleza.
	in := time.Date(2026, 11, 15, 10, 30, 0, 0, tokyo)
	if got := fmtDate(in, loc); got != "14/11/2026" {
		t.Errorf("fmtDate = %q, want 14/11/2026", got)
	}
	if got := fmtClock(in, loc); got != "22:30" {
		t.Errorf("fmtClock = %q, want 22:30", got)
	}
	if got := fmtUntil(in, 120, loc); got != "até 00:30 (+1)" {
		t.Errorf("fmtUntil = %q, want %q", got, "até 00:30 (+1)")
	}
}

func TestFmtDateLong(t *testing.T) {
	loc := fortaleza(t)
	if got := fmtDateLong(utc(t, "2026-11-15T02:30:00Z"), loc); got != "sábado, 14/11/2026" {
		t.Errorf("fmtDateLong = %q, want %q (the instant is still Saturday in Fortaleza)", got, "sábado, 14/11/2026")
	}

	// 2026-11-15 is a Sunday.
	weekdays := []string{
		"domingo", "segunda-feira", "terça-feira", "quarta-feira", "quinta-feira", "sexta-feira", "sábado",
	}
	sunday := utc(t, "2026-11-15T15:00:00Z")
	for i, name := range weekdays {
		day := sunday.AddDate(0, 0, i)
		want := name + ", " + day.Format("02/01/2006")
		if got := fmtDateLong(day, loc); got != want {
			t.Errorf("fmtDateLong(%s) = %q, want %q", day.Format(time.RFC3339), got, want)
		}
	}
}

func TestFmtMonthYear(t *testing.T) {
	loc := fortaleza(t)
	tests := []struct{ name, at, want string }{
		{"month boundary, still November in Fortaleza", "2026-12-01T01:00:00Z", "NOVEMBRO 2026"},
		{"month boundary, December in Fortaleza", "2026-12-01T03:00:00Z", "DEZEMBRO 2026"},
		{"year boundary, still 2026 in Fortaleza", "2027-01-01T02:59:00Z", "DEZEMBRO 2026"},
		{"year boundary, 2027 in Fortaleza", "2027-01-01T03:00:00Z", "JANEIRO 2027"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fmtMonthYear(utc(t, tt.at), loc); got != tt.want {
				t.Errorf("fmtMonthYear(%s) = %q, want %q", tt.at, got, tt.want)
			}
		})
	}

	months := []string{
		"JANEIRO", "FEVEREIRO", "MARÇO", "ABRIL", "MAIO", "JUNHO",
		"JULHO", "AGOSTO", "SETEMBRO", "OUTUBRO", "NOVEMBRO", "DEZEMBRO",
	}
	for i, name := range months {
		mid := time.Date(2026, time.Month(i+1), 15, 15, 0, 0, 0, time.UTC)
		if got, want := fmtMonthYear(mid, loc), name+" 2026"; got != want {
			t.Errorf("fmtMonthYear(%s) = %q, want %q", mid.Format(time.RFC3339), got, want)
		}
	}
}

func TestFmtClock(t *testing.T) {
	loc := fortaleza(t)
	tests := []struct{ at, want string }{
		{"2026-11-15T01:00:00Z", "22:00"},
		{"2026-11-15T02:30:00Z", "23:30"},
		{"2026-11-15T03:00:00Z", "00:00"},
		{"2026-11-15T01:05:00Z", "22:05"},
		{"2026-11-15T12:00:00Z", "09:00"},
	}
	for _, tt := range tests {
		if got := fmtClock(utc(t, tt.at), loc); got != tt.want {
			t.Errorf("fmtClock(%s) = %q, want %q", tt.at, got, tt.want)
		}
	}
}

func TestFmtUntil(t *testing.T) {
	loc := fortaleza(t)
	tests := []struct {
		name string
		at   string // UTC
		min  int
		want string
	}{
		{"22:00 plus 8h ends the next morning", "2026-11-15T01:00:00Z", 480, "até 06:00 (+1)"},
		{"18:00 plus 5h30 ends the same night", "2026-11-14T21:00:00Z", 330, "até 23:30"},
		{"ends one minute before midnight", "2026-11-15T01:00:00Z", 119, "até 23:59"},
		{"ends exactly at midnight, the next day", "2026-11-15T01:00:00Z", 120, "até 00:00 (+1)"},
		{"local midnight plus the longest duration", "2026-11-14T03:00:00Z", 1439, "até 23:59"},
		{"23:59 plus the longest duration", "2026-11-15T02:59:00Z", 1439, "até 23:58 (+1)"},
		{"the local day changes while the UTC day does not", "2026-11-15T02:30:00Z", 60, "até 00:30 (+1)"},
		{"the UTC day changes while the local day does not", "2026-11-14T20:00:00Z", 240, "até 21:00"},
		{"the shortest duration", "2026-11-14T21:00:00Z", 15, "até 18:15"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fmtUntil(utc(t, tt.at), tt.min, loc); got != tt.want {
				t.Errorf("fmtUntil(%s, %d) = %q, want %q", tt.at, tt.min, got, tt.want)
			}
		})
	}
}

func TestFmtDuration(t *testing.T) {
	tests := []struct {
		min  int
		want string
	}{
		{15, "15min"},
		{45, "45min"},
		{59, "59min"},
		{60, "1h"},
		{65, "1h05"},
		{120, "2h"},
		{360, "6h"},
		{390, "6h30"},
		{600, "10h"},
		{1439, "23h59"},
	}
	for _, tt := range tests {
		if got := fmtDuration(tt.min); got != tt.want {
			t.Errorf("fmtDuration(%d) = %q, want %q", tt.min, got, tt.want)
		}
	}
}

func TestFmtPrice(t *testing.T) {
	tests := []struct {
		free  bool
		cents int64
		want  string
	}{
		{true, 0, "Free"},
		{true, 2500, "Free"}, // free wins; the domain keeps the price at 0 then
		{false, 1, "R$ 0,01"},
		{false, 50, "R$ 0,50"},
		{false, 100, "R$ 1,00"},
		{false, 2500, "R$ 25,00"},
		{false, 99900, "R$ 999,00"},
		{false, 100000, "R$ 1.000,00"},
		{false, 123456, "R$ 1.234,56"},
		{false, 9999999, "R$ 99.999,99"},
		{false, 100000000, "R$ 1.000.000,00"},
	}
	for _, tt := range tests {
		if got := fmtPrice(tt.free, tt.cents); got != tt.want {
			t.Errorf("fmtPrice(%v, %d) = %q, want %q", tt.free, tt.cents, got, tt.want)
		}
	}
}

// formatCall is the body that uses every registered format function; v is
// what comes before ".Start" and the other fields: nothing in a partial, whose
// dot is the data, and ".Data" in a page.
func formatCall(v string) string {
	body := `{{date $v.Start}}|{{dateLong $v.Start}}|{{monthYear $v.Start}}|{{clock $v.Start}}|` +
		`{{until $v.Late 480}}|{{duration $v.Minutes}}|{{price false $v.Cents}}|{{price true $v.Cents}}`
	return strings.ReplaceAll(body, "$v", v)
}

// TestFormatFuncsAreRegistered renders every function through the real
// NewRenderer path, in a page and in a partial, so a typo in a registered
// name fails here and not on the first request.
func TestFormatFuncsAreRegistered(t *testing.T) {
	data := struct {
		Start, Late time.Time
		Minutes     int
		Cents       int64
	}{
		Start:   utc(t, "2026-11-15T02:30:00Z"), // 23:30 on the 14th in Fortaleza
		Late:    utc(t, "2026-11-15T01:00:00Z"), // 22:00 on the 14th in Fortaleza
		Minutes: 390,
		Cents:   123456,
	}
	fsys := withFiles(fstest.MapFS{}, map[string]string{
		"layouts/base.html":      `{{define "base"}}{{template "content" .}}{{end}}`,
		"pages/base/format.html": `{{define "content"}}` + formatCall(".Data") + `{{end}}`,
		"partials/format.html":   `{{define "format"}}` + formatCall("") + `{{end}}`,
	})
	r, _ := newTestRenderer(t, fsys)

	page := renderPage(t, r, "base/format", PageData{Data: data})
	if page.Code != http.StatusOK {
		t.Fatalf("page status = %d: %s", page.Code, page.Body)
	}
	c, partial := newCtx()
	r.Partial(c, http.StatusOK, "format", data)
	if partial.Code != http.StatusOK {
		t.Fatalf("partial status = %d: %s", partial.Code, partial.Body)
	}

	// html/template writes "+" as "&#43;" in text, so "(+1)" reaches the
	// browser as "(&#43;1)", which it shows as "(+1)".
	const want = "14/11/2026|sábado, 14/11/2026|NOVEMBRO 2026|23:30|" +
		"até 06:00 (&#43;1)|6h30|R$ 1.234,56|Free"
	for name, got := range map[string]string{"page": page.Body.String(), "partial": partial.Body.String()} {
		if got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
