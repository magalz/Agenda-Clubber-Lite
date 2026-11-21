package web

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/catalog"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Tests of the option lists the dependent selects swap in: GET /opcoes/cidades and
// GET /opcoes/subestilos (routes_options.go and partials/select-options.html), and
// of the event form, which builds its own city and substyle lists from the same
// code. The expected lists are written from the catalog here, not from the
// builders, so a change to them shows.

const (
	optCityEmpty     = "escolha a cidade"
	optNoState       = "escolha o estado primeiro"
	optSubstyleEmpty = "sem subestilo"
)

var optLineRe = regexp.MustCompile(`<option [^>]*>[^<]*</option>`)

// optCheck fails unless got is want, naming the first option that differs.
func optCheck(t *testing.T, what string, got, want []evOption) {
	t.Helper()
	if slices.Equal(got, want) {
		return
	}
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	var g, w any = "(none)", "(none)"
	if i < len(got) {
		g = got[i]
	}
	if i < len(want) {
		w = want[i]
	}
	t.Errorf("%s: %d options, want %d; first difference at %d: got %v, want %v", what, len(got), len(want), i, g, w)
}

// optList is the options a list of values makes: the empty option first, then each
// value as its own label, the one equal to selected marked, and then any extra.
func optList(empty string, values []string, selected string, extra ...string) []evOption {
	out := []evOption{{Label: empty}}
	for _, v := range append(slices.Clone(values), extra...) {
		out = append(out, evOption{Value: v, Label: v, Selected: v == selected})
	}
	return out
}

func TestOptionsCities(t *testing.T) {
	e := newEvWorld(t)
	ce, pe := catalog.Cities("CE"), catalog.Cities("PE")
	if len(ce) != 184 || !slices.Contains(ce, "Fortaleza") {
		t.Fatalf("the catalog has %d cities in CE (Fortaleza among them: %v); the tests expect 184", len(ce), slices.Contains(ce, "Fortaleza"))
	}
	none := []evOption{{Label: optNoState}}

	for _, tc := range []struct {
		name, query string
		want        []evOption
	}{
		{"a state", "?uf=CE", optList(optCityEmpty, ce, "")},
		{"a state and its city", "?uf=CE&selecionada=Fortaleza", optList(optCityEmpty, ce, "Fortaleza")},
		{"the first city", "?uf=CE&selecionada=" + url.QueryEscape(ce[0]), optList(optCityEmpty, ce, ce[0])},
		{"the last city", "?uf=CE&selecionada=" + url.QueryEscape(ce[len(ce)-1]), optList(optCityEmpty, ce, ce[len(ce)-1])},
		{"another state", "?uf=PE&selecionada=Recife", optList(optCityEmpty, pe, "Recife")},
		{"a city of another state", "?uf=CE&selecionada=Recife", optList(optCityEmpty, ce, "")},
		{"a city that does not exist", "?uf=CE&selecionada=Atlantis", optList(optCityEmpty, ce, "")},
		{"a city in another case", "?uf=CE&selecionada=fortaleza", optList(optCityEmpty, ce, "")},
		{"an empty city", "?uf=CE&selecionada=", optList(optCityEmpty, ce, "")},
		{"an unknown state", "?uf=XX", none},
		{"an unknown state with a city", "?uf=XX&selecionada=Fortaleza", none},
		{"a state in lower case", "?uf=ce", none},
		{"an empty state", "?uf=", none},
		{"no state", "", none},
		{"no state with a city", "?selecionada=Fortaleza", none},
	} {
		t.Run(tc.name, func(t *testing.T) {
			optCheck(t, "GET /opcoes/cidades"+tc.query, evOptions(e.body(t, "/opcoes/cidades"+tc.query)), tc.want)
		})
	}
}

func TestOptionsSubstyles(t *testing.T) {
	e := newEvWorld(t)
	techno, house := catalog.Substyles("techno"), catalog.Substyles("house")
	if !slices.Contains(techno, "acid techno") || !slices.Contains(house, "jackin' house") {
		t.Fatal("the catalog lacks the substyles the tests use")
	}
	if pop := catalog.Substyles("pop"); pop == nil || len(pop) != 0 {
		t.Fatalf("catalog.Substyles(pop) = %#v; the tests expect a style with none", pop)
	}
	none := []evOption{{Label: optSubstyleEmpty}}

	for _, tc := range []struct {
		name, query string
		want        []evOption
	}{
		{"a style", "?estilo=techno", optList(optSubstyleEmpty, techno, "", "outros")},
		{"a style and its substyle", "?estilo=techno&selecionada=acid%20techno", optList(optSubstyleEmpty, techno, "acid techno", "outros")},
		{"outros as the substyle", "?estilo=techno&selecionada=outros", optList(optSubstyleEmpty, techno, "outros", "outros")},
		{"a substyle with an apostrophe", "?estilo=house&selecionada=" + url.QueryEscape("jackin' house"), optList(optSubstyleEmpty, house, "jackin' house", "outros")},
		{"a substyle of another style", "?estilo=techno&selecionada=deep%20house", optList(optSubstyleEmpty, techno, "", "outros")},
		{"a substyle that does not exist", "?estilo=techno&selecionada=nada", optList(optSubstyleEmpty, techno, "", "outros")},
		{"a substyle in another case", "?estilo=techno&selecionada=Acid%20Techno", optList(optSubstyleEmpty, techno, "", "outros")},
		{"a style with no substyles", "?estilo=pop", optList(optSubstyleEmpty, nil, "", "outros")},
		{"a style with no substyles and outros", "?estilo=pop&selecionada=outros", optList(optSubstyleEmpty, nil, "outros", "outros")},
		{"the style outros", "?estilo=outros", none},
		{"the style outros with a substyle", "?estilo=outros&selecionada=outros", none},
		{"an unknown style", "?estilo=nada&selecionada=acid%20techno", none},
		{"a style in another case", "?estilo=Techno", none},
		{"an empty style", "?estilo=", none},
		{"no style", "", none},
		{"no style with a substyle", "?selecionada=acid%20techno", none},
	} {
		t.Run(tc.name, func(t *testing.T) {
			optCheck(t, "GET /opcoes/subestilos"+tc.query, evOptions(e.body(t, "/opcoes/subestilos"+tc.query)), tc.want)
		})
	}
}

// The fragment is only <option> lines: no document, no script, nothing cached.
func TestOptionsFragment(t *testing.T) {
	e := newEvWorld(t)
	for _, target := range []string{
		"/opcoes/cidades?uf=CE&selecionada=Fortaleza", "/opcoes/cidades", "/opcoes/cidades?uf=XX",
		"/opcoes/subestilos?estilo=techno&selecionada=acid%20techno", "/opcoes/subestilos?estilo=pop", "/opcoes/subestilos",
	} {
		for _, mod := range [][]func(*http.Request){nil, {hx}} {
			rec := e.get(e.Outsider, target, mod...)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s: %d %.200q; want 200", target, rec.Code, rec.Body)
			}
			if got := rec.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("GET %s: Cache-Control = %q, want no-store", target, got)
			}
			if got := rec.Header().Get("Content-Type"); got != htmlContentType {
				t.Errorf("GET %s: Content-Type = %q, want %q", target, got, htmlContentType)
			}
			body := rec.Body.String()
			for _, bad := range []string{"<html", "<head", "<body", "<main", "<script", "<form", "<select", "x-data", "feito_por", "onclick=", "onload="} {
				if strings.Contains(strings.ToLower(body), bad) {
					t.Errorf("GET %s: the fragment contains %q", target, bad)
				}
			}
			lines := strings.Split(strings.TrimSpace(body), "\n")
			if len(lines) != len(evOptions(body)) {
				t.Errorf("GET %s: %d lines but %d options; want one option on each line", target, len(lines), len(evOptions(body)))
			}
			for _, line := range lines {
				if !optLineRe.MatchString(line) || !strings.HasPrefix(line, "<option ") || !strings.HasSuffix(line, "</option>") {
					t.Errorf("GET %s: %q is not an <option> line", target, line)
					break
				}
			}
		}
	}
}

// What the person typed is only compared with the catalog, never printed; what the
// partial prints is escaped.
func TestOptionsEscaping(t *testing.T) {
	e := newEvWorld(t)
	evil := `"><script>alert(1)</script>&'`
	q := url.QueryEscape(evil)
	for _, target := range []string{
		"/opcoes/cidades?uf=" + q + "&selecionada=" + q, "/opcoes/cidades?uf=CE&selecionada=" + q,
		"/opcoes/subestilos?estilo=" + q + "&selecionada=" + q, "/opcoes/subestilos?estilo=techno&selecionada=" + q,
	} {
		body := e.body(t, target)
		for _, bad := range []string{"<script", "alert(1)", evil, html.EscapeString(evil)} {
			if strings.Contains(body, bad) {
				t.Errorf("GET %s: the fragment contains %q", target, bad)
			}
		}
		for _, o := range evOptions(body) {
			if o.Selected {
				t.Errorf("GET %s: the option %q is selected", target, o.Value)
			}
		}
	}

	// A value and a label that look like markup come out as text, and read back as they went in.
	opts := []calendarOption{
		{Value: evil, Label: `<b>a & b</b>`, Selected: true},
		{Value: "jackin' house", Label: "jackin' house"},
	}
	body := renderPartial(t, e.App, "select-options", opts)
	for _, bad := range []string{"<script", "<b>", "</b>", "alert(1)</script>"} {
		if strings.Contains(body, bad) {
			t.Errorf("the partial printed %q unescaped: %q", bad, body)
		}
	}
	want := []evOption{{Value: evil, Label: `<b>a & b</b>`, Selected: true}, {Value: "jackin' house", Label: "jackin' house"}}
	optCheck(t, "the partial", evOptions(body), want)
}

// The routes are for signed-in people only, like every page.
func TestOptionsNeedASession(t *testing.T) {
	e := newEvWorld(t)
	for _, target := range []string{"/opcoes/cidades?uf=CE", "/opcoes/subestilos?estilo=techno"} {
		rec := serve(e.r, http.MethodGet, target)
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/entrar") {
			t.Errorf("GET %s: %d Location %q, want 303 to /entrar", target, rec.Code, rec.Header().Get("Location"))
		}
		if strings.Contains(rec.Body.String(), "<option") {
			t.Errorf("GET %s: the refusal carries options", target)
		}
		rec = serve(e.r, http.MethodGet, target, hx)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("HX-Redirect") == "" {
			t.Errorf("GET %s (htmx): %d HX-Redirect %q, want 401 and a redirect to the login", target, rec.Code, rec.Header().Get("HX-Redirect"))
		}
		if strings.Contains(rec.Body.String(), "<option") {
			t.Errorf("GET %s (htmx): the refusal carries options", target)
		}
	}
}

// optLines returns the <option> lines of a body, as written.
func optLines(body string) []string { return optLineRe.FindAllString(body, -1) }

// The form renders its lists with the builders and the partial the endpoints use, so
// the option lines of its selects are, character for character, those of the endpoints
// for the state and the style it holds.
func TestOptionsFormListsAreTheServedLists(t *testing.T) {
	e := newEvWorld(t)
	edit := func(mutate func(*domain.Event)) string {
		ev := evEvent(t, e.Store, e.Alfa.ID, e.Owner.ID, mutate)
		rec := e.get(e.Owner, "/eventos/"+ev.ID+"/editar")
		if rec.Code != http.StatusOK {
			t.Fatalf("edit page: %d %.200q", rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	refresh := func(v url.Values) string {
		v.Set("acao", "atualizar")
		rec := e.send(e.Owner, "/eventos/novo", v)
		if rec.Code != http.StatusOK {
			t.Fatalf("refresh of the new form: %d %.200q", rec.Code, rec.Body)
		}
		return rec.Body.String()
	}
	blank := e.get(e.Owner, "/eventos/novo")
	if blank.Code != http.StatusOK {
		t.Fatalf("new form: %d %.200q", blank.Code, blank.Body)
	}

	for _, tc := range []struct {
		name, body                   string
		state, city, style, substyle string
	}{
		{"the empty form", blank.Body.String(), "", "", "", ""},
		{"the edit page of an event in CE and techno", edit(func(ev *domain.Event) { ev.Substyle = "acid techno" }), "CE", "Fortaleza", "techno", "acid techno"},
		{"the edit page of an event in PE and house", edit(func(ev *domain.Event) {
			ev.State, ev.City, ev.Style, ev.Substyle = "PE", "Recife", "house", "jackin' house"
		}), "PE", "Recife", "house", "jackin' house"},
		{"the edit page of an event in a style with no substyles", edit(func(ev *domain.Event) { ev.Style, ev.Substyle = "pop", "outros" }), "CE", "Fortaleza", "pop", "outros"},
		{"the edit page of an event in the style outros", edit(func(ev *domain.Event) { ev.Style, ev.StyleOther, ev.Substyle = "outros", "forró", "" }), "CE", "Fortaleza", "outros", ""},
		{"a refreshed new form", refresh(url.Values{"collective_id": {e.Alfa.ID}, "state": {"CE"}, "city": {"Sobral"}, "style": {"techno"}, "substyle": {"deep techno"}}), "CE", "Sobral", "techno", "deep techno"},
		{"a refreshed new form with a city of another state", refresh(url.Values{"collective_id": {e.Alfa.ID}, "state": {"CE"}, "city": {"Recife"}, "style": {"house"}, "substyle": {"deep techno"}}), "CE", "Recife", "house", "deep techno"},
		{"a refreshed new form with an unknown state and style", refresh(url.Values{"collective_id": {e.Alfa.ID}, "state": {"XX"}, "city": {"Fortaleza"}, "style": {"nada"}, "substyle": {"acid techno"}}), "XX", "Fortaleza", "nada", "acid techno"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cities := "/opcoes/cidades?uf=" + url.QueryEscape(tc.state) + "&selecionada=" + url.QueryEscape(tc.city)
			substyles := "/opcoes/subestilos?estilo=" + url.QueryEscape(tc.style) + "&selecionada=" + url.QueryEscape(tc.substyle)
			for _, f := range []struct{ field, target string }{{"city", cities}, {"substyle", substyles}} {
				got := optLines(evfSelect(t, tc.body, f.field))
				want := optLines(e.body(t, f.target))
				if len(want) == 0 || !slices.Equal(got, want) {
					t.Errorf("the %s select has %d option lines, GET %s has %d; they must be the same lines.\nform: %.300q\nlist: %.300q", f.field, len(got), f.target, len(want), got, want)
				}
			}
		})
	}
}
