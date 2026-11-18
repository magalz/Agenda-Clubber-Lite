package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	webfs "github.com/magalz/agenda-clubber-lite/web"
)

// The widgets of the event form (date picker, time and duration spinners, BRL
// price) enhance the form T4.12 renders: the server only wraps the fields and
// hands the script two dates. These tests pin that markup and its contract with
// app.js. What the script does with it is checked in a browser, because the Go
// tests cannot run JavaScript.

// ewComponents are the Alpine components the event form uses.
var ewComponents = []string{"datePicker", "timeSpinner", "durationSpinner", "moneyInput"}

// ewForms are the create form and the edit form of one event, as its owner sees them.
type ewForms struct{ create, edit string }

func ewRender(t *testing.T, e evfEnv, mutate ...func(*domain.Event)) ewForms {
	t.Helper()
	ev := e.full(t, mutate...)
	return ewForms{
		create: evdOK(t, "create form", e.get(e.Owner, "/eventos/novo")),
		edit:   evdOK(t, "edit form", e.get(e.Owner, "/eventos/"+ev.ID+"/editar")),
	}
}

func (f ewForms) each(t *testing.T, check func(t *testing.T, body string)) {
	t.Helper()
	t.Run("create", func(t *testing.T) { check(t, f.create) })
	t.Run("edit", func(t *testing.T) { check(t, f.edit) })
}

// ewCloaked matches a bare x-cloak attribute, which base.css hides until Alpine removes it.
var ewCloaked = regexp.MustCompile(`\sx-cloak[\s>]`)

// ewTag returns the opening tag that holds needle.
func ewTag(t *testing.T, body, needle string) string {
	t.Helper()
	m := regexp.MustCompile(`<[a-z]+\b[^>]*` + regexp.QuoteMeta(needle) + `[^>]*>`).FindString(body)
	if m == "" {
		t.Fatalf("no tag with %s in:\n%.800s", needle, body)
	}
	return m
}

// ewBetween returns the text from the first from to the next to after it.
func ewBetween(t *testing.T, body, from, to string) string {
	t.Helper()
	i := strings.Index(body, from)
	if i < 0 {
		t.Fatalf("%q not found", from)
	}
	rest := body[i+len(from):]
	j := strings.Index(rest, to)
	if j < 0 {
		t.Fatalf("%q not found after %q", to, from)
	}
	return rest[:j]
}

// The Alpine CSP build has no expression evaluator: every Alpine attribute must
// name a member app.js registers (the rule of TestAlpineAttributesNameRegisteredMembers,
// which only sees the home page). The form has the four components, so the check
// cannot pass with nothing to check.
func TestEwAlpineAttributesNameRegisteredMembers(t *testing.T) {
	js, err := fs.ReadFile(webfs.Static, "static/js/app.js")
	if err != nil {
		t.Fatal(err)
	}
	attr := regexp.MustCompile(`\s(x-[a-z-]+|@[a-z.]+|:[a-z-]+)="([^"]*)"`)
	ident := regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

	ewRender(t, newEvfEnv(t)).each(t, func(t *testing.T, body string) {
		for _, c := range ewComponents {
			if !strings.Contains(body, `x-data="`+c+`"`) {
				t.Errorf("the form has no x-data=%q", c)
			}
		}
		for _, m := range attr.FindAllStringSubmatch(body, -1) {
			name, value := m[1], m[2]
			if !ident.MatchString(value) {
				t.Errorf(`%s="%s" is an expression; the CSP build only takes a property or method name`, name, value)
				continue
			}
			switch name {
			case "x-ref":
			case "x-data":
				if !strings.Contains(string(js), "Alpine.data('"+value+"'") {
					t.Errorf("x-data=%q is not registered with Alpine.data in app.js", value)
				}
			default:
				member := regexp.MustCompile(`(?m)^\s*(get\s+)?` + regexp.QuoteMeta(value) + `\s*[(:]`)
				if !member.Match(js) {
					t.Errorf(`%s="%s" is not a member of a component in app.js`, name, value)
				}
			}
		}
	})
}

func TestEwNoInlineScriptOrStyle(t *testing.T) {
	ewRender(t, newEvfEnv(t)).each(t, func(t *testing.T, body string) {
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("the CSP would block: %v", v)
		}
	})
}

// A widget adds no field: the posted names are the ones the server reads.
func TestEwPostedNamesAreUnchanged(t *testing.T) {
	names := regexp.MustCompile(`<(?:input|select|textarea|button)\b[^>]*\bname="([^"]+)"`)
	common := []string{"name", "type", "type_other", "date", "time", "duration_hours", "duration_minutes", "price", "free",
		"ticket_url", "style", "substyle", "style_other", "state", "city", "venue", "artists", "description", "acao"}
	want := map[string][]string{
		"create": append([]string{"collective_id"}, common...),
		"edit":   append([]string{"version"}, common...),
	}

	f := ewRender(t, newEvfEnv(t))
	for kind, body := range map[string]string{"create": f.create, "edit": f.edit} {
		form := ewBetween(t, body, `<form class="event-form`, `</form>`)
		var got []string
		for _, m := range names.FindAllStringSubmatch(form, -1) {
			got = append(got, m[1])
		}
		if strings.Join(got, ",") != strings.Join(want[kind], ",") {
			t.Errorf("%s form posts %v, want %v", kind, got, want[kind])
		}
	}
}

// The wrappers hold the controls their script drives, and every control the
// script adds is hidden until it runs: without JavaScript the form is as T4.12
// left it.
func TestEwMarkup(t *testing.T) {
	ewRender(t, newEvfEnv(t)).each(t, func(t *testing.T, body string) {
		// Each component wraps its own inputs.
		wrapped := func(component, next string, ids ...string) {
			t.Helper()
			inside := ewBetween(t, body, `x-data="`+component+`"`, `x-data="`+next+`"`)
			for _, id := range ids {
				if !strings.Contains(inside, `id="`+id+`"`) {
					t.Errorf("%s does not wrap #%s", component, id)
				}
			}
		}
		wrapped("datePicker", "timeSpinner", "date")
		wrapped("timeSpinner", "durationSpinner", "time")
		wrapped("durationSpinner", "moneyInput", "duration_hours", "duration_minutes")
		money := ewBetween(t, body, `x-data="moneyInput"`, `</fieldset>`)
		for _, id := range []string{"price", "free"} {
			if !strings.Contains(money, `id="`+id+`"`) {
				t.Errorf("moneyInput does not wrap #%s", id)
			}
		}

		// The date picker: a named toggle, and a popup that is labelled and hidden until opened.
		toggle := ewTag(t, body, `date-picker__toggle`)
		wantAll(t, "the date toggle", toggle, `type="button"`, `aria-label="escolher data"`, `aria-haspopup="dialog"`, `aria-controls="date-picker-popup"`, `aria-expanded="false"`)
		if !ewCloaked.MatchString(toggle) {
			t.Errorf("the date toggle shows without JavaScript: no x-cloak in %s", toggle)
		}
		popup := ewTag(t, body, `id="date-picker-popup"`)
		wantAll(t, "the date popup", popup, `role="dialog"`, `aria-label="escolher data"`, ` hidden`)
		wantAll(t, "the month title", ewTag(t, body, `date-picker__title`), `aria-live="polite"`)
		for _, dir := range []string{"-1", "1"} {
			nav := ewTag(t, body, `data-nav="`+dir+`"`)
			wantAll(t, "the month button "+dir, nav, `class="btn btn--outline btn--sm"`, `type="button"`, `aria-label=`)
		}

		// The spinners: a name and a type on every button, hidden until the script runs.
		steps := regexp.MustCompile(`<button\b[^>]*\bdata-step="(-?\d+)"[^>]*>`).FindAllStringSubmatch(body, -1)
		var got []string
		for _, m := range steps {
			wantAll(t, "the spinner button "+m[1], m[0], `type="button"`, `aria-label=`)
			got = append(got, m[1])
		}
		if want := "60,-60,1,-1,60,-60,1,-1"; strings.Join(got, ",") != want {
			t.Errorf("spinner steps = %v, want %s (hour up and down, minute up and down, for the time and for the duration)", got, want)
		}
		if n := strings.Count(body, `<span class="spinner__btns" x-cloak>`); n != 4 {
			t.Errorf("%d hidden spinner button groups, want 4", n)
		}

		// The price: R$ beside the input, hidden without the script (the label already says R$).
		wantAll(t, "the R$ prefix", body, `<span class="input-group__prefix" aria-hidden="true" x-cloak>R$</span>`)
		wantAll(t, "the message of a refused value", body, `class="field-status" aria-live="polite" x-text="message"`)
	})
}

// data-today and data-keep tell the script which days it may offer: today (in
// APP_TZ, not UTC) and, editing, the event's own day, which stays valid even
// when it is past (events.Form: schedule).
func TestEwDatePickerDates(t *testing.T) {
	e := newEvfEnv(t)
	loc := e.App.Cfg.Location
	pastNight := func(ev *domain.Event) { ev.StartsAt = time.Date(2026, 11, 1, 22, 0, 0, 0, loc).UTC() } // 02/11 01:00 UTC

	edit := func(mutate ...func(*domain.Event)) func() string {
		return func() string {
			ev := e.full(t, mutate...)
			return evdOK(t, "edit form", e.get(e.Owner, "/eventos/"+ev.ID+"/editar"))
		}
	}
	create := func() string { return evdOK(t, "create form", e.get(e.Owner, "/eventos/novo")) }

	const afterDinner = "2026-11-13T01:30:00Z" // 22:30 of the 12th in Fortaleza
	late, err := time.Parse(time.RFC3339, afterDinner)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		now   time.Time
		page  func() string
		today string
		keep  string // "" means the attribute is absent
	}{
		{"create", webTestNow, create, "2026-11-12", ""},
		{"create, UTC is already the next day", late, create, "2026-11-12", ""},
		{"edit an event ahead (22:00 in Fortaleza, the next day in UTC)", webTestNow, edit(), "2026-11-12", "2026-11-14"},
		{"edit a past event (22:00 in Fortaleza, the next day in UTC)", webTestNow, edit(pastNight), "2026-11-12", "2026-11-01"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			realNow := e.App.Now
			e.App.Now = func() time.Time { return tc.now }
			defer func() { e.App.Now = realNow }()

			tag := ewTag(t, tc.page(), `x-data="datePicker"`)
			wantAll(t, "the date picker", tag, ` data-today="`+tc.today+`"`)
			if tc.keep == "" {
				if strings.Contains(tag, "data-keep") {
					t.Errorf("the date picker has data-keep on a new event: %s", tag)
				}
				return
			}
			wantAll(t, "the date picker", tag, ` data-keep="`+tc.keep+`"`)
		})
	}
}
