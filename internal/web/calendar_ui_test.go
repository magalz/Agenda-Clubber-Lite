package web

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// The interactive layer of the calendar (T4.19): the detail sheet, the month swap, the keyboard and the day list of a
// phone, all of it progressive enhancement over the plain calendar of routes_calendar_test.go. Go cannot run the
// script, so these tests pin what the script stands on: the markup it finds, the members its attributes name, the
// routes and parameters it calls and the classes it needs. What it does with them is checked in a browser.

// --- helpers (prefix cui) ---

// cuiPages are the calendar page and the month alone, for a month with two events.
type cuiPages struct {
	home, month string
	chipHref    string // the link of the first event's chip
}

func cuiRender(t *testing.T) cuiPages {
	t.Helper()
	e := newCalEnv(t)
	first := e.event(t, "Noite Verde", e.at(time.November, 14, 22))
	e.event(t, "Sobral Fest", e.at(time.November, 20, 20), calPlace("CE", "Sobral"))
	return cuiPages{home: e.body(t, "/"), month: e.body(t, "/calendario"), chipHref: eventPath(first.ID)}
}

// cuiRoot is the element that holds the calendar's script: the div with x-data="calendarPage", with its tags.
func cuiRoot(t *testing.T, home string) string {
	t.Helper()
	start := dynOpen(t, home, `x-data="calendarPage"`)
	return home[start:dynClose(t, home, start)]
}

// cuiSheet is the <dialog> of root, tags included.
func cuiSheet(t *testing.T, root string) string {
	t.Helper()
	start := strings.Index(root, "<dialog")
	if start < 0 {
		t.Fatalf("no <dialog> in the calendar:\n%s", root)
	}
	end := strings.Index(root[start:], "</dialog>")
	if end < 0 {
		t.Fatalf("the <dialog> is never closed:\n%s", root[start:])
	}
	return root[start : start+end+len("</dialog>")]
}

// cuiGoTo is the goTo method of calendarPage in app.js, from its signature to its closing brace.
func cuiGoTo(t *testing.T, js string) string {
	t.Helper()
	goTo := regexp.MustCompile(`(?s)async goTo\(.*?\n        \},\n`).FindString(js)
	if goTo == "" {
		t.Fatalf("app.js has no goTo method in calendarPage")
	}
	return goTo
}

// --- the markup ---

// One <dialog class="sheet"> for the whole calendar, outside #calendar-month (the swap replaces that block and would
// take the sheet with it), inside the script's root. Its bar holds only the close button, as a method=dialog form, and
// the title comes with the event (h2.sheet__title) into the body.
func TestCuiSheetSitsOutsideTheMonthWithOnlyTheCloseButtonInTheBar(t *testing.T) {
	p := cuiRender(t)
	root := cuiRoot(t, p.home)

	if n := strings.Count(p.home, "<dialog"); n != 1 {
		t.Errorf("the page has %d <dialog>, want exactly 1", n)
	}
	sheet := cuiSheet(t, root)
	at := strings.Index(root, sheet)
	month := strings.Index(root, `<div class="calendar" id="calendar-month"`)
	if month < 0 {
		t.Fatalf("no month in the root:\n%s", root)
	}
	if end := dynClose(t, root, month); at >= month && at < end {
		t.Errorf("the sheet is inside #calendar-month (%d..%d, at %d): the month swap would replace it", month, end, at)
	}

	wantAll(t, "sheet", sheet, `<dialog class="sheet"`, `@close="onSheetClose"`)
	bar := tagAttrs(t, sheet, `(?s)<div class="sheet__bar">(.*?)\n\s*</div>`)
	wantAll(t, "bar", bar, `<form method="dialog">`,
		`<button class="btn btn--ghost btn--icon" type="submit" aria-label="Fechar">[✕]</button>`)
	if strings.Contains(bar, "<h") || strings.Contains(bar, "sheet__title") {
		t.Errorf("the bar holds more than the close button (the title is the event's own h2): %s", bar)
	}
	// The body is where the event is loaded: empty until the script fills it.
	if !regexp.MustCompile(`(?s)<div class="sheet__body">\s*</div>`).MatchString(sheet) {
		t.Errorf("the sheet has no empty .sheet__body to load the event into: %s", sheet)
	}
}

// The script's root wraps the month and the sheet, and names the listeners it needs: delegated, so the HTML a swap
// brings needs no setup. It also holds the status the script announces a month change in, outside the month.
func TestCuiRootWrapsTheMonthAndTheSheet(t *testing.T) {
	p := cuiRender(t)
	root := cuiRoot(t, p.home)

	attrs := tagAttrs(t, p.home, `<div ([^>]*x-data="calendarPage"[^>]*)>`)
	wantAll(t, "root", attrs, `@click="onCalendarClick"`, `@keydown="onCalendarKeydown"`, `@change="onCalendarChange"`)
	wantAll(t, "root content", root, `id="calendar-month"`, `<dialog class="sheet"`, `<table class="calendar__grid"`)

	status := regexp.MustCompile(`<[a-z]+ [^>]*id="calendar-status"[^>]*>`).FindString(root)
	wantAll(t, "status", status, `role="status"`, `class="sr-only"`)
	month := strings.Index(root, `<div class="calendar" id="calendar-month"`)
	if at := strings.Index(root, status); at >= month && at < dynClose(t, root, month) {
		t.Errorf("the status is inside #calendar-month, which the swap replaces")
	}
}

// The month swaps into a stable slot (innerHTML of #calendar-slot), not over itself: app.js may not write the strategy
// that replaces an element's own HTML (TestSignupUIScript bans the word for the whole script). The slot holds the
// month and nothing else, so the sheet and the status stay put, and GET /calendario answers the month without it.
func TestCuiMonthSwapsIntoAStableSlot(t *testing.T) {
	p := cuiRender(t)
	root := cuiRoot(t, p.home)

	slot := strings.Index(root, `<div id="calendar-slot">`)
	if slot < 0 {
		t.Fatalf("no <div id=\"calendar-slot\"> in the root:\n%s", root)
	}
	slotEnd := dynClose(t, root, slot)
	month := strings.Index(root, `<div class="calendar" id="calendar-month"`)
	if month < slot || dynClose(t, root, month) > slotEnd {
		t.Errorf("the month (at %d) is not inside the slot (%d..%d)", month, slot, slotEnd)
	}
	for _, outside := range []string{`id="calendar-status"`, `<dialog class="sheet"`} {
		if at := strings.Index(root, outside); at >= slot && at < slotEnd {
			t.Errorf("%s is inside the slot (%d..%d, at %d): the swap would replace it", outside, slot, slotEnd, at)
		}
	}
	if strings.Contains(p.month, "calendar-slot") {
		t.Errorf("GET /calendario answers with the slot, which the page already has: it would nest")
	}

	wantAll(t, "goTo", cuiGoTo(t, dynRead(t, "static/js/app.js")), "#calendar-slot", "innerHTML")
}

// A chip is a plain link to the event, so a click with a modifier key, a middle click and a visit with no script all
// open the event page; the script only intercepts the plain click. The month alone has none of the script's markup.
func TestCuiChipsStayPlainLinks(t *testing.T) {
	p := cuiRender(t)
	for name, body := range map[string]string{"home": p.home, "month": p.month} {
		chips := calChips(body)
		if len(chips) != 2 {
			t.Errorf("%s: %d chips, want 2", name, len(chips))
		}
		for _, chip := range chips {
			if !strings.Contains(chip, `href="/eventos/`) {
				t.Errorf("%s: the chip is not a link to an event: %s", name, chip)
			}
		}
		for _, a := range regexp.MustCompile(`<a [^>]*>`).FindAllString(body, -1) {
			if !strings.Contains(a, "calendar__") && !strings.Contains(a, "btn") {
				continue // the shell's links are not the calendar's
			}
			for _, bad := range []string{" x-", " @", " hx-", " onclick", " target="} {
				if strings.Contains(a, bad) {
					t.Errorf("%s: a calendar link carries %q: %s", name, bad, a)
				}
			}
		}
	}
	wantAll(t, "first chip", calChips(p.month)[0], `href="`+p.chipHref+`"`)
	if strings.Contains(p.month, "<dialog") {
		t.Errorf("the month alone holds the sheet")
	}
}

// Without the script the filter is applied with the button, so the button is in the markup of the page and of the
// month, visible and with no script attribute; the script hides it by a class on its root (components.css).
func TestCuiFilterButtonStaysForThePlainForm(t *testing.T) {
	p := cuiRender(t)
	button := regexp.MustCompile(`<button class="[^"]*\bcalendar__apply\b[^"]*" type="submit">\[→\] filtrar</button>`)
	for name, body := range map[string]string{"home": p.home, "month": p.month} {
		form := regexp.MustCompile(`(?s)<form class="calendar__filter".*?</form>`).FindString(body)
		if form == "" {
			t.Fatalf("%s: no filter form", name)
		}
		if !button.MatchString(form) {
			t.Errorf("%s: the filter has no FILTRAR submit button with the calendar__apply hook: %s", name, form)
		}
		wantAll(t, name+" form", form, `method="get"`, `action="/"`)
		for _, bad := range []string{" hidden", " x-", " @"} {
			if strings.Contains(form, bad) {
				t.Errorf("%s: the form carries %q: %s", name, bad, form)
			}
		}
	}
}

// --- the contract with app.js ---

// The Alpine CSP build evaluates nothing: every Alpine attribute of the calendar names a member of calendarPage, and
// no member of it hides one of the shell's (the component sits inside the shell's scope, which Alpine merges).
func TestCuiAlpineAttributesNameMembersOfCalendarPage(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	scopes := dynScopes(js)
	members := scopes["calendarPage"]
	if len(members) == 0 {
		t.Fatalf("app.js registers no calendarPage component with members (Alpine.data('calendarPage', ...) and members indented by 8 spaces); scopes: %v", scopes)
	}

	attr := regexp.MustCompile(`\s(x-[a-z-]+|@[a-z.]+|:[a-z-]+)="([^"]*)"`)
	ident := regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)
	root := cuiRoot(t, cuiRender(t).home)
	found := attr.FindAllStringSubmatch(root, -1)
	if len(found) < 5 {
		t.Errorf("the root holds %d Alpine attributes, want x-data, 3 listeners on the root and @close on the sheet: %v", len(found), found)
	}
	for _, m := range found {
		name, value := m[1], m[2]
		switch {
		case !ident.MatchString(value):
			t.Errorf(`%s="%s" is an expression; the CSP build only takes a property or method name`, name, value)
		case name == "x-data":
			if value != "calendarPage" {
				t.Errorf("x-data=%q inside the calendar's root", value)
			}
		case name == "x-ref":
		case !slices.Contains(members, value):
			t.Errorf(`%s="%s" is not a member of calendarPage (%v)`, name, value, members)
		}
	}

	for _, member := range members {
		if member != "init" && member != "destroy" && slices.Contains(scopes["shell"], member) {
			t.Errorf("calendarPage.%s hides the shell's member of the same name", member)
		}
	}
}

// The script loads the month, the event and the day list from the routes the server answers, with the parameters the
// routes read, and opens the sheet with showModal. The keys are the ones of the server (mes, uf, cidade, data).
func TestCuiScriptTalksToTheRoutes(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	wantAll(t, "app.js", js,
		"/calendario", "/eventos/dia", "'data'", "'uf'", "'cidade'", "'mes'",
		"showModal", "pushState", "popstate", "location.assign",
		"calendarPage", "calendar-page--js")
}

// htmx resolves the promise of htmx.ajax before the swap of a view transition runs (the browser runs that swap a frame
// later), so the month that is in the page right after the await is still the old one. The swap's own event tells
// whether the month came: goTo awaits swapped(), which listens to htmx:afterSwap and htmx:responseError, and never
// reads the page straight after the bare promise (that sent every animated month change to location.assign).
func TestCuiMonthSwapIsToldByItsEventNotByThePromise(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	helper := cuiSwapped(t, js)
	wantAll(t, "swapped", helper, "htmx:afterSwap", "htmx:responseError", "removeEventListener")
	goTo := cuiGoTo(t, js)
	wantAll(t, "goTo", goTo, "swapped(", "location.assign")
	if strings.Contains(goTo, "await htmx.ajax") {
		t.Errorf("goTo awaits htmx.ajax and reads the page after its promise, which resolves before a view transition swaps: %s", goTo)
	}
}

// cuiSwapped is the swapped(request, target) function of app.js, from its signature to its closing brace.
func cuiSwapped(t *testing.T, js string) string {
	t.Helper()
	helper := regexp.MustCompile(`(?s)function swapped\(request, target\) \{.*?\n  \}\n`).FindString(js)
	if helper == "" {
		t.Fatalf("app.js has no function swapped(request, target) for the month swap to await")
	}
	return helper
}

// htmx has one request slot for every htmx.ajax call that names no source (document.body): a request made while
// another is out is queued, and its promise resolves at once. The answer of one request must therefore not settle the
// wait of another: swapped() takes the element its request swaps into and heeds only the events of that target (the day
// list's answer once settled the month's wait, and a filter changed twice before the first answer left the month
// without its tab stop, its stagger and its status).
func TestCuiSwapIsToldOnlyByTheEventsOfItsOwnTarget(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	helper := cuiSwapped(t, js)
	if n := strings.Count(helper, "detail.target"); n < 2 {
		t.Errorf("swapped compares detail.target in %d of its 2 listeners (htmx:afterSwap and htmx:responseError): %s", n, helper)
	}
	wantAll(t, "goTo", cuiGoTo(t, js), "root.querySelector('#calendar-slot')", "slot)")
}

// A month change (the filter, the history) that comes while a month is out waits, and of several only the newest goes
// on, so the page ends on the last choice, with the one history entry of it, and the month in the page is the one the
// script set up (tab stop, stagger, status). A change that comes while the sheet or a day list is out follows its link
// instead, as a click does. Each request names itself in busy, so goTo can tell them apart.
func TestCuiMonthChangesWhileOneIsOutKeepOnlyTheNewest(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	wantAll(t, "app.js", js, "busy = 'month'", "busy = 'sheet'", "busy = 'day'")
	goTo := cuiGoTo(t, js)
	wantAll(t, "goTo", goTo, "pending = [", "busy === 'month'")

	busy := strings.Index(goTo, "if (busy)")
	old := strings.Index(goTo, "const old")
	if busy < 0 || old < 0 || busy > old {
		t.Fatalf("goTo does not meet a request that is out before it reads the page (if (busy) at %d, const old at %d): %s", busy, old, goTo)
	}
	if !strings.Contains(goTo[busy:old], "location.assign(href)") {
		t.Errorf("goTo does not follow its link when the sheet or a day list is out: %s", goTo[busy:old])
	}

	superseded := strings.Index(goTo, "if (pending)")
	push := strings.Index(goTo, "pushState")
	focus := strings.Index(goTo, ".focus()")
	if superseded < 0 || push < 0 || focus < 0 {
		t.Fatalf("goTo has no if (pending) (%d), pushState (%d) or .focus() (%d): %s", superseded, push, focus, goTo)
	}
	if superseded > push {
		t.Errorf("goTo pushes its history entry before it looks for a newer change: a superseded change would leave an entry of its own")
	}
	if focus > superseded {
		t.Errorf("goTo gives the focus back after it looks for a newer change: the control the person is using would be lost on the way to it")
	}
}

// The keys of the roving tabindex and the media queries the script mirrors from the stylesheet.
func TestCuiScriptHandlesTheKeysAndTheBreakpoints(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	wantAll(t, "app.js", js,
		"ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "'Home'", "'End'", "'tabindex'",
		"(min-width: 640px)", "(prefers-reduced-motion: reduce)", "startViewTransition", "animate-fade-up", "'--i'")
}

// The stylesheet has what the script and the markup lean on: the slide of the month (a view transition, off under
// reduced motion), the hidden FILTRAR, the chosen day and the right-aligned close button.
func TestCuiStylesheetDefinesTheClasses(t *testing.T) {
	css := dynRead(t, "static/css/components.css")
	wantAll(t, "components.css", css,
		"::view-transition-old(", "::view-transition-new(", "view-transition-name",
		".calendar-page--js .calendar__apply", ".calendar__cell--chosen",
		".sheet__bar > :only-child")
	// The reduced-motion block of base.css does not reach the view-transition pseudo-elements.
	if !regexp.MustCompile(`(?s)@media \(prefers-reduced-motion: reduce\)\s*\{[^@]*::view-transition`).MatchString(css) {
		t.Errorf("components.css does not switch the view transition off under prefers-reduced-motion: reduce")
	}
}

// Between about 640 and 780px a cell is too narrow for a chip's time and type side by side, and the type's ellipsis cut it
// to "Fe…": the line wraps, so the type goes on a line of its own instead of being cut.
func TestCuiChipMetaWrapsInsteadOfCuttingTheType(t *testing.T) {
	css := dynRead(t, "static/css/components.css")
	rule := regexp.MustCompile(`(?s)\.calendar__chip-meta \{[^}]*\}`).FindString(css)
	if rule == "" {
		t.Fatalf("components.css has no .calendar__chip-meta rule")
	}
	wantAll(t, ".calendar__chip-meta", rule, "display: flex", "flex-wrap: wrap")
}

// What the interactive layer adds is no inline script or style.
func TestCuiNoInlineScriptOrStyle(t *testing.T) {
	p := cuiRender(t)
	for name, body := range map[string]string{"home": p.home, "month": p.month} {
		if v := cspViolations(body); len(v) > 0 {
			t.Errorf("%s: the CSP would block: %v", name, v)
		}
	}
}
