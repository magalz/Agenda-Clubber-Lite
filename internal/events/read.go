package events

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// The views of the pages that list or show events (the month grid is in
// calendar.go). Each one decides the rights and flags a page shows with the checks
// of permissions.go, so a page offers exactly what the write flows allow.

// DayItem is an event of the day list. SameCity says it is held in the city the
// list was asked to highlight.
type DayItem struct {
	domain.EventView
	SameCity bool
}

// DayView is the day list ("Analisar Data"): the events of one day. Date is
// midnight of the day in the zone it was read in. Truncated says the store
// returned its limit, so the list may miss events.
type DayView struct {
	Date      time.Time
	Items     []DayItem
	Truncated bool
}

// Day returns the events that start on the day that holds the instant day, read
// in loc, by start time and restricted to filter when it names a state. With a
// highlight that names a state and a city, the events held there come first, each
// flagged SameCity, and the rest follow; both groups stay in order of start time.
// A filter or a highlight with a city but no state names no place.
func Day(ctx context.Context, s domain.Store, loc *time.Location, day time.Time, filter, highlight domain.Place) (DayView, error) {
	d := day.In(loc)
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
	end := time.Date(d.Year(), d.Month(), d.Day()+1, 0, 0, 0, 0, loc)
	filter, highlight = placeFilter(filter), placeFilter(highlight)

	evs, err := s.Repos().Events.ListBetween(ctx, domain.EventFilter{From: start.UTC(), To: end.UTC(), State: filter.State, City: filter.City}, domain.ListLimitMax)
	if err != nil {
		return DayView{}, fmt.Errorf("events: day %s: %w", start.Format(dayKey), err)
	}
	v := DayView{Date: start, Truncated: len(evs) >= domain.ListLimitMax}
	var rest []DayItem
	for _, e := range evs {
		item := DayItem{EventView: e, SameCity: highlight.City != "" && e.State == highlight.State && e.City == highlight.City}
		if item.SameCity {
			v.Items = append(v.Items, item)
		} else {
			rest = append(rest, item)
		}
	}
	v.Items = append(v.Items, rest...)
	return v, nil
}

// MineItem is a row of Meus Eventos: the event, the actor's role in its
// collective and whether the row is editable. Blocked says the collective's
// events are blocked, which is why an Administrador's row is read-only.
type MineItem struct {
	domain.EventView
	Role             domain.Role
	CanEdit, Blocked bool
}

// MineView is Meus Eventos: Upcoming is ascending by start time, Past descending.
// Truncated says the store returned its limit, so the lists may miss events.
type MineView struct {
	Upcoming, Past []MineItem
	Truncated      bool
}

// Mine returns the events of every collective where actor has an approved
// membership, Administrador or Membro, whatever the collective's own status.
// An event that ends after now is Upcoming (one in progress included), any other
// is Past. CanEdit is what mayEdit says for the actor and the collective.
func Mine(ctx context.Context, s domain.Store, actor domain.User, now time.Time) (MineView, error) {
	repos := s.Repos()
	memberships, err := repos.Memberships.ListByUser(ctx, actor.ID)
	if err != nil {
		return MineView{}, fmt.Errorf("events: mine: memberships: %w", err)
	}
	approved := make(map[string]domain.MembershipView)
	var ids []string
	for _, m := range memberships {
		if m.Status == domain.MembershipStatusApproved {
			approved[m.CollectiveID] = m
			ids = append(ids, m.CollectiveID)
		}
	}
	if len(ids) == 0 {
		return MineView{}, nil
	}

	// Latest start first, which is the order of Past.
	evs, err := repos.Events.ListByCollectives(ctx, ids, domain.ListLimitMax)
	if err != nil {
		return MineView{}, fmt.Errorf("events: mine: events: %w", err)
	}
	var v MineView
	for _, e := range evs {
		m := approved[e.CollectiveID]
		membership := m.Membership
		// mayEdit reads only the collective's status and its block, which the
		// membership list already carries.
		c := domain.Collective{Status: m.CollectiveStatus, EventsBlocked: m.CollectiveEventsBlocked}
		item := MineItem{EventView: e, Role: m.Role, CanEdit: mayEdit(actor, c, &membership) == nil, Blocked: m.CollectiveEventsBlocked}
		if e.StartsAt.Add(time.Duration(e.DurationMin) * time.Minute).After(now) {
			v.Upcoming = append(v.Upcoming, item)
		} else {
			v.Past = append(v.Past, item)
		}
	}
	slices.Reverse(v.Upcoming)
	return v, nil
}

// DetailView is the event with the actor's rights over it. CanDelete is CanEdit:
// the same people do both. EditBlocked says mayEdit refused the actor only because
// of the event block, so a page can say why an Administrador may not edit.
type DetailView struct {
	domain.EventView
	CanEdit, CanDelete, EditBlocked bool
}

// Detail returns the event id with what actor may do to it, or an error that
// matches domain.ErrNotFound when there is no such event.
func Detail(ctx context.Context, s domain.Store, actor domain.User, id string) (DetailView, error) {
	repos := s.Repos()
	e, err := repos.Events.ByID(ctx, id)
	if err != nil {
		return DetailView{}, fmt.Errorf("events: detail: %w", err)
	}
	c, err := repos.Collectives.ByID(ctx, e.CollectiveID)
	if err != nil {
		return DetailView{}, fmt.Errorf("events: detail: collective: %w", err)
	}
	var m *domain.Membership
	switch membership, err := repos.Memberships.Get(ctx, actor.ID, c.ID); {
	case err == nil:
		m = &membership
	case !errors.Is(err, domain.ErrNotFound):
		return DetailView{}, fmt.Errorf("events: detail: membership: %w", err)
	}
	err = mayEdit(actor, c, m)
	return DetailView{EventView: e, CanEdit: err == nil, CanDelete: err == nil, EditBlocked: errors.Is(err, ErrBlocked)}, nil
}

// CollectiveOption is a collective the event form offers.
type CollectiveOption struct {
	ID, Name string
}

// FormCollectives returns the collectives actor may create an event in, by name:
// an approved Administrador of an approved collective whose events are not
// blocked. mayCreate decides the right; the block is checked here too because
// mayCreate lets it pass for the site admin, and the form never offers a blocked
// collective.
func FormCollectives(ctx context.Context, s domain.Store, actor domain.User) ([]CollectiveOption, error) {
	// ListByUser returns the memberships in ascending order of the collective's name.
	memberships, err := s.Repos().Memberships.ListByUser(ctx, actor.ID)
	if err != nil {
		return nil, fmt.Errorf("events: form collectives: %w", err)
	}
	var out []CollectiveOption
	for _, m := range memberships {
		membership := m.Membership
		c := domain.Collective{Status: m.CollectiveStatus, EventsBlocked: m.CollectiveEventsBlocked}
		if !m.CollectiveEventsBlocked && mayCreate(actor, c, &membership) == nil {
			out = append(out, CollectiveOption{ID: m.CollectiveID, Name: m.CollectiveName})
		}
	}
	return out, nil
}
