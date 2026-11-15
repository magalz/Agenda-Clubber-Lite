package events

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/catalog"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The month grid and the parsers the calendar pages need for their query strings
// (day=, month=, state=, city=). Days and months are read and shown in the zone
// the caller passes (APP_TZ); the store keeps UTC instants, so the range the grid
// asks for is built in that zone and converted.

const (
	dayKey   = "2006-01-02" // the key of a day, in a URL and in MonthView
	monthKey = "2006-01"    // the key of a month

	// gridShown is how many events a cell lists; the rest are counted in More.
	gridShown = 3
)

// Filter turns the state and city of a query string into the place to filter by:
// the state only when it is a UF of the catalog, the city only when it is a city
// of that state (exact name). Anything else is dropped, so a handler never
// passes the store a filter it would refuse.
func Filter(state, city string) domain.Place {
	state, city = strings.TrimSpace(state), strings.TrimSpace(city)
	if _, ok := catalog.StateName(state); !ok {
		return domain.Place{}
	}
	if !catalog.ValidCity(state, city) {
		return domain.Place{State: state}
	}
	return domain.Place{State: state, City: city}
}

// placeFilter drops a city that has no state, which does not name a place and
// which the store refuses.
func placeFilter(p domain.Place) domain.Place {
	if p.State == "" {
		return domain.Place{}
	}
	return p
}

// ParseDay reads a day of a query string, AAAA-MM-DD or dd/mm/aaaa, as midnight
// in loc.
func ParseDay(s string, loc *time.Location) (time.Time, bool) {
	d, ok := parseDate(strings.TrimSpace(s))
	if !ok {
		return time.Time{}, false
	}
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc), true
}

// ParseMonth reads a month of a query string, AAAA-MM, as the first day of that
// month at midnight in loc.
func ParseMonth(s string, loc *time.Location) (time.Time, bool) {
	t, err := time.Parse(monthKey, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc), true
}

// GridDay is one cell of the month grid. Date is midnight in the grid's zone and
// Key is the day as AAAA-MM-DD, the value of the day= query parameter. Events are
// all the events that start that day, by start time; Shown is the first of them
// that fit the cell and More how many did not.
type GridDay struct {
	Date                 time.Time
	Key                  string
	InMonth, Today, Past bool
	Events               []domain.EventView
	Shown                []domain.EventView
	More                 int
}

// Week is seven days, Sunday to Saturday.
type Week [7]GridDay

// MonthView is the month grid with its filter. Month is its first day; Key,
// PrevKey and NextKey are AAAA-MM for the page's links and TodayKey is the month
// that holds today. Weeks cover the month in whole weeks, so the first and last
// ones hold days of the neighbouring months (InMonth false). Filter is the place
// the events were restricted to and Places every place that has events, for the
// filter's options. Truncated says the store returned its limit, so the grid may
// miss events.
type MonthView struct {
	Month                           time.Time
	Key, PrevKey, NextKey, TodayKey string
	Weeks                           []Week
	Filter                          domain.Place
	Places                          []domain.Place
	Truncated                       bool
}

// calendarRange is the span the grid of the month starting on first covers, in
// first's zone: from the Sunday on or before the first day to the Sunday after
// the Saturday on or after the last day (excluded).
func calendarRange(first time.Time) (from, to time.Time) {
	loc := first.Location()
	y, m, d := first.Date()
	from = time.Date(y, m, d-int(first.Weekday()), 0, 0, 0, 0, loc)
	last := first.AddDate(0, 1, -1)
	ly, lm, ld := last.Date()
	to = time.Date(ly, lm, ld+7-int(last.Weekday()), 0, 0, 0, 0, loc)
	return from, to
}

// calendarMonth builds the grid of the month starting on first (midnight on its
// first day, in the zone to show) from the events the store returned for
// calendarRange, in order of start time. now says which day is today. It does no
// I/O, so the grid is tested without a store.
func calendarMonth(first, now time.Time, filter domain.Place, evs []domain.EventView) MonthView {
	loc := first.Location()
	today := civilDate(now.In(loc))
	from, to := calendarRange(first)

	var days []GridDay
	index := make(map[string]int)
	for d := from; d.Before(to); {
		y, m, dd := d.Date()
		civil := civilDate(d)
		g := GridDay{Date: d, Key: d.Format(dayKey), InMonth: m == first.Month(), Today: civil.Equal(today), Past: civil.Before(today)}
		index[g.Key] = len(days)
		days = append(days, g)
		d = time.Date(y, m, dd+1, 0, 0, 0, 0, loc)
	}
	for _, e := range evs {
		if i, ok := index[e.StartsAt.In(loc).Format(dayKey)]; ok {
			days[i].Events = append(days[i].Events, e)
		}
	}
	for i := range days {
		n := min(len(days[i].Events), gridShown)
		days[i].Shown = days[i].Events[:n]
		days[i].More = len(days[i].Events) - n
	}

	v := MonthView{
		Month:     first,
		Key:       first.Format(monthKey),
		PrevKey:   first.AddDate(0, -1, 0).Format(monthKey),
		NextKey:   first.AddDate(0, 1, 0).Format(monthKey),
		TodayKey:  now.In(loc).Format(monthKey),
		Filter:    filter,
		Truncated: len(evs) >= domain.ListLimitMax,
	}
	for i := 0; i < len(days); i += 7 {
		v.Weeks = append(v.Weeks, Week(days[i:i+7]))
	}
	return v
}

// Month returns the grid of the month that holds the instant month, read in loc,
// with the events that match filter. The store's limit is ListLimitMax; when it
// is reached the view says Truncated. A filter with a city but no state is read as
// no filter.
func Month(ctx context.Context, s domain.Store, loc *time.Location, month time.Time, filter domain.Place, now time.Time) (MonthView, error) {
	m := month.In(loc)
	first := time.Date(m.Year(), m.Month(), 1, 0, 0, 0, 0, loc)
	filter = placeFilter(filter)
	from, to := calendarRange(first)

	repos := s.Repos()
	evs, err := repos.Events.ListBetween(ctx, domain.EventFilter{From: from.UTC(), To: to.UTC(), State: filter.State, City: filter.City}, domain.ListLimitMax)
	if err != nil {
		return MonthView{}, fmt.Errorf("events: month %s: %w", first.Format(monthKey), err)
	}
	places, err := repos.Events.ListPlaces(ctx)
	if err != nil {
		return MonthView{}, fmt.Errorf("events: month %s: places: %w", first.Format(monthKey), err)
	}
	v := calendarMonth(first, now, filter, evs)
	v.Places = places
	return v, nil
}
