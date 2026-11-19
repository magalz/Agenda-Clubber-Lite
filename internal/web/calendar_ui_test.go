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

// cuiPage is the calendarPage component of app.js, from Alpine.data('calendarPage' to its closing. The script checks
// below read only it: a string that the module's constants or the other components also hold would pass them whatever
// calendarPage does.
func cuiPage(t *testing.T, js string) string {
	t.Helper()
	page := regexp.MustCompile(`(?s)Alpine\.data\('calendarPage'.*?\n    \}\);\n`).FindString(js)
	if page == "" {
		t.Fatalf("app.js has no calendarPage component (Alpine.data('calendarPage', ...) closed at 4 spaces)")
	}
	return page
}

// cuiMember is the member of calendarPage called name (indented by 8 spaces), from its signature to its closing brace.
func cuiMember(t *testing.T, js, name string) string {
	t.Helper()
	member := regexp.MustCompile(`(?s)\n        (?:async )?` + regexp.QuoteMeta(name) + `\(.*?\n        \},\n`).FindString(cuiPage(t, js))
	if member == "" {
		t.Fatalf("calendarPage has no %s member", name)
	}
	return member
}

// cuiBefore fails unless first is in text and comes before second (which must be there too).
func cuiBefore(t *testing.T, what, text, first, second string) {
	t.Helper()
	a, b := strings.Index(text, first), strings.Index(text, second)
	if a < 0 || b < 0 || a > b {
		t.Errorf("%s: %q must come before %q (at %d and %d): %s", what, first, second, a, b, text)
	}
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

	wantAll(t, "calendarPage", cuiPage(t, dynRead(t, "static/js/app.js")), "#calendar-slot", "innerHTML")
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
// routes read, and opens the sheet with showModal. The keys are the ones of the server (mes, uf, cidade); the day
// list's route is the one of the day link the server renders.
func TestCuiScriptTalksToTheRoutes(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	wantAll(t, "app.js", js, "const MONTH_URL = '/calendario'")
	wantAll(t, "calendarPage", cuiPage(t, js),
		"MONTH_URL", "'mes'", "'uf'", "'cidade'",
		"showModal", "pushState", "popstate", "location.assign", "calendar-page--js")
}

// htmx resolves the promise of htmx.ajax before the swap of a view transition runs (the browser runs that swap a frame
// later), so the month that is in the page right after the await is still the old one. The swap's own event tells
// whether the month came: the month loader awaits swapped(), which listens to htmx:afterSwap and htmx:responseError,
// and never reads the page straight after the bare promise (that sent every animated month change to the link).
func TestCuiMonthSwapIsToldByItsEventNotByThePromise(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	helper := cuiSwapped(t, js)
	wantAll(t, "swapped", helper, "htmx:afterSwap", "htmx:responseError", "removeEventListener")
	month := cuiMember(t, js, "showMonth")
	wantAll(t, "showMonth", month, "swapped(", "location.replace(")
	if strings.Contains(month, "await htmx.ajax") {
		t.Errorf("showMonth awaits htmx.ajax and reads the page after its promise, which resolves before a view transition swaps: %s", month)
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

// The events of a swap bubble to the document from whichever request made it, so the answer of one request must not
// settle the wait of another: swapped() takes the element its request swaps into and heeds only the events of that
// target (the day list's answer once settled the month's wait, and a filter changed twice before the first answer left
// the month without its tab stop, its stagger and its status).
func TestCuiSwapIsToldOnlyByTheEventsOfItsOwnTarget(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	helper := cuiSwapped(t, js)
	if n := strings.Count(helper, "detail.target"); n < 2 {
		t.Errorf("swapped compares detail.target in %d of its 2 listeners (htmx:afterSwap and htmx:responseError): %s", n, helper)
	}
	month := cuiMember(t, js, "showMonth")
	wantAll(t, "showMonth", month, "root.querySelector('#calendar-slot')", "target: slot")
	if !regexp.MustCompile(`(?s)swapped\(.*\bslot,?\s*\)`).MatchString(month) {
		t.Errorf("showMonth does not pass the slot it swaps into as the target swapped() waits for: %s", month)
	}
}

// htmx keeps one request slot for each element that makes requests (the source), and an ajax call that names no source
// uses document.body: the month, the sheet and the day list then share one slot, a second request waits behind the
// first, and its promise resolves at once. Each kind of request therefore names a source of its own, so a chip or a day
// tapped while a month loads works in place instead of reloading the page.
func TestCuiEveryKindOfRequestNamesASourceOfItsOwn(t *testing.T) {
	page := cuiPage(t, dynRead(t, "static/js/app.js"))
	calls := regexp.MustCompile(`htmx\.ajax\([^\n]*`).FindAllString(page, -1)
	if len(calls) != 3 {
		t.Fatalf("calendarPage has %d htmx.ajax calls (a month, a sheet and a day list), want 3: %v", len(calls), calls)
	}
	sources := map[string]bool{}
	for _, call := range calls {
		m := regexp.MustCompile(`\bsource: ([A-Za-z_$][\w$]*)`).FindStringSubmatch(call)
		if m == nil {
			t.Errorf("an htmx.ajax call names no source, so it uses document.body: %s", call)
			continue
		}
		sources[m[1]] = true
	}
	if len(sources) != 3 {
		t.Errorf("the 3 requests share their sources %v, want one each (the slot, the sheet, the root)", sources)
	}
}

// Every control builds its address from ONE record: the newest month asked for, which is the month that is out or
// waiting, else the month in the page. The month in the page is stale while a newer one loads (its filter form, its
// [‹][›][Hoje] and day links carry the old month and filter), so building from it lost the person's choice: [›] then a
// state ended on the old month, a state then [›] reloaded without the state, and [›] twice reloaded.
func TestCuiMonthChangesAreBuiltFromTheNewestAddress(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	page := cuiPage(t, js)
	if regexp.MustCompile(`\bbusy\b`).MatchString(page) {
		t.Errorf("calendarPage still turns an overlapping request away with busy: the newest one waits for its turn instead")
	}
	wantAll(t, "calendarPage", page, "const latest = () =>", "loadMonth.newest()")

	change := cuiMember(t, js, "onCalendarChange")
	wantAll(t, "onCalendarChange", change, "latest()", "this.goTo(")
	for _, bad := range []string{"form.elements", "location."} {
		if strings.Contains(change, bad) {
			t.Errorf("onCalendarChange has %q: it builds from the form of the month in the page, or leaves it, instead of the newest address: %s", bad, change)
		}
	}

	click := cuiMember(t, js, "onCalendarClick")
	from := strings.Index(click, "calendar__nav")
	if from < 0 {
		t.Fatalf("onCalendarClick has no branch for the month bar: %s", click)
	}
	nav := click[from:]
	if to := strings.Index(nav, "} else"); to >= 0 {
		nav = nav[:to]
	}
	wantAll(t, "the month bar's branch", nav, "latest()", "addMonths(", "todayOf()", "this.goTo(")
	for _, bad := range []string{"getAttribute('href')", "location."} {
		if strings.Contains(nav, bad) {
			t.Errorf("the month bar's branch has %q: a link of the month in the page carries that month, not the newest one: %s", bad, nav)
		}
	}

	// A day link of the month in the page carries its filter too: a phone's tap waits for the month that is loading and
	// opens its list there, a wider screen's follows the link with the newest filter.
	wantAll(t, "onCalendarClick", click, "loadMonth.newest()", "dayAfter", "searchParams.set(")
	if n := strings.Count(click, "latest()"); n < 2 {
		t.Errorf("onCalendarClick reads latest() %d times (the month bar and the day links), want 2: %s", n, click)
	}
	month := cuiMember(t, js, "showMonth")
	wantAll(t, "showMonth", month, "dayAfter", "loadDay(")
	if strings.Contains(month, "pushState") {
		t.Errorf("showMonth pushes the history entry, after the answer: a choice has its entry when it is made, whichever way the loader ends: %s", month)
	}
	if !strings.Contains(cuiMember(t, js, "goTo"), "pushState") {
		t.Errorf("goTo does not push the history entry of the choice")
	}
}

// Of each kind of request one runs at a time and only the newest waits behind it (month, sheet and day list), and one
// that takes longer than HANG_WAIT hands the page to the plain link of the newest one, so a request that hangs never
// traps the person (a click while it is out would otherwise only queue). A superseded answer goes on to the newer one,
// and only the newest month leaves the page (to the address it already is in the history, so no entry is added), the
// newest sheet opens the dialog, and a day list whose month was swapped away says nothing.
func TestCuiRequestsOfAKindRunOneAtATimeAndTheNewestWins(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	wantAll(t, "app.js", js, "const HANG_WAIT = ")
	helper := regexp.MustCompile(`(?s)\n  function newestOnly\(run, follow\) \{.*?\n  \}\n`).FindString(js)
	if helper == "" {
		t.Fatalf("app.js has no function newestOnly(run, follow)")
	}
	wantAll(t, "newestOnly", helper, "setTimeout(", "clearTimeout(", "HANG_WAIT", "follow(", "send.newest")

	page := cuiPage(t, js)
	for _, runner := range []string{"loadMonth = newestOnly(", "loadSheet = newestOnly(", "loadDay = newestOnly("} {
		wantAll(t, "calendarPage", page, runner)
	}

	month := cuiMember(t, js, "showMonth")
	cuiBefore(t, "showMonth", month, ".focus()", "newer()")
	cuiBefore(t, "showMonth", month, "newer()", "location.replace(")
	cuiBefore(t, "showMonth", month, "newer()", "this.enhance()")
	cuiBefore(t, "showEvent", cuiMember(t, js, "showEvent"), "newer()", "showModal")
	wantAll(t, "showDay", cuiMember(t, js, "showDay"), "isConnected", "newer()")
}

// The slide of a month is told to the stylesheet (an attribute on the root) when the answer comes, not before the
// request: the answer may take longer than the attribute's life, and a second month's direction must not overwrite the
// first one's while it still slides. The timer that clears it starts there too, and nothing else deletes it
// (htmx:afterSwap comes inside the view transition's update callback, before the pseudo-elements are styled). The
// control the person is on is read when the answer comes as well, since they may have moved to another one by then, and
// no slide runs under the open sheet.
func TestCuiSlideAndFocusAreSettledWhenTheAnswerComes(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	month := cuiMember(t, js, "showMonth")
	for _, settled := range []string{"document.activeElement", "calendarSlide =", "slideTimer = setTimeout("} {
		cuiBefore(t, "showMonth", month, "'htmx:beforeSwap'", settled)
	}
	wantAll(t, "showMonth", month, "SLIDE_KEEP", ".open")
	page := cuiPage(t, js)
	if n := strings.Count(page, "delete "); n != 1 || !strings.Contains(page, "setTimeout(() => delete document.documentElement.dataset.calendarSlide, SLIDE_KEEP)") {
		t.Errorf("the slide attribute is deleted %d times in calendarPage, want once, by the timer that SLIDE_KEEP sets", n)
	}
}

// The keys of the roving tabindex and the media queries the script mirrors from the stylesheet. calendarPage names
// them (the arrows, the breakpoint, the reduced-motion query, the stagger) and the module defines their values.
func TestCuiScriptHandlesTheKeysAndTheBreakpoints(t *testing.T) {
	js := dynRead(t, "static/js/app.js")
	wantAll(t, "app.js", js,
		"const WIDE = '(min-width: 640px)'", "const REDUCED_MOTION = '(prefers-reduced-motion: reduce)'",
		"'animate-fade-up'", "'--i'")
	page := cuiPage(t, js)
	wantAll(t, "calendarPage", page,
		"ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "'Home'", "'End'", "'tabindex'",
		"WIDE", "REDUCED_MOTION", "startViewTransition", "stagger(")
	// One caller and no template names it, so it is not a member of its own.
	if strings.Contains(page, "focusDay") {
		t.Errorf("calendarPage still has focusDay: its one caller moves the focus itself")
	}
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
