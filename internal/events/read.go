package events

import (
	"context"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// DayItem is an event of the day list.
type DayItem struct {
	domain.EventView
	SameCity bool
}

// DayView is the day list.
type DayView struct {
	Date      time.Time
	Key       string
	Items     []DayItem
	Truncated bool
}

// Day is a stub.
func Day(ctx context.Context, s domain.Store, loc *time.Location, day time.Time, filter, highlight domain.Place) (DayView, error) {
	return DayView{}, nil
}

// MineItem is a row of Meus Eventos.
type MineItem struct {
	domain.EventView
	Role             domain.Role
	CanEdit, Blocked bool
}

// MineView is Meus Eventos.
type MineView struct {
	Upcoming, Past []MineItem
}

// Mine is a stub.
func Mine(ctx context.Context, s domain.Store, actor domain.User, now time.Time) (MineView, error) {
	return MineView{}, nil
}

// DetailView is the event with the actor's rights.
type DetailView struct {
	domain.EventView
	CanEdit, CanDelete, Blocked bool
}

// Detail is a stub.
func Detail(ctx context.Context, s domain.Store, actor domain.User, id string) (DetailView, error) {
	return DetailView{}, nil
}

// CollectiveOption is a collective the event form offers.
type CollectiveOption struct {
	ID, Name string
}

// FormCollectives is a stub.
func FormCollectives(ctx context.Context, s domain.Store, actor domain.User) ([]CollectiveOption, error) {
	return nil, nil
}
