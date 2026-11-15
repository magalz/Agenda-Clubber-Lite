package events

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The tests of this file need no database: the parsers, and the grid built from
// the events a store already returned (calendarMonth, calendarRange). The tests
// of Month against a store are in read_test.go.

// calendarLocal is the instant y-mo-d h:mi in testLoc.
func calendarLocal(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, testLoc)
}

// calendarFirst is the first day of the month at 00:00 in testLoc.
func calendarFirst(y int, mo time.Month) time.Time {
	return calendarLocal(y, mo, 1, 0, 0)
}

// calendarEvent is an event view that starts at the instant at.
func calendarEvent(name string, at time.Time) domain.EventView {
	return domain.EventView{Event: domain.Event{ID: name, Name: name, StartsAt: at.UTC()}}
}

// calendarCell returns the cell with that day key, failing the test when the grid
// has none.
func calendarCell(t *testing.T, v MonthView, key string) GridDay {
	t.Helper()
	for _, w := range v.Weeks {
		for _, d := range w {
			if d.Key == key {
				return d
			}
		}
	}
	t.Fatalf("the grid of %s has no day %s", v.Key, key)
	return GridDay{}
}

// calendarCount returns how many events the cells of the grid hold.
func calendarCount(v MonthView) int {
	n := 0
	for _, w := range v.Weeks {
		for _, d := range w {
			n += len(d.Events)
		}
	}
	return n
}

func TestFilter(t *testing.T) {
	tests := []struct {
		name, state, city string
		want              domain.Place
	}{
		{"state and city", "CE", "Fortaleza", domain.Place{State: "CE", City: "Fortaleza"}},
		{"state only", "CE", "", domain.Place{State: "CE"}},
		{"nothing", "", "", domain.Place{}},
		{"not a UF", "XX", "Fortaleza", domain.Place{}},
		{"state in lower case", "ce", "Fortaleza", domain.Place{}},
		{"city of another state", "CE", "Recife", domain.Place{State: "CE"}},
		{"city that does not exist", "CE", "Lugar Nenhum", domain.Place{State: "CE"}},
		{"city in another case", "CE", "fortaleza", domain.Place{State: "CE"}},
		{"city without a state", "", "Fortaleza", domain.Place{}},
		{"spaces around both", " PE ", " Recife ", domain.Place{State: "PE", City: "Recife"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Filter(tt.state, tt.city); got != tt.want {
				t.Errorf("Filter(%q, %q) = %+v, want %+v", tt.state, tt.city, got, tt.want)
			}
		})
	}
}

func TestParseDay(t *testing.T) {
	valid := []struct {
		in   string
		want time.Time
	}{
		{"2026-11-14", calendarLocal(2026, time.November, 14, 0, 0)},
		{"14/11/2026", calendarLocal(2026, time.November, 14, 0, 0)},
		{" 14/11/2026 ", calendarLocal(2026, time.November, 14, 0, 0)},
		{"5/3/2026", calendarLocal(2026, time.March, 5, 0, 0)},
		{"29/02/2028", calendarLocal(2028, time.February, 29, 0, 0)},
		{"2026-12-31", calendarLocal(2026, time.December, 31, 0, 0)},
	}
	for _, tt := range valid {
		got, ok := ParseDay(tt.in, testLoc)
		if !ok || !got.Equal(tt.want) || got.Location() != testLoc {
			t.Errorf("ParseDay(%q) = %v, %v; want %v in %v", tt.in, got, ok, tt.want, testLoc)
		}
	}

	// The day is read in the zone it is given, not in UTC.
	if got, ok := ParseDay("2026-11-14", time.UTC); !ok || !got.Equal(time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("ParseDay in UTC = %v, %v", got, ok)
	}

	for _, in := range []string{"", "garbage", "31/02/2026", "2026-02-30", "29/02/2026", "2026-13-01", "1/13/2026", "14-11-2026", "2026/11/14", "2026-11", "14/11/26", "0/1/2026"} {
		if got, ok := ParseDay(in, testLoc); ok {
			t.Errorf("ParseDay(%q) = %v, true; want false", in, got)
		}
	}
}

func TestParseMonth(t *testing.T) {
	for _, tt := range []struct {
		in   string
		want time.Time
	}{
		{"2026-11", calendarFirst(2026, time.November)},
		{"2027-01", calendarFirst(2027, time.January)},
		{" 2026-02 ", calendarFirst(2026, time.February)},
	} {
		got, ok := ParseMonth(tt.in, testLoc)
		if !ok || !got.Equal(tt.want) || got.Location() != testLoc {
			t.Errorf("ParseMonth(%q) = %v, %v; want %v in %v", tt.in, got, ok, tt.want, testLoc)
		}
	}

	for _, in := range []string{"", "garbage", "2026-13", "2026-00", "2026-11-01", "11/2026", "26-11", "2026-ab"} {
		if got, ok := ParseMonth(in, testLoc); ok {
			t.Errorf("ParseMonth(%q) = %v, true; want false", in, got)
		}
	}
}

// TestCalendarGrid checks the shape of the grid: whole weeks from Sunday to
// Saturday, the first holding day 1 and the last holding the month's last day,
// and the range the grid asks the store for.
func TestCalendarGrid(t *testing.T) {
	type ymd [3]int
	tests := []struct {
		name        string
		month       time.Month
		year, weeks int
		first, last ymd // the grid's first and last day
	}{
		{"November 2026 starts on a Sunday", time.November, 2026, 5, ymd{2026, 11, 1}, ymd{2026, 12, 5}},
		{"August 2026 needs six weeks", time.August, 2026, 6, ymd{2026, 7, 26}, ymd{2026, 9, 5}},
		{"February 2026 fits four", time.February, 2026, 4, ymd{2026, 2, 1}, ymd{2026, 2, 28}},
		{"December 2026 reaches into January", time.December, 2026, 5, ymd{2026, 11, 29}, ymd{2027, 1, 2}},
		{"January 2026 starts in December", time.January, 2026, 5, ymd{2025, 12, 28}, ymd{2026, 1, 31}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first := calendarFirst(tt.year, tt.month)
			v := calendarMonth(first, testNow, domain.Place{}, nil)
			if len(v.Weeks) != tt.weeks {
				t.Fatalf("%d weeks, want %d", len(v.Weeks), tt.weeks)
			}
			if !v.Month.Equal(first) || v.Month.Location() != testLoc {
				t.Errorf("Month = %v, want %v", v.Month, first)
			}
			wantKey := fmt.Sprintf("%04d-%02d", tt.year, int(tt.month))
			if v.Key != wantKey {
				t.Errorf("Key = %q, want %q", v.Key, wantKey)
			}

			from, to := calendarRange(first)
			wantFrom := calendarLocal(tt.first[0], time.Month(tt.first[1]), tt.first[2], 0, 0)
			wantTo := calendarLocal(tt.last[0], time.Month(tt.last[1]), tt.last[2]+1, 0, 0)
			if !from.Equal(wantFrom) || !to.Equal(wantTo) {
				t.Errorf("calendarRange = [%v, %v), want [%v, %v)", from, to, wantFrom, wantTo)
			}

			lastDay := calendarLocal(tt.year, tt.month+1, 0, 0, 0).Day() // the month's number of days
			var prev time.Time
			inMonth, sawFirst, sawLast := 0, false, false
			for i, w := range v.Weeks {
				if w[0].Date.Weekday() != time.Sunday || w[6].Date.Weekday() != time.Saturday {
					t.Errorf("week %d runs %v to %v, want Sunday to Saturday", i, w[0].Date.Weekday(), w[6].Date.Weekday())
				}
				for j, d := range w {
					y, m, day := d.Date.Date()
					if !d.Date.Equal(calendarLocal(y, m, day, 0, 0)) || d.Date.Location() != testLoc {
						t.Errorf("week %d day %d: Date = %v, want 00:00 in %v", i, j, d.Date, testLoc)
					}
					if want := fmt.Sprintf("%04d-%02d-%02d", y, int(m), day); d.Key != want {
						t.Errorf("week %d day %d: Key = %q, want %q", i, j, d.Key, want)
					}
					if !prev.IsZero() && !d.Date.Equal(calendarLocal(prev.Year(), prev.Month(), prev.Day()+1, 0, 0)) {
						t.Errorf("week %d day %d: %v does not follow %v", i, j, d.Date, prev)
					}
					prev = d.Date
					if d.InMonth != (m == tt.month) {
						t.Errorf("%s: InMonth = %v", d.Key, d.InMonth)
					}
					if d.InMonth {
						inMonth++
						sawFirst = sawFirst || day == 1
						sawLast = sawLast || day == lastDay
					}
				}
			}
			if inMonth != lastDay {
				t.Errorf("%d days in the month, want %d", inMonth, lastDay)
			}
			if !sawFirst || !sawLast {
				t.Errorf("the grid misses the month's first (%v) or last (%v) day", sawFirst, sawLast)
			}
			if v.Weeks[0][0].Key != fmt.Sprintf("%04d-%02d-%02d", tt.first[0], tt.first[1], tt.first[2]) ||
				v.Weeks[len(v.Weeks)-1][6].Key != fmt.Sprintf("%04d-%02d-%02d", tt.last[0], tt.last[1], tt.last[2]) {
				t.Errorf("the grid runs %s to %s", v.Weeks[0][0].Key, v.Weeks[len(v.Weeks)-1][6].Key)
			}
		})
	}
}

// TestCalendarFlags checks InMonth, Today and Past against the clock of the
// tests, Saturday 14/11/2026 13:00 in Fortaleza.
func TestCalendarFlags(t *testing.T) {
	v := calendarMonth(calendarFirst(2026, time.November), testNow, domain.Place{}, nil)
	past, today := 0, 0
	for _, w := range v.Weeks {
		for _, d := range w {
			wantPast := d.Key < "2026-11-14"
			if d.Past != wantPast {
				t.Errorf("%s: Past = %v, want %v", d.Key, d.Past, wantPast)
			}
			if d.Today != (d.Key == "2026-11-14") {
				t.Errorf("%s: Today = %v", d.Key, d.Today)
			}
			if d.Past {
				past++
			}
			if d.Today {
				today++
			}
			if d.InMonth != (d.Key < "2026-12") {
				t.Errorf("%s: InMonth = %v", d.Key, d.InMonth)
			}
		}
	}
	if past != 13 || today != 1 {
		t.Errorf("%d past days and %d today, want 13 and 1", past, today)
	}
	if d := calendarCell(t, v, "2026-12-05"); d.InMonth || d.Past || d.Today {
		t.Errorf("2026-12-05 = %+v, want outside the month and in the future", d)
	}

	// A month that is over: every day is past, and the days of the neighbouring
	// months are flagged as outside it.
	v = calendarMonth(calendarFirst(2026, time.August), testNow, domain.Place{}, nil)
	for _, key := range []string{"2026-07-26", "2026-07-31", "2026-08-01", "2026-08-31", "2026-09-05"} {
		if d := calendarCell(t, v, key); !d.Past || d.Today {
			t.Errorf("%s: Past = %v, Today = %v, want past and not today", key, d.Past, d.Today)
		}
	}
	if d := calendarCell(t, v, "2026-07-26"); d.InMonth {
		t.Error("2026-07-26 is in the month")
	}
	if d := calendarCell(t, v, "2026-08-01"); !d.InMonth {
		t.Error("2026-08-01 is outside the month")
	}

	// A month to come: nothing is past.
	v = calendarMonth(calendarFirst(2027, time.March), testNow, domain.Place{}, nil)
	if d := calendarCell(t, v, "2027-03-01"); d.Past || d.Today {
		t.Errorf("2027-03-01 = %+v, want in the future", d)
	}
}

// TestCalendarKeys checks the keys of the links, across a year boundary, and that
// today is the date in the grid's zone, not in UTC.
func TestCalendarKeys(t *testing.T) {
	lateNight := time.Date(2026, 12, 1, 1, 30, 0, 0, time.UTC) // Monday 30/11, 22:30 in Fortaleza
	tests := []struct {
		name                   string
		first, now             time.Time
		key, prev, next, today string
		todayCell              string // a cell of the grid that is today, if the grid has one
		notTodayCell           string // a cell that is not
	}{
		{"November 2026", calendarFirst(2026, time.November), testNow, "2026-11", "2026-10", "2026-12", "2026-11", "2026-11-14", "2026-11-13"},
		{"December into January", calendarFirst(2026, time.December), testNow, "2026-12", "2026-11", "2027-01", "2026-11", "", "2026-12-01"},
		{"January back to December", calendarFirst(2026, time.January), testNow, "2026-01", "2025-12", "2026-02", "2026-11", "", "2026-01-01"},
		{"UTC is already December", calendarFirst(2026, time.November), lateNight, "2026-11", "2026-10", "2026-12", "2026-11", "2026-11-30", "2026-12-01"},
		{"December grid on that night", calendarFirst(2026, time.December), lateNight, "2026-12", "2026-11", "2027-01", "2026-11", "2026-11-30", "2026-12-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := calendarMonth(tt.first, tt.now, domain.Place{}, nil)
			if v.Key != tt.key || v.PrevKey != tt.prev || v.NextKey != tt.next || v.TodayKey != tt.today {
				t.Errorf("keys = %s, %s, %s, %s; want %s, %s, %s, %s", v.Key, v.PrevKey, v.NextKey, v.TodayKey, tt.key, tt.prev, tt.next, tt.today)
			}
			if tt.todayCell != "" && !calendarCell(t, v, tt.todayCell).Today {
				t.Errorf("%s is not today", tt.todayCell)
			}
			if calendarCell(t, v, tt.notTodayCell).Today {
				t.Errorf("%s is today", tt.notTodayCell)
			}
		})
	}
}

// TestCalendarEvents checks where the grid puts the events it is given: on the
// day they start in its zone, in the order given, three shown and the rest as
// More.
func TestCalendarEvents(t *testing.T) {
	var evs []domain.EventView
	for _, h := range []int{9, 11, 15, 20} {
		evs = append(evs, calendarEvent(fmt.Sprintf("dia 10 %02dh", h), calendarLocal(2026, time.November, 10, h, 0)))
	}
	for _, h := range []int{8, 12, 18} {
		evs = append(evs, calendarEvent(fmt.Sprintf("dia 12 %02dh", h), calendarLocal(2026, time.November, 12, h, 0)))
	}
	evs = append(evs,
		calendarEvent("virada", calendarLocal(2026, time.November, 30, 23, 30)), // 02:30 UTC on 1 December
		calendarEvent("madrugada", calendarLocal(2026, time.December, 1, 0, 30)),
	)

	v := calendarMonth(calendarFirst(2026, time.November), testNow, domain.Place{}, evs)
	names := func(d GridDay) []string {
		var out []string
		for _, e := range d.Events {
			out = append(out, e.Name)
		}
		return out
	}

	d10 := calendarCell(t, v, "2026-11-10")
	if got, want := names(d10), []string{"dia 10 09h", "dia 10 11h", "dia 10 15h", "dia 10 20h"}; !slices.Equal(got, want) {
		t.Errorf("2026-11-10 holds %v, want %v", got, want)
	}
	var shown []string
	for _, e := range d10.Shown {
		shown = append(shown, e.Name)
	}
	if want := []string{"dia 10 09h", "dia 10 11h", "dia 10 15h"}; !slices.Equal(shown, want) || d10.More != 1 {
		t.Errorf("2026-11-10: Shown %v, More %d; want %v and 1", shown, d10.More, want)
	}

	if d := calendarCell(t, v, "2026-11-12"); len(d.Events) != 3 || len(d.Shown) != 3 || d.More != 0 {
		t.Errorf("2026-11-12: %d events, %d shown, More %d; want 3, 3 and 0", len(d.Events), len(d.Shown), d.More)
	}
	if d := calendarCell(t, v, "2026-11-11"); len(d.Events) != 0 || len(d.Shown) != 0 || d.More != 0 {
		t.Errorf("2026-11-11 = %+v, want no events", d)
	}

	// 23:30 in Fortaleza is already the next day in UTC; it stays on its own day.
	if got := names(calendarCell(t, v, "2026-11-30")); !slices.Equal(got, []string{"virada"}) {
		t.Errorf("2026-11-30 holds %v, want [virada]", got)
	}
	if got := names(calendarCell(t, v, "2026-12-01")); !slices.Equal(got, []string{"madrugada"}) {
		t.Errorf("2026-12-01 holds %v, want [madrugada]", got)
	}
	if got := calendarCount(v); got != len(evs) {
		t.Errorf("the grid holds %d events, want %d", got, len(evs))
	}
}

func TestCalendarTruncated(t *testing.T) {
	at := calendarLocal(2026, time.November, 20, 12, 0)
	for _, tt := range []struct {
		n    int
		want bool
	}{{0, false}, {domain.ListLimitMax - 1, false}, {domain.ListLimitMax, true}} {
		evs := make([]domain.EventView, tt.n)
		for i := range evs {
			evs[i] = calendarEvent(fmt.Sprint(i), at)
		}
		if got := calendarMonth(calendarFirst(2026, time.November), testNow, domain.Place{}, evs).Truncated; got != tt.want {
			t.Errorf("%d events: Truncated = %v, want %v", tt.n, got, tt.want)
		}
	}
}

// TestCalendarFilterKept checks that the view carries the filter it was built for.
func TestCalendarFilterKept(t *testing.T) {
	f := domain.Place{State: "CE", City: "Fortaleza"}
	if got := calendarMonth(calendarFirst(2026, time.November), testNow, f, nil).Filter; got != f {
		t.Errorf("Filter = %+v, want %+v", got, f)
	}
}
