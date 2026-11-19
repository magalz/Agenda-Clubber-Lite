package events

import (
	"maps"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/catalog"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// None of these tests needs the database: Form.Event reads no store.

// inputForm is a form that creates a valid event: a Festa on Sunday 15/11/2026,
// 22:00 in Fortaleza, for six hours, R$ 25,00, in Fortaleza/CE.
func inputForm() Form {
	return Form{
		CollectiveID: "col-1",
		Name:         "Noite de Teste",
		Type:         "festa",
		Date:         "15/11/2026",
		Time:         "22:00",
		DurHours:     "6",
		DurMinutes:   "0",
		Price:        "R$ 25,00",
		TicketURL:    "https://ingressos.example/noite",
		Style:        "techno",
		Substyle:     "acid techno",
		State:        "CE",
		City:         "Fortaleza",
		Venue:        "Galpão 7",
		Artists:      "Ana\nBeto",
		Description:  "Uma noite de teste.",
	}
}

// inputEvent is the event inputForm turns into.
func inputEvent() domain.Event {
	return domain.Event{
		CollectiveID: "col-1",
		Name:         "Noite de Teste",
		Type:         domain.EventTypeFesta,
		StartsAt:     time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC),
		DurationMin:  360,
		PriceCents:   2500,
		TicketURL:    "https://ingressos.example/noite",
		Style:        "techno",
		Substyle:     "acid techno",
		State:        "CE",
		City:         "Fortaleza",
		Venue:        "Galpão 7",
		Artists:      "Ana\nBeto",
		Description:  "Uma noite de teste.",
	}
}

// inputPrev is a stored event to edit: the same Festa as inputEvent, from
// another collective, saved twice.
func inputPrev() domain.Event {
	e := inputEvent()
	e.ID = "ev-1"
	e.CollectiveID = "col-prev"
	e.CreatedBy = "user-1"
	e.UpdatedBy = "user-2"
	e.CreatedAt = testNow.Add(-48 * time.Hour)
	e.UpdatedAt = testNow.Add(-time.Hour)
	e.Version = 3
	return e
}

// inputEditForm is inputForm as the edit page posts it back for inputPrev.
func inputEditForm() Form {
	f := inputForm()
	f.CollectiveID = "col-ignored"
	f.Version = "3"
	return f
}

// inputMustEvent turns the form into an event at testNow and fails the test on any
// field error. It also checks the rule behind the whole file: what Event accepts
// passes domain.Event.Validate, so the service never fails on user input.
func inputMustEvent(t *testing.T, f Form, prev *domain.Event) domain.Event {
	t.Helper()
	e, fe := f.Event(testNow, testLoc, prev)
	if fe != nil {
		t.Fatalf("Event(%+v) field errors: %v", f, fe)
	}
	v := e
	if v.CreatedAt.IsZero() { // the service sets it on create
		v.CreatedAt = testNow
	}
	if err := v.Validate(); err != nil {
		t.Errorf("Event(%+v) = %+v, which fails Validate: %v", f, e, err)
	}
	return e
}

func TestFormEventCreate(t *testing.T) {
	got, fe := inputForm().Event(testNow, testLoc, nil)
	if fe != nil {
		t.Fatalf("field errors: %v", fe)
	}
	want := inputEvent()
	if got.StartsAt.Location() != time.UTC || !got.StartsAt.Equal(want.StartsAt) {
		t.Errorf("StartsAt = %v, want %v (22:00 in Fortaleza, in UTC)", got.StartsAt, want.StartsAt)
	}
	got.StartsAt, want.StartsAt = time.Time{}, time.Time{}
	if got != want {
		t.Errorf("event =\n%+v\nwant\n%+v", got, want)
	}
	inputMustEvent(t, inputForm(), nil)
}

func TestFormEventEditCopiesWhatTheFormNeverChanges(t *testing.T) {
	prev := inputPrev()
	got := inputMustEvent(t, inputEditForm(), &prev)
	want := inputEvent()
	want.ID, want.CollectiveID, want.CreatedBy, want.CreatedAt = prev.ID, prev.CollectiveID, prev.CreatedBy, prev.CreatedAt
	want.Version = 3 // from the form; UpdatedBy and UpdatedAt stay zero: the service sets them
	if got.StartsAt.Location() != time.UTC || !got.StartsAt.Equal(want.StartsAt) {
		t.Errorf("StartsAt = %v, want %v", got.StartsAt, want.StartsAt)
	}
	got.StartsAt, want.StartsAt = time.Time{}, time.Time{}
	if got != want {
		t.Errorf("event =\n%+v\nwant\n%+v", got, want)
	}
	if prev != inputPrev() {
		t.Errorf("Event changed prev: %+v", prev)
	}
}

func TestFormEventCreateIgnoresVersion(t *testing.T) {
	f := inputForm()
	f.Version = "not a number"
	if got := inputMustEvent(t, f, nil); got.Version != 0 {
		t.Errorf("Version = %d, want 0 when creating", got.Version)
	}
}

func TestFormEventVersion(t *testing.T) {
	prev := inputPrev()
	for _, v := range []string{"1", " 7 ", "9223372036854775807"} {
		f := inputEditForm()
		f.Version = v
		want, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if got := inputMustEvent(t, f, &prev); got.Version != want {
			t.Errorf("Version %q parsed to %d, want %d", v, got.Version, want)
		}
	}
	for _, v := range []string{"", " ", "0", "-1", "+3", "abc", "1.5", "3x", "9223372036854775808", "٣"} {
		f := inputEditForm()
		f.Version = v
		_, fe := f.Event(testNow, testLoc, &prev)
		requireFieldErrors(t, fe, "version")
		if fe["version"] != domain.MsgInvalid {
			t.Errorf("version %q: message %q, want %q", v, fe["version"], domain.MsgInvalid)
		}
	}
}

// inputCase is a form that must fail with exactly one field error.
type inputCase struct {
	name   string
	mutate func(*Form)
	key    string
	msg    string // the exact message; empty means any non-empty one
}

// inputBadValues makes one inputCase for each value of the field key.
func inputBadValues(key string, set func(*Form, string), values ...string) []inputCase {
	var out []inputCase
	for _, v := range values {
		out = append(out, inputCase{key + " " + strconv.Quote(v), func(f *Form) { set(f, v) }, key, ""})
	}
	return out
}

func TestFormEventFieldErrors(t *testing.T) {
	line := strings.Repeat("a", 3000)
	tests := []inputCase{
		{"collective missing", func(f *Form) { f.CollectiveID = "  " }, "collective_id", domain.MsgRequired},

		{"name empty", func(f *Form) { f.Name = " \t " }, "name", domain.MsgRequired},
		{"name too short", func(f *Form) { f.Name = " a " }, "name", ""},
		{"name too long", func(f *Form) { f.Name = line[:domain.EventNameMax+1] }, "name", ""},
		{"name with a line break", func(f *Form) { f.Name = "Noite\nTeste" }, "name", domain.MsgControl},
		{"name with a tab", func(f *Form) { f.Name = "Noite\tTeste" }, "name", domain.MsgControl},
		{"name with a bell", func(f *Form) { f.Name = "Noite\x07Teste" }, "name", domain.MsgControl},
		{"name not UTF-8", func(f *Form) { f.Name = "Noite\xff" }, "name", domain.MsgInvalid},

		{"type empty", func(f *Form) { f.Type = "" }, "type", domain.MsgRequired},
		{"type unknown", func(f *Form) { f.Type = "balada" }, "type", domain.MsgInvalid},
		{"type in capitals", func(f *Form) { f.Type = "Festa" }, "type", domain.MsgInvalid},
		{"type outros without its text", func(f *Form) { f.Type = "outros" }, "type_other", domain.MsgRequired},
		{"type outros with blank text", func(f *Form) { f.Type, f.TypeOther = "outros", "  " }, "type_other", domain.MsgRequired},
		{"type outros with a long text", func(f *Form) { f.Type, f.TypeOther = "outros", line[:domain.EventOtherMax+1] }, "type_other", ""},
		{"type outros with a line break", func(f *Form) { f.Type, f.TypeOther = "outros", "a\nb" }, "type_other", domain.MsgControl},

		{"date empty", func(f *Form) { f.Date = " " }, "date", domain.MsgRequired},
		{"time empty", func(f *Form) { f.Time = "" }, "time", domain.MsgRequired},
		{"date in the past", func(f *Form) { f.Date = "13/11/2026" }, "date", "a data não pode ser anterior a hoje"},
		{"date in the past, ISO", func(f *Form) { f.Date = "2026-11-13" }, "date", "a data não pode ser anterior a hoje"},

		{"duration empty", func(f *Form) { f.DurHours, f.DurMinutes = "", " " }, "duration", domain.MsgRequired},
		{"duration zero", func(f *Form) { f.DurHours, f.DurMinutes = "0", "0" }, "duration", ""},
		{"duration of 14 minutes", func(f *Form) { f.DurHours, f.DurMinutes = "", "14" }, "duration", ""},

		{"price empty", func(f *Form) { f.Price = "" }, "price", domain.MsgRequired},
		{"price zero", func(f *Form) { f.Price = "0" }, "price", ""},
		{"price above the maximum", func(f *Form) { f.Price = "100.000,00" }, "price", ""},

		{"ticket link too long", func(f *Form) { f.TicketURL = "https://a.example/" + strings.Repeat("a", domain.EventURLMax) }, "ticket_url", ""},

		{"style empty", func(f *Form) { f.Style = "" }, "style", domain.MsgRequired},
		{"style outros without its text", func(f *Form) { f.Style, f.Substyle = "outros", "" }, "style_other", domain.MsgRequired},
		{"style outros with blank text", func(f *Form) { f.Style, f.StyleOther = "outros", " " }, "style_other", domain.MsgRequired},
		{"style outros with a long text", func(f *Form) { f.Style, f.StyleOther = "outros", line[:domain.EventOtherMax+1] }, "style_other", ""},
		{"style outros with a line break", func(f *Form) { f.Style, f.StyleOther = "outros", "a\nb" }, "style_other", domain.MsgControl},
		{"substyle of another style", func(f *Form) { f.Substyle = "acid house" }, "substyle", domain.MsgInvalid},

		{"venue too long", func(f *Form) { f.Venue = line[:domain.EventVenueMax+1] }, "venue", ""},
		{"venue with a line break", func(f *Form) { f.Venue = "a\nb" }, "venue", domain.MsgControl},
		{"artists too long", func(f *Form) { f.Artists = line[:domain.EventArtistsMax+1] }, "artists", ""},
		{"artists with a control character", func(f *Form) { f.Artists = "Ana\x00Beto" }, "artists", ""},
		{"description too long", func(f *Form) { f.Description = strings.Repeat("a", domain.EventDescriptionMax+1) }, "description", ""},
		{"description with a control character", func(f *Form) { f.Description = "a\x07b" }, "description", ""},
		{"description not UTF-8", func(f *Form) { f.Description = "a\xffb" }, "description", domain.MsgInvalid},
	}
	tests = append(tests, inputBadValues("date", func(f *Form, v string) { f.Date = v },
		"31/11/2026", "29/02/2027", "00/12/2026", "15/00/2026", "15/13/2026", "15-11-2026", "2026/11/15",
		"15/12/26", "2026-12-5", "2026-1-05", "15.12.2026", "amanhã", "15/12", "15/12/2026/1", "١٥/12/2026", "+5/12/2026",
		"15/11/+026", "15/11/-026", "015/11/2026", "15/11/20266", "2026-11-15T22:00")...)
	tests = append(tests, inputBadValues("time", func(f *Form, v string) { f.Time = v },
		"24:00", "22:60", "22", "2200", "22:5", "ab:cd", "22:00:00", "-1:00", "22h00", "1:2:3", "+1:00", "٢٢:٠٠", "001:00", "1: 00", "22:000")...)
	tests = append(tests, inputBadValues("duration", func(f *Form, v string) { f.DurHours = v },
		"24", "-1", "abc", "1.5", "+1", "100", "٣")...)
	tests = append(tests, inputBadValues("duration", func(f *Form, v string) { f.DurMinutes = v },
		"60", "-1", "abc", "1.5", "+1", "100", "٣")...)
	tests = append(tests, inputBadValues("price", func(f *Form, v string) { f.Price = v },
		"25.50", "1,234.56", "-5", "+25", "abc", "R$", "1.23", "12.34.567", ",50", "25,", "25,555", "1,2,3",
		"0,00", "100000,00", "12.345.678", "1 234,56", "R$R$ 5", "25 reais", "٢٥", "99999999999999999999")...)
	tests = append(tests, inputBadValues("ticket_url", func(f *Form, v string) { f.TicketURL = v },
		"ftp://x.example", "x.example", "https://", "https:///caminho", "https://exa mple.com", "https://x.example/a b",
		"javascript:alert(1)", "//x.example", "https://x.example/\na", "mailto:a@x.example")...)
	tests = append(tests, inputBadValues("style", func(f *Form, v string) { f.Style = v },
		"Techno", "forró-xyz", "techno\nhouse", "Outros", "acid techno")...)
	tests = append(tests, inputBadValues("substyle", func(f *Form, v string) { f.Substyle = v },
		"xyz", "Acid Techno", "techno")...)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := inputForm()
			tc.mutate(&f)
			_, fe := f.Event(testNow, testLoc, nil)
			requireFieldErrors(t, fe, tc.key)
			if got := fe[tc.key]; got == "" || (tc.msg != "" && got != tc.msg) {
				t.Errorf("%s message = %q, want %q", tc.key, got, tc.msg)
			}
		})
	}
}

func TestFormEventEmptyForm(t *testing.T) {
	_, fe := Form{}.Event(testNow, testLoc, nil)
	requireFieldErrors(t, fe, "collective_id", "name", "type", "date", "time", "duration", "price", "style", "state", "city")

	prev := inputPrev()
	_, fe = Form{}.Event(testNow, testLoc, &prev)
	requireFieldErrors(t, fe, "name", "type", "date", "time", "duration", "price", "style", "state", "city", "version")
}

func TestFormEventDateAndTime(t *testing.T) {
	local := func(y int, m time.Month, d, h, min int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, testLoc)
	}
	tests := []struct {
		date, clock string
		want        time.Time
	}{
		{"15/11/2026", "22:00", local(2026, 11, 15, 22, 0)},
		{"2026-11-15", "22:00", local(2026, 11, 15, 22, 0)},
		{"5/1/2027", "9:30", local(2027, 1, 5, 9, 30)},
		{"05/01/2027", "09:30", local(2027, 1, 5, 9, 30)},
		{"2027-01-05", "00:00", local(2027, 1, 5, 0, 0)},
		{"29/02/2028", "23:59", local(2028, 2, 29, 23, 59)},
		{" 31/12/2026 ", " 7:05 ", local(2026, 12, 31, 7, 5)},
	}
	for _, tc := range tests {
		f := inputForm()
		f.Date, f.Time = tc.date, tc.clock
		got := inputMustEvent(t, f, nil).StartsAt
		if got.Location() != time.UTC || !got.Equal(tc.want) {
			t.Errorf("date %q time %q: StartsAt = %v, want %v in UTC", tc.date, tc.clock, got, tc.want.UTC())
		}
	}
}

func TestFormEventDuration(t *testing.T) {
	for _, tc := range []struct {
		hours, minutes string
		want           int
	}{
		{"6", "0", 360}, {"", "30", 30}, {"6", "", 360}, {"0", "15", 15}, {"23", "59", 1439},
		{"1", "30", 90}, {"01", "05", 65}, {" 2 ", " 10 ", 130},
	} {
		f := inputForm()
		f.DurHours, f.DurMinutes = tc.hours, tc.minutes
		if got := inputMustEvent(t, f, nil).DurationMin; got != tc.want {
			t.Errorf("hours %q minutes %q: DurationMin = %d, want %d", tc.hours, tc.minutes, got, tc.want)
		}
	}
}

func TestFormEventPrice(t *testing.T) {
	for _, tc := range []struct {
		price string
		want  int64
	}{
		{"R$ 1.234,56", 123456}, {"1.234,56", 123456}, {"1234,56", 123456}, {"25", 2500}, {"25,5", 2550},
		{"25,50", 2550}, {"R$25", 2500}, {"  R$  25  ", 2500}, {"0,01", 1}, {"0,5", 50}, {"999", 99900},
		{"1.000", 100000}, {"99.999,99", domain.EventPriceMax}, {"99999,99", domain.EventPriceMax}, {"007,1", 710},
	} {
		f := inputForm()
		f.Price = tc.price
		got := inputMustEvent(t, f, nil)
		if got.PriceCents != tc.want || got.Free {
			t.Errorf("price %q: PriceCents = %d Free = %v, want %d, not free", tc.price, got.PriceCents, got.Free, tc.want)
		}
	}
}

func TestFormEventFreeIgnoresThePrice(t *testing.T) {
	for _, price := range []string{"", "25", "abc", "-5", "100.000,00"} {
		f := inputForm()
		f.Free, f.Price = true, price
		got := inputMustEvent(t, f, nil)
		if !got.Free || got.PriceCents != 0 {
			t.Errorf("free with price %q: Free = %v PriceCents = %d, want free and 0", price, got.Free, got.PriceCents)
		}
	}
}

func TestFormEventTicketURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"https://x.example/a?b=c#d", "https://x.example/a?b=c#d"},
		{"http://localhost:8080/x", "http://localhost:8080/x"},
		{"  https://x.example  ", "https://x.example"},
	} {
		f := inputForm()
		f.TicketURL = tc.in
		if got := inputMustEvent(t, f, nil).TicketURL; got != tc.want {
			t.Errorf("ticket_url %q = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFormEventTicketURLIsTheStoresRule checks that the form refuses a link with
// the store's own rule and message, so a link the form takes is one the store
// takes, and the other way round.
func TestFormEventTicketURLIsTheStoresRule(t *testing.T) {
	for _, in := range []string{
		"", "https://x.example/a?b=c#d", "http://localhost:8080/x", "https://x.example/a%20b",
		"https://x.example/a b", "https://x.example/a b", "https://x.example/?q=a　b", "http://exa mple.com",
		"x.example/a", "javascript:alert(1)", "ftp://x.example", "https:///x", "https://exa%zzmple.com",
		"https://x.example/\x07", "https://x.example/\xff", "https://x.example/" + strings.Repeat("a", domain.EventURLMax),
	} {
		f := inputForm()
		f.TicketURL = in
		_, fe := f.Event(testNow, testLoc, nil)
		if got, want := fe["ticket_url"], domain.TicketURLProblem(in); got != want {
			t.Errorf("ticket_url %q: the form says %q, the store says %q", in, got, want)
		}
	}
}

func TestFormEventDateRule(t *testing.T) {
	const past = "a data não pode ser anterior a hoje"
	// 01/10/2026, 22:00 in Fortaleza, which is already 02/10 in UTC.
	pastPrev := inputPrev()
	pastPrev.StartsAt = time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)
	futurePrev := inputPrev() // 15/11/2026

	tests := []struct {
		name string
		now  time.Time
		prev *domain.Event
		date string
		want string // the date message, empty when accepted
	}{
		{"create today, though the time of day has passed", testNow, nil, "14/11/2026", ""},
		{"create tomorrow", testNow, nil, "15/11/2026", ""},
		{"create yesterday", testNow, nil, "13/11/2026", past},
		// 14/11 23:00 in Fortaleza, already 15/11 in UTC: the zone decides.
		{"create today in loc, a day behind in UTC", time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC), nil, "14/11/2026", ""},
		{"create yesterday in loc", time.Date(2026, 11, 15, 2, 0, 0, 0, time.UTC), nil, "13/11/2026", past},
		// 13/11 23:00 in Fortaleza, still 14/11 in UTC.
		{"create today in loc, a day ahead in UTC", time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC), nil, "13/11/2026", ""},
		{"create 12/11 when today in loc is 13/11", time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC), nil, "12/11/2026", past},

		{"edit keeps a past date", testNow, &pastPrev, "01/10/2026", ""},
		{"edit keeps a past date, ISO", testNow, &pastPrev, "2026-10-01", ""},
		{"edit changes to another past date", testNow, &pastPrev, "30/09/2026", past},
		{"edit moves to prev's UTC date, which is not its date in loc", testNow, &pastPrev, "02/10/2026", past},
		{"edit moves a past event to today", testNow, &pastPrev, "14/11/2026", ""},
		{"edit moves a past event ahead", testNow, &pastPrev, "20/12/2026", ""},
		{"edit keeps a future date", testNow, &futurePrev, "15/11/2026", ""},
		{"edit moves a future event to the past", testNow, &futurePrev, "13/11/2026", past},
		{"edit moves a future event to today", testNow, &futurePrev, "14/11/2026", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := inputForm()
			if tc.prev != nil {
				f = inputEditForm()
			}
			f.Date = tc.date
			f.Time = "08:00" // only the date matters, not the time of day
			_, fe := f.Event(tc.now, testLoc, tc.prev)
			if tc.want == "" {
				if fe != nil {
					t.Errorf("field errors %v, want none", fe)
				}
				return
			}
			requireFieldErrors(t, fe, "date")
			if fe["date"] != tc.want {
				t.Errorf("date message = %q, want %q", fe["date"], tc.want)
			}
		})
	}
}

func TestFormEventPlace(t *testing.T) {
	const (
		chooseState = "escolha o estado"
		chooseCity  = "escolha a cidade"
		stateFirst  = "escolha primeiro o estado e depois a cidade"
	)
	tests := []struct {
		name, state, city string
		want              domain.FieldErrors
	}{
		{"no state and no city", "", "", domain.FieldErrors{"state": chooseState, "city": chooseCity}},
		{"no state, a city", "", "Fortaleza", domain.FieldErrors{"state": chooseState, "city": stateFirst}},
		{"unknown state, a city", "XX", "Fortaleza", domain.FieldErrors{"state": chooseState, "city": stateFirst}},
		{"lowercase UF", "ce", "Fortaleza", domain.FieldErrors{"state": chooseState, "city": stateFirst}},
		{"unknown state, no city", "XX", "", domain.FieldErrors{"state": chooseState, "city": chooseCity}},
		{"state, no city", "CE", "", domain.FieldErrors{"city": chooseCity}},
		{"state, blank city", "CE", "   ", domain.FieldErrors{"city": chooseCity}},
		{"city of another state", "CE", "Recife", domain.FieldErrors{"city": "escolha uma cidade de CE da lista"}},
		{"city in lowercase", "CE", "fortaleza", domain.FieldErrors{"city": "escolha uma cidade de CE da lista"}},
		{"city with a line break", "CE", "Fortaleza\nCE", domain.FieldErrors{"city": "escolha uma cidade de CE da lista"}},
		{"city of the other state", "PE", "Fortaleza", domain.FieldErrors{"city": "escolha uma cidade de PE da lista"}},
		{"valid pair", "PE", "Recife", nil},
		{"valid pair, padded", " CE ", " Fortaleza ", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := inputForm()
			f.State, f.City = tc.state, tc.city
			got, fe := f.Event(testNow, testLoc, nil)
			if tc.want == nil {
				if fe != nil {
					t.Fatalf("field errors %v, want none", fe)
				}
				if got.State != strings.TrimSpace(tc.state) || got.City != strings.TrimSpace(tc.city) {
					t.Errorf("place = %q/%q, want %q/%q", got.State, got.City, strings.TrimSpace(tc.state), strings.TrimSpace(tc.city))
				}
				return
			}
			if !maps.Equal(fe, tc.want) {
				t.Errorf("field errors %v, want %v", fe, tc.want)
			}
		})
	}
}

func TestFormEventTextsAreTrimmedAndKeepTheirLineBreaks(t *testing.T) {
	f := inputForm()
	f.CollectiveID = "  col-1  "
	f.Name = "  Noite de Teste \t"
	f.TicketURL = " https://ingressos.example/noite "
	f.Venue = "\tGalpão 7  "
	f.Artists = "\r\n  Ana\r\nBeto\rCaio\n\n"
	f.Description = "  Linha 1\r\n\r\nLinha\t3\r\n"
	got := inputMustEvent(t, f, nil)
	want := inputEvent()
	want.Artists = "Ana\nBeto\nCaio"
	want.Description = "Linha 1\n\nLinha\t3"
	got.StartsAt, want.StartsAt = time.Time{}, time.Time{}
	if got != want {
		t.Errorf("event =\n%+v\nwant\n%+v", got, want)
	}
}

func TestFormEventOutrosAndStaleTexts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Form)
		check  func(*domain.Event) string // returns what is wrong, or ""
	}{
		{"type outros keeps its text, trimmed", func(f *Form) { f.Type, f.TypeOther = "outros", "  Sarau " },
			func(e *domain.Event) string {
				if e.Type != domain.EventTypeOutros || e.TypeOther != "Sarau" {
					return "want type outros with type_other Sarau"
				}
				return ""
			}},
		{"stale type_other is dropped", func(f *Form) { f.Type, f.TypeOther = "rave", "Sarau" },
			func(e *domain.Event) string {
				if e.Type != domain.EventTypeRave || e.TypeOther != "" {
					return "want type rave with no type_other"
				}
				return ""
			}},
		{"style outros keeps its text and drops the substyle", func(f *Form) { f.Style, f.StyleOther, f.Substyle = "outros", " Brega funk ", "acid techno" },
			func(e *domain.Event) string {
				if e.Style != domain.StyleOther || e.StyleOther != "Brega funk" || e.Substyle != "" {
					return "want style outros, style_other Brega funk and no substyle"
				}
				return ""
			}},
		{"stale style_other is dropped", func(f *Form) { f.StyleOther = "Brega funk" },
			func(e *domain.Event) string {
				if e.Style != "techno" || e.Substyle != "acid techno" || e.StyleOther != "" {
					return "want style techno, substyle acid techno and no style_other"
				}
				return ""
			}},
		{"a style needs no substyle", func(f *Form) { f.Substyle = "" },
			func(e *domain.Event) string {
				if e.Style != "techno" || e.Substyle != "" {
					return "want style techno and no substyle"
				}
				return ""
			}},
		{"style and substyle are trimmed", func(f *Form) { f.Style, f.Substyle = " techno ", " acid techno " },
			func(e *domain.Event) string {
				if e.Style != "techno" || e.Substyle != "acid techno" {
					return "want style techno and substyle acid techno"
				}
				return ""
			}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := inputForm()
			tc.mutate(&f)
			got := inputMustEvent(t, f, nil)
			if msg := tc.check(&got); msg != "" {
				t.Errorf("%s; got %+v", msg, got)
			}
		})
	}
}

// TestFormEventAcceptsEveryCatalogEntry proves the catalog and the domain limits
// agree: no style, substyle or city the form offers is refused by Validate.
func TestFormEventAcceptsEveryCatalogEntry(t *testing.T) {
	for _, s := range catalog.Styles() {
		for _, sub := range append([]string{""}, s.Substyles...) {
			f := inputForm()
			f.Style, f.Substyle = s.Name, sub
			inputMustEvent(t, f, nil)
		}
	}
	for _, st := range catalog.States() {
		for _, city := range catalog.Cities(st.Code) {
			f := inputForm()
			f.State, f.City = st.Code, city
			inputMustEvent(t, f, nil)
		}
	}
	for _, typ := range catalog.Types() {
		f := inputForm()
		f.Type, f.TypeOther = string(typ), "Sarau"
		inputMustEvent(t, f, nil)
	}
}
