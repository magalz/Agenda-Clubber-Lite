package web

import (
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/magalz/agenda-clubber-lite/internal/catalog"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

// The dynamic event form (T4.18) is the T4.12 form with the eventDynamic Alpine
// component of app.js wrapped around it and the side panel of "Analisar Data"
// beside it. The Go tests cannot run JavaScript, so they pin the markup the
// script drives, the contract between that markup and app.js (names a member of
// the right scope, the URLs and parameters the server routes read) and the routes
// the script calls. What the script does with them is checked in a browser.

// dynMembers are the names the markup gives the wrapper's scope.
var dynMembers = []string{
	"typeOtherShown", "styleOtherShown", "substyleOff", "refreshShown",
	"analyseDisabled", "analyse", "panelIdle", "panelStale", "onFieldChange", "onFieldInput",
}

func dynRead(t *testing.T, name string) string {
	t.Helper()
	b, err := fs.ReadFile(webfs.Static, name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var (
	dynDataRe   = regexp.MustCompile(`Alpine\.data\('([A-Za-z]+)'`)
	dynMemberRe = regexp.MustCompile(`(?m)^ {8}(?:get |async )?([A-Za-z_$][\w$]*)\s*[(:]`)
	dynTagRe    = regexp.MustCompile(`<(/?)(div|aside)\b`)
)

// dynScopes lists the members of each Alpine.data component of app.js: the lines
// of the returned object, which the components indent by eight spaces.
func dynScopes(js string) map[string][]string {
	at := dynDataRe.FindAllStringSubmatchIndex(js, -1)
	out := map[string][]string{}
	for i, m := range at {
		end := len(js)
		if i+1 < len(at) {
			end = at[i+1][0]
		}
		name := js[m[2]:m[3]]
		for _, member := range dynMemberRe.FindAllStringSubmatch(js[m[0]:end], -1) {
			out[name] = append(out[name], member[1])
		}
	}
	return out
}

// dynClose is the index just past the </div> or </aside> that closes the element
// opened at start (the index of its "<"), counting the div and aside tags in
// between. The templates write well-formed markup.
func dynClose(t *testing.T, body string, start int) int {
	t.Helper()
	depth := 0
	for _, m := range dynTagRe.FindAllStringSubmatchIndex(body[start:], -1) {
		if body[start+m[2]:start+m[3]] == "" {
			depth++
			continue
		}
		if depth--; depth == 0 {
			return start + m[1] + strings.Index(body[start+m[1]:], ">") + 1
		}
	}
	t.Fatalf("the element at %d is never closed", start)
	return 0
}

// dynOpen is the index of the "<" of the tag that holds the marker (an attribute).
func dynOpen(t *testing.T, body, marker string) int {
	t.Helper()
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no %q in the page", marker)
	}
	return strings.LastIndex(body[:i+1], "<")
}

func TestDynNoInlineScriptOrStyle(t *testing.T) {
	ewRender(t, newEvfEnv(t)).each(t, func(t *testing.T, body string) {
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("the CSP would block: %v", v)
		}
	})
}

// The component wraps the form and the panel, so the delegated listeners hear the
// fields and the panel is in its scope; the other widgets sit inside it.
func TestDynWrapsTheFormAndThePanel(t *testing.T) {
	ewRender(t, newEvfEnv(t)).each(t, func(t *testing.T, body string) {
		start := dynOpen(t, body, `x-data="eventDynamic"`)
		end := dynClose(t, body, start)
		for _, inside := range []string{`<form class="event-form`, `x-data="datePicker"`, `<aside class="event-aside"`} {
			if i := strings.Index(body, inside); i < start || i > end {
				t.Errorf("%s is not inside the eventDynamic wrapper (%d..%d), at %d", inside, start, end, i)
			}
		}
		form := strings.Index(body, `<form class="event-form`)
		if strings.Index(body[form:], "</form>")+form > strings.Index(body, `<aside class="event-aside"`) {
			t.Errorf("the panel comes before the end of the form: on a phone it must come below the fields")
		}
	})
}

// What the script adds is hidden until it runs; what it hides when it runs shows
// without it. The hidden attribute loses to .btn and .field, and x-cloak would
// hide the field for good without the script: those use x-show.
func TestDynMarkup(t *testing.T) {
	ewRender(t, newEvfEnv(t)).each(t, func(t *testing.T, body string) {
		// The cascades: the lists are refilled by the script, so the refresh button and its hint go.
		refresh := tagAttrs(t, body, `<button\b([^>]*)>\s*\[↻\] ATUALIZAR LISTAS\s*</button>`)
		wantAll(t, "the refresh button", refresh, `x-show="refreshShown"`)
		hint := tagAttrs(t, body, `<p class="hint" id="dica-atualizar"([^>]*)>depois de mudar o estado`)
		wantAll(t, "the refresh hint", hint, `x-show="refreshShown"`)
		for _, shown := range []string{refresh, hint} {
			if ewCloaked.MatchString(" " + shown + ">") {
				t.Errorf("x-cloak hides the refresh button or its hint for good without JavaScript: %s", shown)
			}
		}

		// The Outros fields: their .field shows or hides, and the substyle goes off with the style.
		for _, tc := range []struct{ field, member string }{{"type_other", "typeOtherShown"}, {"style_other", "styleOtherShown"}} {
			wrapper := tagAttrs(t, body, `(<div class="field"[^>]*>)\s*<label class="label" for="`+tc.field+`">`)
			wantAll(t, tc.field+"'s field", wrapper, `x-show="`+tc.member+`"`)
			if ewCloaked.MatchString(wrapper) {
				t.Errorf("%s's field is hidden for good without JavaScript: %s", tc.field, wrapper)
			}
		}
		wantAll(t, "the substyle select", tagAttrs(t, body, `(<select class="select" id="substyle"[^>]*>)`), `:disabled="substyleOff"`)

		// Analisar Data: JS only, so its row is x-cloak; the button is no field and sits outside the date's popup.
		row := tagAttrs(t, body, `(<div class="event-form__analyse"[^>]*>)`)
		if !ewCloaked.MatchString(row) {
			t.Errorf("the Analisar Data row shows without JavaScript: no x-cloak in %s", row)
		}
		button := tagAttrs(t, body, `<button\b([^>]*)>\s*\[→\] ANALISAR DATA\s*</button>`)
		wantAll(t, "the Analisar Data button", button, `type="button"`, ` disabled`, `:disabled="analyseDisabled"`, `@click="analyse"`)
		if strings.Contains(" "+button, " name=") {
			t.Errorf("the Analisar Data button posts a name: %s", button)
		}
		at := strings.Index(body, `[→] ANALISAR DATA`)
		pickerStart := dynOpen(t, body, `x-data="datePicker"`)
		if pickerEnd := dynClose(t, body, pickerStart); at < pickerEnd {
			t.Errorf("the Analisar Data button is inside the date picker's field, whose popup hangs from it")
		}
		if i := strings.Index(body, `x-data="durationSpinner"`); at > i {
			t.Errorf("the Analisar Data button comes after the duration: it belongs to the date row")
		}

		// The panel: a labelled live region, hidden until the script runs, with the heading and a stable target.
		start := dynOpen(t, body, `<aside class="event-aside"`)
		aside := body[start:dynClose(t, body, start)]
		open := aside[:strings.Index(aside, ">")+1]
		wantAll(t, "the panel", open, `aria-live="polite"`, `aria-labelledby="analise-titulo"`)
		if !ewCloaked.MatchString(open) {
			t.Errorf("the panel shows without JavaScript: no x-cloak in %s", open)
		}
		dynHas(t, "the panel", aside, `id="analise-titulo"`, `Na mesma data`, `id="analise-resultado"`,
			`x-show="panelIdle"`, `x-show="panelStale"`)
	})
}

// A closing quote must be followed by a space, ">" or "/" before the next attribute.
// Browsers recover from a glued pair (x-data="a"@keydown="b"), but the attribute
// check of TestEwAlpineAttributesNameRegisteredMembers needs the space in front of
// an Alpine attribute, so a glued one would escape it.
var (
	dynStartTagRe = regexp.MustCompile(`<[a-zA-Z][^<>]*>`)
	dynGluedRe    = regexp.MustCompile(`="[^"]*"[^\s>/]`)
)

func TestDynAttributesAreSeparated(t *testing.T) {
	ewRender(t, newEvfEnv(t)).each(t, func(t *testing.T, body string) {
		for _, tag := range dynStartTagRe.FindAllString(body, -1) {
			if glued := dynGluedRe.FindString(tag); glued != "" {
				t.Errorf("no space between two attributes (%q) in %s", glued, tag)
			}
		}
	})
}

// A 422 re-render shows the fields that have a value or an error, so the page
// the server sends back carries the same hooks and the values the script reads.
func TestDynRefusedFormKeepsTheHooks(t *testing.T) {
	e := newEvfEnv(t)
	v := evfCreateForm(e.Alfa.ID)
	v.Set("type", "outros")
	v.Set("style", "outros")
	body := evfBody(t, "refused form", e.send(e.Owner, "/eventos/novo", v), http.StatusUnprocessableEntity)
	dynHas(t, "the refused form", body, `x-data="eventDynamic"`, `x-show="typeOtherShown"`, `x-show="styleOtherShown"`, `@click="analyse"`)
	for _, field := range []string{"type_other", "style_other"} {
		wantAll(t, field, tagAttrs(t, body, `(<input [^>]*id="`+field+`"[^>]*>)`), `aria-invalid="true"`)
	}
}

// Every name the markup gives the component is one of its members, and none of
// them is also a member of a component it shares a scope with (the shell, the
// date picker, the spinners, the price): the scopes merge and the inner one wins,
// so a clash passes the member check of TestEwAlpineAttributesNameRegisteredMembers
// and fails only in the browser.
func TestDynComponentMembers(t *testing.T) {
	scopes := dynScopes(dynRead(t, "static/js/app.js"))
	mine, ok := scopes["eventDynamic"]
	if !ok {
		t.Fatal("app.js does not register Alpine.data('eventDynamic')")
	}
	for _, name := range dynMembers {
		if !slices.Contains(mine, name) {
			t.Errorf("eventDynamic has no member %q, which the markup names", name)
		}
	}
	for other, members := range scopes {
		if other == "eventDynamic" {
			continue
		}
		for _, name := range mine {
			// Alpine calls init and destroy on each component's own object and no attribute names them: they cannot shadow.
			if name == "init" || name == "destroy" {
				continue
			}
			if slices.Contains(members, name) {
				t.Errorf("eventDynamic and %s both define %q: the one nested in the other would shadow it", other, name)
			}
		}
	}
}

// The script builds the URLs of the option and day routes, which read uf, estilo,
// selecionada, data, destaque_uf and destaque_cidade (not the names of the
// selects), and draws the panel as the brief says.
func TestDynScriptTalksToTheRoutes(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	for _, want := range []string{
		"'/opcoes/cidades'", "'/opcoes/subestilos'", "'/eventos/dia'",
		"'uf'", "'estilo'", "'selecionada'", "'data'", "'destaque_uf'", "'destaque_cidade'",
		"URLSearchParams",
		"'htmx:beforeSwap'", "422", "analise-resultado", // a refused date is swapped into the panel only
		"'animate-fade-up'", "setProperty('--i'", // the stagger goes through the CSSOM: no style attribute under the CSP
	} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js has no %s", want)
		}
	}
}

// The classes the markup adds are defined in components.css.
func TestDynStylesheetDefinesTheClasses(t *testing.T) {
	css := dynRead(t, "static/css/components.css")
	for _, class := range []string{"event-layout", "event-aside", "event-aside__title", "event-aside__stale", "event-form__analyse"} {
		if !strings.Contains(css, "\n."+class+" {") && !strings.Contains(css, "\n  ."+class+" {") {
			t.Errorf("components.css has no .%s rule", class)
		}
	}
}

// The routes the script calls answer a signed-in user with the options and the
// day list it swaps in, and a date the server refuses is a 422 with the message.
func TestDynRoutesAnswerTheScript(t *testing.T) {
	e := newEvfEnv(t)
	sub := catalog.Substyles("techno")
	if len(sub) == 0 {
		t.Fatal("the techno style has no substyles in the catalog")
	}

	for _, tc := range []struct {
		name   string
		target string
		want   int
		in     []string
	}{
		{"the cities of a state keep a valid choice", "/opcoes/cidades?" + url.Values{"uf": {"CE"}, "selecionada": {"Fortaleza"}}.Encode(), http.StatusOK,
			[]string{`<option value="Fortaleza" selected>`}},
		{"no state, no cities", "/opcoes/cidades?" + url.Values{"uf": {""}, "selecionada": {"Fortaleza"}}.Encode(), http.StatusOK,
			[]string{"escolha o estado primeiro"}},
		{"the substyles of a style keep a valid choice", "/opcoes/subestilos?" + url.Values{"estilo": {"techno"}, "selecionada": {sub[0]}}.Encode(), http.StatusOK,
			[]string{`<option value="` + sub[0] + `" selected>`}},
		{"outros has no substyles", "/opcoes/subestilos?" + url.Values{"estilo": {"outros"}}.Encode(), http.StatusOK,
			[]string{"sem subestilo"}},
		{"the day list of the form's date and city", "/eventos/dia?" + url.Values{"data": {"2026-11-14"}, "destaque_uf": {"CE"}, "destaque_cidade": {"Fortaleza"}}.Encode(), http.StatusOK,
			[]string{`id="day-list"`}},
		{"a date the server refuses", "/eventos/dia?" + url.Values{"data": {"31/02/2026"}}.Encode(), http.StatusUnprocessableEntity,
			[]string{`id="day-list"`, "[erro]"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := serve(e.r, http.MethodGet, tc.target, withCookie(e.Cookie[e.Owner.ID]), hx)
			body := evfBody(t, tc.name, rec, tc.want)
			dynHas(t, tc.name, body, tc.in...)
			if strings.Contains(body, "<html") {
				t.Errorf("%s answers a whole page, not the fragment to swap in", tc.name)
			}
		})
	}
}

// dynHas is wantAll for a whole page or fragment: it names what is missing and
// does not print the page.
func dynHas(t *testing.T, what, body string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(body, w) {
			t.Errorf("%s: missing %q (%d bytes)", what, w, len(body))
		}
	}
}
