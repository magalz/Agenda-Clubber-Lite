package events

import (
	"context"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Filter is a stub.
func Filter(state, city string) domain.Place { return domain.Place{} }

// ParseDay is a stub.
func ParseDay(s string, loc *time.Location) (time.Time, bool) { return time.Time{}, false }

// ParseMonth is a stub.
func ParseMonth(s string, loc *time.Location) (time.Time, bool) { return time.Time{}, false }

// GridDay is one cell of the month grid.
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

// MonthView is the month grid with its filter.
type MonthView struct {
	Month                           time.Time
	Key, PrevKey, NextKey, TodayKey string
	Weeks                           []Week
	Filter                          domain.Place
	Places                          []domain.Place
	Truncated                       bool
}

// calendarRange is a stub.
func calendarRange(first time.Time) (from, to time.Time) { return time.Time{}, time.Time{} }

// calendarMonth is a stub.
func calendarMonth(first, now time.Time, filter domain.Place, evs []domain.EventView) MonthView {
	return MonthView{}
}

// Month is a stub.
func Month(ctx context.Context, s domain.Store, loc *time.Location, month time.Time, filter domain.Place, now time.Time) (MonthView, error) {
	return MonthView{}, nil
}
