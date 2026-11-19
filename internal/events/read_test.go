package events

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The tests of this file run the views against a store: Month, Day, Mine, Detail
// and FormCollectives. Dates are in testLoc; testNow is Saturday 14/11/2026,
// 13:00 in Fortaleza.

// readInsert stores an event of collective c, created by the world's Owner, that
// starts at the instant at. mutate adjusts it.
func readInsert(t *testing.T, w world, c domain.Collective, name string, at time.Time, mutate ...func(*domain.Event)) domain.Event {
	t.Helper()
	set := func(e *domain.Event) { e.Name, e.StartsAt = name, at.UTC() }
	return insertEvent(t, w.S, c.ID, w.Owner.ID, append([]func(*domain.Event){set}, mutate...)...)
}

// readPlace sets the state and the city of an event.
func readPlace(state, city string) func(*domain.Event) {
	return func(e *domain.Event) { e.State, e.City = state, city }
}

// readDuration sets the length of an event in minutes.
func readDuration(minutes int) func(*domain.Event) {
	return func(e *domain.Event) { e.DurationMin = minutes }
}

// readNames lists the names of the events in items.
func readNames[T any](items []T, name func(T) string) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, name(it))
	}
	return out
}

func readViewName(e domain.EventView) string { return e.Name }
func readDayName(i DayItem) string           { return i.Name }
func readMineName(i MineItem) string         { return i.Name }

// readCellNames lists the names of the events of one day of the grid.
func readCellNames(t *testing.T, v MonthView, key string) []string {
	t.Helper()
	return readNames(calendarCell(t, v, key).Events, readViewName)
}

// readWantNames fails the test unless got is exactly want, in order.
func readWantNames(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestMonthPlacesEventsOnTheirLocalDay(t *testing.T) {
	w := newWorld(t)
	// Four events on 10/11, stored out of order.
	for _, h := range []int{20, 9, 15, 11} {
		readInsert(t, w, w.Alfa, fmt.Sprintf("dia 10 %02dh", h), calendarLocal(2026, time.November, 10, h, 0))
	}
	readInsert(t, w, w.Alfa, "virada", calendarLocal(2026, time.November, 30, 23, 30))        // 01/12 02:30 UTC
	readInsert(t, w, w.Alfa, "fim da grade", calendarLocal(2026, time.December, 5, 22, 0))    // 06/12 01:00 UTC
	readInsert(t, w, w.Alfa, "antes da grade", calendarLocal(2026, time.October, 31, 23, 30)) // 01/11 02:30 UTC
	readInsert(t, w, w.Alfa, "depois da grade", calendarLocal(2026, time.December, 6, 0, 30))
	readInsert(t, w, w.Delta, "bloqueado", calendarLocal(2026, time.November, 21, 21, 0)) // the calendar shows every event

	v, err := Month(t.Context(), w.S, testLoc, calendarFirst(2026, time.November), domain.Place{}, testNow)
	if err != nil {
		t.Fatalf("Month: %v", err)
	}
	if v.Key != "2026-11" || len(v.Weeks) != 5 || v.Truncated {
		t.Fatalf("Key %q, %d weeks, Truncated %v; want 2026-11, 5 weeks, false", v.Key, len(v.Weeks), v.Truncated)
	}
	if !calendarCell(t, v, "2026-11-14").Today {
		t.Error("14/11 is not today")
	}

	readWantNames(t, "10/11", readCellNames(t, v, "2026-11-10"), []string{"dia 10 09h", "dia 10 11h", "dia 10 15h", "dia 10 20h"})
	d10 := calendarCell(t, v, "2026-11-10")
	readWantNames(t, "10/11 shown", readNames(d10.Shown, readViewName), []string{"dia 10 09h", "dia 10 11h", "dia 10 15h"})
	if d10.More != 1 {
		t.Errorf("10/11: More = %d, want 1", d10.More)
	}

	// 23:30 in Fortaleza is 02:30 of the next day in UTC; it stays on 30/11. The
	// grid ends on Saturday 05/12 at the end of that day in Fortaleza, not in UTC.
	readWantNames(t, "30/11", readCellNames(t, v, "2026-11-30"), []string{"virada"})
	readWantNames(t, "05/12", readCellNames(t, v, "2026-12-05"), []string{"fim da grade"})
	d21 := calendarCell(t, v, "2026-11-21")
	readWantNames(t, "21/11", readNames(d21.Events, readViewName), []string{"bloqueado"})
	if d21.Events[0].CollectiveName != "Coletivo Delta" {
		t.Errorf("21/11: collective %q, want Coletivo Delta", d21.Events[0].CollectiveName)
	}
	if got := calendarCount(v); got != 7 {
		t.Errorf("the grid holds %d events, want 7 (four, virada, fim da grade, bloqueado)", got)
	}

	// December's grid starts on 29/11 and holds the events at both ends of it.
	v, err = Month(t.Context(), w.S, testLoc, calendarFirst(2026, time.December), domain.Place{}, testNow)
	if err != nil {
		t.Fatalf("Month December: %v", err)
	}
	readWantNames(t, "30/11 in December's grid", readCellNames(t, v, "2026-11-30"), []string{"virada"})
	readWantNames(t, "05/12 in December's grid", readCellNames(t, v, "2026-12-05"), []string{"fim da grade"})
	readWantNames(t, "06/12 in December's grid", readCellNames(t, v, "2026-12-06"), []string{"depois da grade"})
	if got := calendarCount(v); got != 3 {
		t.Errorf("December's grid holds %d events, want 3", got)
	}
}

// TestMonthTakesAnyInstantOfTheMonth checks that the month argument is read in
// loc: a handler may pass the clock, which is in UTC.
func TestMonthTakesAnyInstantOfTheMonth(t *testing.T) {
	w := newWorld(t)
	for _, tt := range []struct {
		name  string
		month time.Time
		want  string
	}{
		{"first day", calendarFirst(2026, time.November), "2026-11"},
		{"middle of the month", calendarLocal(2026, time.November, 17, 15, 30), "2026-11"},
		{"the clock in UTC", testNow, "2026-11"},
		{"UTC is already December", time.Date(2026, 12, 1, 1, 30, 0, 0, time.UTC), "2026-11"}, // 30/11, 22:30 in Fortaleza
	} {
		v, err := Month(t.Context(), w.S, testLoc, tt.month, domain.Place{}, testNow)
		if err != nil || v.Key != tt.want || !v.Month.Equal(calendarFirst(2026, time.November)) {
			t.Errorf("%s: Month = %q, %v (Month %v); want %s", tt.name, v.Key, err, v.Month, tt.want)
		}
	}
}

func TestMonthFilterAndPlaces(t *testing.T) {
	w := newWorld(t)
	readInsert(t, w, w.Alfa, "fortaleza", calendarLocal(2026, time.November, 20, 12, 0))
	readInsert(t, w, w.Alfa, "sobral", calendarLocal(2026, time.November, 20, 13, 0), readPlace("CE", "Sobral"))
	readInsert(t, w, w.Alfa, "recife", calendarLocal(2026, time.November, 20, 14, 0), readPlace("PE", "Recife"))
	places := []domain.Place{{State: "CE", City: "Fortaleza"}, {State: "CE", City: "Sobral"}, {State: "PE", City: "Recife"}}

	tests := []struct {
		name       string
		filter     domain.Place
		want       []string
		wantFilter domain.Place
	}{
		{"no filter", domain.Place{}, []string{"fortaleza", "sobral", "recife"}, domain.Place{}},
		{"a state", Filter("CE", ""), []string{"fortaleza", "sobral"}, domain.Place{State: "CE"}},
		{"a state and a city", Filter("CE", "Sobral"), []string{"sobral"}, domain.Place{State: "CE", City: "Sobral"}},
		{"another state", Filter("PE", "Recife"), []string{"recife"}, domain.Place{State: "PE", City: "Recife"}},
		{"a state without events", Filter("SP", ""), nil, domain.Place{State: "SP"}},
		{"a city without a state is no city", domain.Place{City: "Sobral"}, []string{"fortaleza", "sobral", "recife"}, domain.Place{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := Month(t.Context(), w.S, testLoc, calendarFirst(2026, time.November), tt.filter, testNow)
			if err != nil {
				t.Fatalf("Month: %v", err)
			}
			readWantNames(t, "20/11", readCellNames(t, v, "2026-11-20"), tt.want)
			if v.Filter != tt.wantFilter {
				t.Errorf("Filter = %+v, want %+v", v.Filter, tt.wantFilter)
			}
			// The options are the places that have events, whatever the filter.
			if !slices.Equal(v.Places, places) {
				t.Errorf("Places = %v, want %v", v.Places, places)
			}
		})
	}
}

// TestMonthAndDayTruncated stores as many events on one day as the store returns
// at most: both views then say they may be missing some.
func TestMonthAndDayTruncated(t *testing.T) {
	w := newWorld(t)
	start := calendarLocal(2026, time.November, 20, 0, 0)
	var first domain.Event
	for i := range domain.ListLimitMax {
		e := readInsert(t, w, w.Alfa, fmt.Sprintf("e%03d", i), start.Add(time.Duration(i)*time.Minute))
		if i == 0 {
			first = e
		}
	}

	v, err := Month(t.Context(), w.S, testLoc, calendarFirst(2026, time.November), domain.Place{}, testNow)
	if err != nil {
		t.Fatalf("Month: %v", err)
	}
	d := calendarCell(t, v, "2026-11-20")
	if !v.Truncated || len(d.Events) != domain.ListLimitMax || len(d.Shown) != 3 || d.More != domain.ListLimitMax-3 {
		t.Errorf("Month: Truncated %v, %d events, %d shown, More %d; want true, %d, 3, %d", v.Truncated, len(d.Events), len(d.Shown), d.More, domain.ListLimitMax, domain.ListLimitMax-3)
	}
	dv, err := Day(t.Context(), w.S, testLoc, start, domain.Place{}, domain.Place{})
	if err != nil {
		t.Fatalf("Day: %v", err)
	}
	if !dv.Truncated || len(dv.Items) != domain.ListLimitMax {
		t.Errorf("Day: Truncated %v, %d items; want true, %d", dv.Truncated, len(dv.Items), domain.ListLimitMax)
	}

	// One event fewer than the limit is not truncated.
	if err := w.S.Repos().Events.Delete(t.Context(), first.ID); err != nil {
		t.Fatalf("delete event: %v", err)
	}
	v, err = Month(t.Context(), w.S, testLoc, calendarFirst(2026, time.November), domain.Place{}, testNow)
	if err != nil || v.Truncated {
		t.Errorf("Month with %d events: Truncated %v, %v; want false", domain.ListLimitMax-1, v.Truncated, err)
	}
	dv, err = Day(t.Context(), w.S, testLoc, start, domain.Place{}, domain.Place{})
	if err != nil || dv.Truncated {
		t.Errorf("Day with %d events: Truncated %v, %v; want false", domain.ListLimitMax-1, dv.Truncated, err)
	}
}

func TestDay(t *testing.T) {
	w := newWorld(t)
	readInsert(t, w, w.Alfa, "noite", calendarLocal(2026, time.November, 20, 22, 0))
	readInsert(t, w, w.Alfa, "manha sobral", calendarLocal(2026, time.November, 20, 9, 0), readPlace("CE", "Sobral"))
	readInsert(t, w, w.Alfa, "tarde recife", calendarLocal(2026, time.November, 20, 15, 0), readPlace("PE", "Recife"))
	readInsert(t, w, w.Delta, "meio-dia pe", calendarLocal(2026, time.November, 20, 12, 0), readPlace("PE", "Fortaleza")) // the city's name, in another state
	readInsert(t, w, w.Alfa, "virada", calendarLocal(2026, time.November, 20, 23, 30))                                    // 21/11 02:30 UTC
	readInsert(t, w, w.Alfa, "ontem", calendarLocal(2026, time.November, 19, 23, 30))
	readInsert(t, w, w.Alfa, "amanha", calendarLocal(2026, time.November, 21, 0, 30)) // 21/11 03:30 UTC

	day := calendarLocal(2026, time.November, 20, 0, 0)
	all := []string{"manha sobral", "meio-dia pe", "tarde recife", "noite", "virada"}
	none := domain.Place{}
	ceFortaleza := domain.Place{State: "CE", City: "Fortaleza"}
	tests := []struct {
		name              string
		day               time.Time
		filter, highlight domain.Place
		want, wantSame    []string
	}{
		{"every event, by start time", day, none, none, all, nil},
		{"any instant of the day", calendarLocal(2026, time.November, 20, 15, 30), none, none, all, nil},
		{"the clock in UTC", time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC), none, none, all, nil}, // 20/11, 22:00 in Fortaleza
		{"a state", day, Filter("CE", ""), none, []string{"manha sobral", "noite", "virada"}, nil},
		{"a state and a city", day, Filter("CE", "Fortaleza"), none, []string{"noite", "virada"}, nil},
		{"another state", day, Filter("PE", ""), none, []string{"meio-dia pe", "tarde recife"}, nil},
		{"a city without a state is no filter", day, domain.Place{City: "Recife"}, none, all, nil},
		{"same city first, then by start time", day, none, ceFortaleza,
			[]string{"noite", "virada", "manha sobral", "meio-dia pe", "tarde recife"}, []string{"noite", "virada"}},
		{"the city's name in another state is another place", day, none, domain.Place{State: "PE", City: "Fortaleza"},
			[]string{"meio-dia pe", "manha sobral", "tarde recife", "noite", "virada"}, []string{"meio-dia pe"}},
		{"a highlight without a city flags nothing", day, none, domain.Place{State: "CE"}, all, nil},
		{"a highlight without a state flags nothing", day, none, domain.Place{City: "Fortaleza"}, all, nil},
		{"a highlight nobody matches", day, none, domain.Place{State: "BA", City: "Salvador"}, all, nil},
		{"filter and highlight", day, Filter("PE", ""), domain.Place{State: "PE", City: "Recife"},
			[]string{"tarde recife", "meio-dia pe"}, []string{"tarde recife"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := Day(t.Context(), w.S, testLoc, tt.day, tt.filter, tt.highlight)
			if err != nil {
				t.Fatalf("Day: %v", err)
			}
			if !v.Date.Equal(day) || v.Date.Location() != testLoc || v.Truncated {
				t.Errorf("Date %v, Truncated %v; want %v, false", v.Date, v.Truncated, day)
			}
			readWantNames(t, "events", readNames(v.Items, readDayName), tt.want)
			var same []string
			for _, it := range v.Items {
				if it.SameCity {
					same = append(same, it.Name)
				}
			}
			readWantNames(t, "same city", same, tt.wantSame)
		})
	}
}

func TestMine(t *testing.T) {
	w := newWorld(t)
	echo := insertCollective(t, w.S, "Coletivo Echo", w.Owner.ID, func(c *domain.Collective) { c.Status = domain.CollectiveStatusPending })
	insertMember(t, w.S, w.Owner.ID, echo.ID, domain.RoleOwner)
	siteAdmin := insertUser(t, w.S, "admin2@agenda.test", func(u *domain.User) { u.IsAdmin = true })
	insertMember(t, w.S, siteAdmin.ID, w.Delta.ID, domain.RoleMember)
	waiting := insertUser(t, w.S, "espera@agenda.test")
	insertMember(t, w.S, waiting.ID, w.Alfa.ID, domain.RoleMember, func(m *domain.Membership) {
		m.Status = domain.MembershipStatusPending
		m.AdminApprovedAt, m.OwnerApprovedAt, m.DecidedAt = nil, nil, nil
	})

	// testNow is 13:00. "termina agora" ends exactly then, so it is over;
	// "termina em um minuto" is still going.
	readInsert(t, w, w.Alfa, "passada", calendarLocal(2026, time.November, 10, 22, 0))
	readInsert(t, w, w.Alfa, "em andamento", calendarLocal(2026, time.November, 14, 10, 0)) // ends 16:00
	readInsert(t, w, w.Alfa, "termina agora", calendarLocal(2026, time.November, 14, 12, 0), readDuration(60))
	readInsert(t, w, w.Alfa, "termina em um minuto", calendarLocal(2026, time.November, 14, 12, 1), readDuration(60))
	readInsert(t, w, w.Alfa, "futura", calendarLocal(2026, time.November, 20, 20, 0))
	readInsert(t, w, w.Delta, "delta futura", calendarLocal(2026, time.November, 25, 20, 0))
	readInsert(t, w, w.Delta, "delta passada", calendarLocal(2026, time.November, 1, 20, 0))
	readInsert(t, w, echo, "eco", calendarLocal(2026, time.November, 22, 20, 0)) // a collective that is not approved: listed, never editable

	tests := []struct {
		name           string
		actor          domain.User
		upcoming, past []string
		roles          map[string]domain.Role // by collective name
		edits          []string               // the collectives whose events the actor may edit
	}{
		{"Administrador of Alfa, Delta and Echo", w.Owner,
			[]string{"em andamento", "termina em um minuto", "futura", "eco", "delta futura"}, []string{"termina agora", "passada", "delta passada"},
			map[string]domain.Role{"Coletivo Alfa": domain.RoleOwner, "Coletivo Delta": domain.RoleOwner, "Coletivo Echo": domain.RoleOwner},
			[]string{"Coletivo Alfa"}}, // Delta is blocked and Echo is not approved
		{"Membro of Alfa", w.Member,
			[]string{"em andamento", "termina em um minuto", "futura"}, []string{"termina agora", "passada"},
			map[string]domain.Role{"Coletivo Alfa": domain.RoleMember}, nil},
		{"the site admin as Membro of a blocked collective", siteAdmin,
			[]string{"delta futura"}, []string{"delta passada"},
			map[string]domain.Role{"Coletivo Delta": domain.RoleMember}, []string{"Coletivo Delta"}},
		{"no membership", w.Outsider, nil, nil, nil, nil},
		{"the site admin without membership", w.Admin, nil, nil, nil, nil},
		{"a pending membership", waiting, nil, nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := Mine(t.Context(), w.S, tt.actor, testNow)
			if err != nil {
				t.Fatalf("Mine: %v", err)
			}
			readWantNames(t, "Upcoming", readNames(v.Upcoming, readMineName), tt.upcoming)
			readWantNames(t, "Past", readNames(v.Past, readMineName), tt.past)
			for _, it := range slices.Concat(v.Upcoming, v.Past) {
				wantEdit := slices.Contains(tt.edits, it.CollectiveName)
				wantBlocked := it.CollectiveName == "Coletivo Delta" // the only collective with its events blocked
				if it.Role != tt.roles[it.CollectiveName] || it.CanEdit != wantEdit || it.Blocked != wantBlocked {
					t.Errorf("%s (%s): Role %q, CanEdit %v, Blocked %v; want %q, %v, %v",
						it.Name, it.CollectiveName, it.Role, it.CanEdit, it.Blocked, tt.roles[it.CollectiveName], wantEdit, wantBlocked)
				}
			}
		})
	}
}

func TestDetail(t *testing.T) {
	w := newWorld(t)
	alfa := readInsert(t, w, w.Alfa, "festa alfa", calendarLocal(2026, time.November, 20, 22, 0))
	delta := readInsert(t, w, w.Delta, "festa delta", calendarLocal(2026, time.November, 21, 22, 0))
	pending := insertUser(t, w.S, "pendente@agenda.test")
	insertMember(t, w.S, pending.ID, w.Alfa.ID, domain.RoleOwner, func(m *domain.Membership) {
		m.Status = domain.MembershipStatusPending
		m.OwnerApprovedAt, m.DecidedAt = nil, nil
	})
	disabled := insertUser(t, w.S, "desligado@agenda.test", func(u *domain.User) { u.Status = domain.UserStatusDisabled })
	insertMember(t, w.S, disabled.ID, w.Alfa.ID, domain.RoleOwner)

	// The people of the blocked collective who are not its approved Administrador
	// and a blocked collective that is not approved: none of them may edit, but only
	// the approved Administrador is refused because of the block.
	pendingOnDelta := insertUser(t, w.S, "pendente-delta@agenda.test")
	insertMember(t, w.S, pendingOnDelta.ID, w.Delta.ID, domain.RoleOwner, func(m *domain.Membership) {
		m.Status = domain.MembershipStatusPending
		m.OwnerApprovedAt, m.DecidedAt = nil, nil
	})
	memberOnDelta := insertUser(t, w.S, "membro-delta@agenda.test")
	insertMember(t, w.S, memberOnDelta.ID, w.Delta.ID, domain.RoleMember)
	disabledOnDelta := insertUser(t, w.S, "desligado-delta@agenda.test", func(u *domain.User) { u.Status = domain.UserStatusDisabled })
	insertMember(t, w.S, disabledOnDelta.ID, w.Delta.ID, domain.RoleOwner)
	echo := insertCollective(t, w.S, "Coletivo Echo", w.Owner.ID, func(c *domain.Collective) {
		c.Status, c.EventsBlocked = domain.CollectiveStatusPending, true
	})
	insertMember(t, w.S, w.Owner.ID, echo.ID, domain.RoleOwner)
	festaEcho := readInsert(t, w, echo, "festa echo", calendarLocal(2026, time.November, 22, 22, 0))

	tests := []struct {
		name            string
		actor           domain.User
		event           domain.Event
		wantEdit        bool
		wantEditBlocked bool // refused by mayEdit only because of the event block
		wantCollctive   string
	}{
		{"Administrador", w.Owner, alfa, true, false, "Coletivo Alfa"},
		{"Administrador of a blocked collective", w.Owner, delta, false, true, "Coletivo Delta"},
		{"Membro", w.Member, alfa, false, false, "Coletivo Alfa"},
		{"outsider", w.Outsider, alfa, false, false, "Coletivo Alfa"},
		{"site admin", w.Admin, alfa, true, false, "Coletivo Alfa"},
		{"site admin, blocked collective", w.Admin, delta, true, false, "Coletivo Delta"},
		{"Membro of a blocked collective", memberOnDelta, delta, false, false, "Coletivo Delta"},
		{"outsider, blocked collective", w.Outsider, delta, false, false, "Coletivo Delta"},
		{"pending Administrador", pending, alfa, false, false, "Coletivo Alfa"},
		{"pending Administrador of a blocked collective", pendingOnDelta, delta, false, false, "Coletivo Delta"},
		{"disabled Administrador", disabled, alfa, false, false, "Coletivo Alfa"},
		{"disabled Administrador of a blocked collective", disabledOnDelta, delta, false, false, "Coletivo Delta"},
		// mayEdit says ErrNotAllowed before it looks at the block, so the refusal is not the block's.
		{"Administrador of a blocked collective that is not approved", w.Owner, festaEcho, false, false, "Coletivo Echo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := Detail(t.Context(), w.S, tt.actor, tt.event.ID)
			if err != nil {
				t.Fatalf("Detail: %v", err)
			}
			if v.ID != tt.event.ID || v.Name != tt.event.Name || v.CollectiveID != tt.event.CollectiveID || v.CollectiveName != tt.wantCollctive {
				t.Errorf("event = %q (%s) of %q, want %q (%s) of %q", v.Name, v.ID, v.CollectiveName, tt.event.Name, tt.event.ID, tt.wantCollctive)
			}
			if v.CanEdit != tt.wantEdit || v.CanDelete != tt.wantEdit || v.EditBlocked != tt.wantEditBlocked {
				t.Errorf("CanEdit %v, CanDelete %v, EditBlocked %v; want %v, %v, %v", v.CanEdit, v.CanDelete, v.EditBlocked, tt.wantEdit, tt.wantEdit, tt.wantEditBlocked)
			}
		})
	}

	t.Run("unknown ID", func(t *testing.T) {
		gone := readInsert(t, w, w.Alfa, "apagada", calendarLocal(2026, time.November, 22, 22, 0))
		if err := w.S.Repos().Events.Delete(t.Context(), gone.ID); err != nil {
			t.Fatalf("delete event: %v", err)
		}
		for _, id := range []string{gone.ID, "nao-existe", ""} {
			if _, err := Detail(t.Context(), w.S, w.Admin, id); err == nil {
				t.Errorf("Detail(%q) succeeded", id)
			} else {
				requireErrorIs(t, err, domain.ErrNotFound)
			}
		}
	})
}

func TestFormCollectives(t *testing.T) {
	w := newWorld(t)
	// Inserted out of order; the form lists them by name, accents and case ignored.
	zeta := insertCollective(t, w.S, "Zeta", w.Owner.ID)
	arvore := insertCollective(t, w.S, "Árvore", w.Owner.ID)
	beta := insertCollective(t, w.S, "beta", w.Owner.ID)
	for _, c := range []domain.Collective{zeta, arvore, beta} {
		insertMember(t, w.S, w.Owner.ID, c.ID, domain.RoleOwner)
	}
	// Collectives of the Owner that the form must not offer.
	notApproved := insertCollective(t, w.S, "Aguardando", w.Owner.ID, func(c *domain.Collective) { c.Status = domain.CollectiveStatusPending })
	insertMember(t, w.S, w.Owner.ID, notApproved.ID, domain.RoleOwner)
	asMember := insertCollective(t, w.S, "Apenas Membro", w.Owner.ID)
	insertMember(t, w.S, w.Owner.ID, asMember.ID, domain.RoleMember)
	awaiting := insertCollective(t, w.S, "Vaga Pedida", w.Owner.ID)
	insertMember(t, w.S, w.Owner.ID, awaiting.ID, domain.RoleOwner, func(m *domain.Membership) {
		m.Status = domain.MembershipStatusPending
		m.OwnerApprovedAt, m.DecidedAt = nil, nil
	})

	siteAdmin := insertUser(t, w.S, "admin2@agenda.test", func(u *domain.User) { u.IsAdmin = true })
	insertMember(t, w.S, siteAdmin.ID, w.Delta.ID, domain.RoleOwner)
	insertMember(t, w.S, siteAdmin.ID, w.Alfa.ID, domain.RoleMember)
	insertMember(t, w.S, siteAdmin.ID, zeta.ID, domain.RoleOwner)
	disabled := insertUser(t, w.S, "desligado@agenda.test", func(u *domain.User) { u.Status = domain.UserStatusDisabled })
	insertMember(t, w.S, disabled.ID, w.Alfa.ID, domain.RoleOwner)

	tests := []struct {
		name  string
		actor domain.User
		want  []CollectiveOption
	}{
		{"Administrador: approved, unblocked, by name", w.Owner, []CollectiveOption{
			{arvore.ID, "Árvore"}, {beta.ID, "beta"}, {w.Alfa.ID, "Coletivo Alfa"}, {zeta.ID, "Zeta"},
		}},
		{"Membro", w.Member, nil},
		{"outsider", w.Outsider, nil},
		{"site admin without membership", w.Admin, nil},
		// mayCreate lets the block pass for the admin, but the form offers only
		// unblocked collectives: Delta is blocked, Zeta is not.
		{"site admin as Administrador of a blocked and an unblocked collective", siteAdmin, []CollectiveOption{{zeta.ID, "Zeta"}}},
		{"disabled account", disabled, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormCollectives(t.Context(), w.S, tt.actor)
			if err != nil {
				t.Fatalf("FormCollectives: %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("FormCollectives = %v, want %v", got, tt.want)
			}
		})
	}
}
