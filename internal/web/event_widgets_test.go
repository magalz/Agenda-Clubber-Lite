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
		form := tagAttrs(t, body, `(?s)<form class="event-form(.*?)</form>`)
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
			inside := tagAttrs(t, body, `(?s)x-data="`+component+`"(.*?)x-data="`+next+`"`)
			for _, id := range ids {
				if !strings.Contains(inside, `id="`+id+`"`) {
					t.Errorf("%s does not wrap #%s", component, id)
				}
			}
		}
		wrapped("datePicker", "timeSpinner", "date")
		wrapped("timeSpinner", "durationSpinner", "time")
		wrapped("durationSpinner", "moneyInput", "duration_hours", "duration_minutes")
		money := tagAttrs(t, body, `(?s)x-data="moneyInput"(.*?)</fieldset>`)
		for _, id := range []string{"price", "free"} {
			if !strings.Contains(money, `id="`+id+`"`) {
				t.Errorf("moneyInput does not wrap #%s", id)
			}
		}

		// The date picker: a named toggle, and a popup that is labelled and hidden until opened.
		toggle := tagAttrs(t, body, `(<[a-z]+\b[^>]*date-picker__toggle[^>]*>)`)
		wantAll(t, "the date toggle", toggle, `type="button"`, `aria-label="escolher data"`, `aria-haspopup="dialog"`, `aria-controls="date-picker-popup"`, `aria-expanded="false"`)
		if !ewCloaked.MatchString(toggle) {
			t.Errorf("the date toggle shows without JavaScript: no x-cloak in %s", toggle)
		}
		popup := tagAttrs(t, body, `(<[a-z]+\b[^>]*id="date-picker-popup"[^>]*>)`)
		wantAll(t, "the date popup", popup, `role="dialog"`, `aria-label="escolher data"`, ` hidden`)
		wantAll(t, "the month title", tagAttrs(t, body, `(<[a-z]+\b[^>]*date-picker__title[^>]*>)`), `aria-live="polite"`)
		for _, dir := range []string{"-1", "1"} {
			nav := tagAttrs(t, body, `(<[a-z]+\b[^>]*data-nav="`+dir+`"[^>]*>)`)
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

// The picker draws the calendar's own table, so a restyled calendar restyles the
// picker too: the script names the calendar's classes, the stylesheet defines
// them, and it keeps no copy of them under the picker's name.
func TestEwDatePickerUsesTheCalendarGrid(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		b, err := fs.ReadFile(webfs.Static, name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	js, css := read("static/js/app.js"), read("static/css/components.css")

	for _, class := range []string{"calendar__grid", "calendar__weekday"} {
		if !strings.Contains(js, "className = '"+class+"'") {
			t.Errorf("app.js does not give the picker's table the calendar's %s", class)
		}
		if !strings.Contains(css, "\n."+class+" {") {
			t.Errorf("components.css has no .%s rule", class)
		}
	}
	for _, copied := range []string{"date-picker__table", "date-picker__weekday"} {
		if strings.Contains(js, copied) || strings.Contains(css, copied) {
			t.Errorf("%s is a copy of a calendar rule: use the calendar's class", copied)
		}
	}
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

			tag := tagAttrs(t, tc.page(), `(<[a-z]+\b[^>]*x-data="datePicker"[^>]*>)`)
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

// ewAppJS is the script the event-form widgets live in.
func ewAppJS(t *testing.T) string {
	t.Helper()
	b, err := fs.ReadFile(webfs.Static, "static/js/app.js")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// ewWidgetSource returns one Alpine.data component, from its registration to the factory's closing "    });".
func ewWidgetSource(t *testing.T, component string) string {
	t.Helper()
	js := ewAppJS(t)
	start := strings.Index(js, "Alpine.data('"+component+"'")
	if start < 0 {
		t.Fatalf("app.js has no Alpine.data(%q)", component)
	}
	end := strings.Index(js[start:], "\n    });")
	if end < 0 {
		t.Fatalf("app.js has no end for Alpine.data(%q)", component)
	}
	return js[start : start+end]
}

// ewMethod returns one method of a widget, at the factory method's eight-space indentation.
func ewMethod(t *testing.T, component, method string) string {
	t.Helper()
	src := ewWidgetSource(t, component)
	start := strings.Index(src, "\n        "+method+"(")
	if start < 0 {
		t.Fatalf("the %s component has no %s method", component, method)
	}
	end := strings.Index(src[start:], "\n        },")
	if end < 0 {
		t.Fatalf("the %s %s method has no end", component, method)
	}
	return src[start : start+end]
}

// ewBetween returns the source between two markers, so one test can pin both branches of a rewrite.
func ewBetween(t *testing.T, src, from, to string) string {
	t.Helper()
	start := strings.Index(src, from)
	if start < 0 {
		t.Fatalf("no %q in the widget source", from)
	}
	rest := src[start+len(from):]
	end := strings.Index(rest, to)
	if end < 0 {
		t.Fatalf("no %q after %q in the widget source", to, from)
	}
	return rest[:end]
}

// The masks of the time and price fields rewrite what the person typed. The caret must follow the edit: the
// script saves it before rewriting and restores an adjusted index in both the success and the rejected-value
// branch. Go cannot run the script; these tests pin that shape in app.js, as the picker's classes are pinned.
func TestEwTimeSpinnerKeepsCaret(t *testing.T) {
	onInput := ewMethod(t, "timeSpinner", "onInput")
	wantAll(t, "the time caret capture", onInput,
		"const before = input.value;",
		"const caret = input.selectionStart ?? before.length;",
	)
	if strings.Index(onInput, "const caret =") > strings.Index(onInput, "const typed = typedClock(") {
		t.Error("the time caret is captured after the mask rewrote the text")
	}

	errorPath := ewBetween(t, onInput, "if (typed.error) {", "return;")
	wantAll(t, "the refused time", errorPath,
		"input.value = last;",
		"const restored = caretAfterRewrite(before, last, caret);",
		"if (document.activeElement === input) input.setSelectionRange(restored, restored);",
	)

	successPath := ewBetween(t, onInput, "if (input.value !== typed.text)", "last = typed.text;")
	wantAll(t, "the reformatted time", successPath,
		"input.value = typed.text;",
		"const restored = caretAfterRewrite(before, typed.text, caret);",
		"if (document.activeElement === input) input.setSelectionRange(restored, restored);",
	)
	if strings.Contains(onInput, "setSelectionRange(input.value.length") || strings.Contains(onInput, "setSelectionRange(caret, caret)") {
		t.Error("the time caret must be restored to the adjusted index, not to the captured or final index")
	}
}

func TestEwMoneyInputKeepsCaret(t *testing.T) {
	onInput := ewMethod(t, "moneyInput", "onInput")
	wantAll(t, "the price caret capture", onInput,
		"const before = price.value;",
		"const caret = price.selectionStart ?? before.length;",
	)
	if strings.Index(onInput, "const caret =") > strings.Index(onInput, "const cents = Number(") {
		t.Error("the price caret is captured after the mask rewrote the text")
	}

	errorPath := ewBetween(t, onInput, "if (cents > PRICE_MAX) {", "return;")
	wantAll(t, "the refused price", errorPath,
		"price.value = last;",
		"const restored = caretAfterRewrite(before, last, caret);",
		"if (document.activeElement === price) price.setSelectionRange(restored, restored);",
	)

	successPath := ewBetween(t, onInput, "last = formatCents(cents);", "this.message = '';")
	wantAll(t, "the reformatted price", successPath,
		"if (price.value !== last) {",
		"price.value = last;",
		"const restored = caretAfterRewrite(before, last, caret);",
		"if (document.activeElement === price) price.setSelectionRange(restored, restored);",
	)
	if strings.Contains(onInput, "setSelectionRange(price.value.length") || strings.Contains(onInput, "setSelectionRange(caret, caret)") {
		t.Error("the price caret must be restored to the adjusted index, not to the captured or final index")
	}
}

func TestEwNoThirdWidgetTouchesTheCaret(t *testing.T) {
	js := ewAppJS(t)
	// One restore in the phone mask, one in each branch of the time and the price mask.
	if n := strings.Count(js, "setSelectionRange("); n != 5 {
		t.Errorf("%d setSelectionRange calls in app.js, want 5 (phone mask, and time and price in both branches)", n)
	}
	for _, name := range []string{"datePicker", "durationSpinner"} {
		src := ewWidgetSource(t, name)
		if strings.Contains(src, "setSelectionRange") || strings.Contains(src, "selectionStart") {
			t.Errorf("the %s widget must not touch the caret", name)
		}
	}
	// The index is derived from how the rewrite changed the text, not from the field's old or new length:
	// surviving text before/after the caret is anchored, and an edit inside the replaced span keeps its digit.
	for _, part := range []string{
		"function caretAfterRewrite(before, after, caret)",
		"before[prefix] === after[prefix]",
		"before.length - 1 - suffix",
		"caret < prefix", // a separator inserted at the caret must be stepped over, not left to the left of it
		"caretAfterDigits(after,",
	} {
		if !strings.Contains(js, part) {
			t.Errorf("the caret adjustment: app.js has no %q", part)
		}
	}
}
