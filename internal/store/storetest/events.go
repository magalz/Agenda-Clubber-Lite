package storetest

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// normEvent keeps whole milliseconds in UTC, so DeepEqual can compare.
func normEvent(ev domain.Event) domain.Event {
	ev.StartsAt = ms(ev.StartsAt)
	ev.CreatedAt = ms(ev.CreatedAt)
	ev.UpdatedAt = ms(ev.UpdatedAt)
	return ev
}

// subMilli is a time of day that a backend with millisecond precision has to
// cut, in a zone that is not UTC.
func subMilli(d time.Duration) time.Time {
	return inLocalZone(at(d + 123*time.Millisecond + 456*time.Microsecond))
}

// eventView reads the event and ends the subtest when it cannot.
func (e env) eventView(what, id string) domain.EventView {
	e.t.Helper()
	v, err := e.r.Events.ByID(e.ctx, id)
	noErr(e.t, what, err)
	return v
}

// wantStoredEvent requires that the stored event is want, with its times in
// UTC, and that its collective is named collectiveName.
func (e env) wantStoredEvent(what string, want domain.Event, collectiveName string) {
	e.t.Helper()
	got := e.eventView("ByID for "+what, want.ID)
	wantEqual(e.t, what, normEvent(got.Event), normEvent(want))
	wantUTC(e.t, what+": StartsAt", &got.StartsAt, want.StartsAt)
	wantUTC(e.t, what+": CreatedAt", &got.CreatedAt, want.CreatedAt)
	wantUTC(e.t, what+": UpdatedAt", &got.UpdatedAt, want.UpdatedAt)
	if got.CollectiveName != collectiveName {
		e.t.Errorf("%s: CollectiveName = %q, want %q", what, got.CollectiveName, collectiveName)
	}
}

// normEventView keeps whole milliseconds in UTC in the event of the view.
func normEventView(v domain.EventView) domain.EventView {
	v.Event = normEvent(v.Event)
	return v
}

// eventAt, eventNamed and eventIn set the start, the name and the place of a
// fixture. A list test gives each event its own name, so a failure shows which.
func eventAt(d time.Duration) func(*domain.Event) {
	return func(ev *domain.Event) { ev.StartsAt = at(d) }
}

func eventNamed(name string) func(*domain.Event) {
	return func(ev *domain.Event) { ev.Name = name }
}

func eventIn(state, city string) func(*domain.Event) {
	return func(ev *domain.Event) { ev.State, ev.City = state, city }
}

// listBetween and listByCollectives end the subtest when the call fails.
func (e env) listBetween(what string, f domain.EventFilter, limit int) []domain.EventView {
	e.t.Helper()
	got, err := e.r.Events.ListBetween(e.ctx, f, limit)
	noErr(e.t, what, err)
	return got
}

func (e env) listByCollectives(what string, ids []string, limit int) []domain.EventView {
	e.t.Helper()
	got, err := e.r.Events.ListByCollectives(e.ctx, ids, limit)
	noErr(e.t, what, err)
	return got
}

func (e env) listPlaces(what string) []domain.Place {
	e.t.Helper()
	got, err := e.r.Events.ListPlaces(e.ctx)
	noErr(e.t, what, err)
	return got
}

// wantEvents ends the subtest unless the views are the events want, in this order.
func wantEvents(t *testing.T, what string, got []domain.EventView, want ...domain.Event) {
	t.Helper()
	label := func(v domain.EventView) string { return v.Name + " " + v.ID }
	views := make([]domain.EventView, len(want))
	for i := range want {
		views[i].Event = want[i]
	}
	wantIDsBy(t, what, label, got, views...)
}

// sortedByID returns the events with the smallest ID first, or the greatest
// first when desc is set: the order the lists give events that start together.
func sortedByID(desc bool, events ...domain.Event) []domain.Event {
	sorted := slices.Clone(events)
	slices.SortFunc(sorted, func(a, b domain.Event) int {
		if desc {
			return cmp.Compare(b.ID, a.ID)
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return sorted
}

// wantDistinctIDs ends the subtest when two of the events share an ID: a tie
// the lists break by ID needs IDs that differ.
func wantDistinctIDs(t *testing.T, events ...domain.Event) {
	t.Helper()
	seen := map[string]bool{}
	for _, ev := range events {
		if ev.ID == "" || seen[ev.ID] {
			t.Fatalf("the ID %q of %s is empty or repeated, want a different one for each event", ev.ID, ev.Name)
		}
		seen[ev.ID] = true
	}
}

// ownTypeAndCatalogStyle sets every optional field except StyleOther: a kind the
// catalog does not list, a style with its substyle, a ticket URL, a venue, the
// artists on several lines and a description, edited by editor.
func ownTypeAndCatalogStyle(editor string) func(*domain.Event) {
	return func(ev *domain.Event) {
		ev.Type, ev.TypeOther = domain.EventTypeOutros, "Sarau"
		ev.StartsAt = subMilli(72 * time.Hour)
		ev.DurationMin = 90
		ev.TicketURL = "https://example.com/ingressos?lote=1"
		ev.Style, ev.Substyle = "Techno", "Minimal"
		ev.State, ev.City = "PE", "Recife"
		ev.Venue = "Casa da Música"
		ev.Artists = "Ana Luz\nBia Sol\n\tCleo"
		ev.Description = "Primeira linha.\nSegunda linha."
		ev.UpdatedBy = editor
		ev.UpdatedAt = subMilli(2 * time.Hour)
		ev.CreatedAt = subMilli(time.Hour)
	}
}

// ownStyleAndFree is the other shape: a free event of a catalog kind and of a
// style the organizer typed (so with no substyle), with a venue and no URL.
func ownStyleAndFree(editor string) func(*domain.Event) {
	return func(ev *domain.Event) {
		ev.Type, ev.TypeOther = domain.EventTypeFestival, ""
		ev.StartsAt = subMilli(100 * time.Hour)
		ev.DurationMin = domain.EventDurationMax
		ev.Free, ev.PriceCents, ev.TicketURL = true, 0, ""
		ev.Style, ev.Substyle, ev.StyleOther = domain.StyleOther, "", "Brega funk"
		ev.State, ev.City = "CE", "Sobral"
		ev.Venue, ev.Artists, ev.Description = "Praça", "Dona Ana", "Livre."
		ev.UpdatedBy = editor
		ev.UpdatedAt = subMilli(3 * time.Hour)
	}
}

// clearOptionals empties every optional field, as the shape rules allow.
func clearOptionals(ev *domain.Event) {
	ev.Type, ev.TypeOther = domain.EventTypeFesta, ""
	ev.TicketURL = ""
	ev.Style, ev.Substyle, ev.StyleOther = "Techno", "", ""
	ev.Venue, ev.Artists, ev.Description = "", "", ""
}

// invalidEvents are events that Event.Validate refuses, each by the field named.
var invalidEvents = []struct {
	what, field string
	mutate      func(*domain.Event)
}{
	{"no collective", "collective_id", func(ev *domain.Event) { ev.CollectiveID = "" }},
	{"an empty name", "name", func(ev *domain.Event) { ev.Name = "" }},
	{"a one-letter name", "name", func(ev *domain.Event) { ev.Name = "a" }},
	{"a long name", "name", func(ev *domain.Event) { ev.Name = strings.Repeat("a", domain.EventNameMax+1) }},
	{"a type that is not defined", "type", func(ev *domain.Event) { ev.Type = "bogus" }},
	{"TypeOther without the type outros", "type_other", func(ev *domain.Event) { ev.TypeOther = "Sarau" }},
	{"the type outros without TypeOther", "type_other", func(ev *domain.Event) { ev.Type = domain.EventTypeOutros }},
	{"no start", "starts_at", func(ev *domain.Event) { ev.StartsAt = time.Time{} }},
	{"a duration under the minimum", "duration_min", func(ev *domain.Event) { ev.DurationMin = domain.EventDurationMin - 1 }},
	{"a duration over a day", "duration_min", func(ev *domain.Event) { ev.DurationMin = domain.EventDurationMax + 1 }},
	{"a free event with a price", "price_cents", func(ev *domain.Event) { ev.Free = true }},
	{"a paid event with no price", "price_cents", func(ev *domain.Event) { ev.PriceCents = 0 }},
	{"a ticket URL that is not http", "ticket_url", func(ev *domain.Event) { ev.TicketURL = "ftp://example.com/x" }},
	{"no style", "style", func(ev *domain.Event) { ev.Style = "" }},
	{"a substyle with the style outros", "substyle", func(ev *domain.Event) {
		ev.Style, ev.StyleOther, ev.Substyle = domain.StyleOther, "Brega funk", "Minimal"
	}},
	{"the style outros without StyleOther", "style_other", func(ev *domain.Event) { ev.Style = domain.StyleOther }},
	{"a state in lowercase", "state", func(ev *domain.Event) { ev.State = "ce" }},
	{"no city", "city", func(ev *domain.Event) { ev.City = "" }},
	{"a long venue", "venue", func(ev *domain.Event) { ev.Venue = strings.Repeat("a", domain.EventVenueMax+1) }},
	{"a control character in the artists", "artists", func(ev *domain.Event) { ev.Artists = "Ana\x00Bia" }},
	{"a long description", "description", func(ev *domain.Event) { ev.Description = strings.Repeat("a", domain.EventDescriptionMax+1) }},
	{"no CreatedAt", "created_at", func(ev *domain.Event) { ev.CreatedAt = time.Time{} }},
}

// RunEvents runs the contract of domain.Events: Create, ByID, Update and Delete,
// then the lists ListBetween, ListByCollectives and ListPlaces.
func RunEvents(t *testing.T, newStore NewStore) {
	t.Helper()
	runSubtests(t, newStore, []subtest{
		{"Create assigns an ID and Version 1, ignores the ID and Version the caller set and keeps every other field", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			seen := map[string]bool{}
			for _, shape := range []struct {
				what   string
				mutate func(*domain.Event)
			}{
				{"own type and catalog style", ownTypeAndCatalogStyle(bia.ID)},
				{"own style and free", ownStyleAndFree(bia.ID)},
			} {
				ev := newEvent(c.ID, ana.ID, shape.mutate, func(ev *domain.Event) {
					ev.ID, ev.Version = "chosen-by-the-caller", 7
				})
				noErr(t, "Create "+shape.what, e.r.Events.Create(e.ctx, &ev))
				if ev.ID == "" || ev.ID == "chosen-by-the-caller" || seen[ev.ID] {
					t.Fatalf("%s: ID = %q, want a new one assigned by the store", shape.what, ev.ID)
				}
				seen[ev.ID] = true
				if ev.Version != 1 {
					t.Errorf("%s: Version = %d after Create, want 1", shape.what, ev.Version)
				}
				e.wantStoredEvent(shape.what, ev, c.Name)
			}
		}},
		{"Create stores an event without the optional fields as one that has none", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			ev := e.event(c.ID, ana.ID)
			if ev.ID == "" || ev.Version != 1 {
				t.Fatalf("ID = %q, Version = %d, want an ID and 1", ev.ID, ev.Version)
			}
			got := e.eventView("ByID", ev.ID)
			if got.TypeOther != "" || got.TicketURL != "" || got.Substyle != "" || got.StyleOther != "" ||
				got.Venue != "" || got.Artists != "" || got.Description != "" || got.UpdatedBy != "" {
				t.Errorf("an optional field is not empty: %s", show(got.Event))
			}
			e.wantStoredEvent("the stored event", ev, c.Name)
		}},
		{"Create keeps texts of the greatest length, in runes", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			ev := e.event(c.ID, ana.ID, func(ev *domain.Event) {
				ev.Name = strings.Repeat("ç", domain.EventNameMax)
				ev.Style = strings.Repeat("é", domain.EventStyleMax)
				ev.Substyle = strings.Repeat("ã", domain.EventStyleMax)
				ev.City = strings.Repeat("ô", domain.EventCityMax)
				ev.Venue = strings.Repeat("ü", domain.EventVenueMax)
				ev.Artists = strings.Repeat("→", domain.EventArtistsMax)
				ev.Description = strings.Repeat("ñ", domain.EventDescriptionMax)
				ev.TicketURL = "https://example.com/" + strings.Repeat("a", domain.EventURLMax-len("https://example.com/"))
				ev.PriceCents = domain.EventPriceMax
			})
			e.wantStoredEvent("the stored event", ev, c.Name)
		}},
		{"Create does not look at the status or the blocks of the collective", func(t *testing.T, e env) {
			ana := e.user("ana")
			for i, tc := range []struct {
				status domain.CollectiveStatus
				blocks bool
			}{
				{cPending, false}, {cRejected, false}, {cApproved, true}, {cPending, true},
			} {
				c := e.collective(fmt.Sprintf("Bloco %d", i), coletivo, tc.status, func(c *domain.Collective) { c.EventsBlocked = tc.blocks })
				ev := e.event(c.ID, ana.ID)
				e.wantStoredEvent(fmt.Sprintf("the event of a %s collective with blocks=%v", tc.status, tc.blocks), ev, c.Name)
			}
		}},
		{"Create validates the event before it looks at the references", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			for _, tc := range invalidEvents {
				ev := newEvent(c.ID, ana.ID, tc.mutate)
				wantFieldErrors(t, "Create with "+tc.what, e.r.Events.Create(e.ctx, &ev), tc.field)

				// A reference that does not exist does not hide the FieldErrors.
				ev = newEvent(c.ID, ana.ID, tc.mutate, func(ev *domain.Event) {
					if ev.CollectiveID != "" {
						ev.CollectiveID = "0123456789abcdef01234567"
					}
					ev.CreatedBy, ev.UpdatedBy = "", "not-an-id"
				})
				wantFieldErrors(t, "Create with "+tc.what+" and references that do not exist", e.r.Events.Create(e.ctx, &ev), tc.field)
			}
		}},
		{"Create is ErrNotFound for a collective, a creator or an editor that does not exist", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			gone := e.collective("Bloco Extinto", coletivo, cApproved)
			noErr(t, "delete the collective", e.r.Collectives.Delete(e.ctx, gone.ID))
			dead := e.user("dead")
			noErr(t, "delete the user", e.r.Users.Delete(e.ctx, dead.ID))

			try := func(what string, mutate func(*domain.Event)) {
				t.Helper()
				ev := newEvent(c.ID, ana.ID, mutate)
				isErr(t, "Create with "+what, e.r.Events.Create(e.ctx, &ev), domain.ErrNotFound)
			}
			for _, id := range unknownRefs {
				try(fmt.Sprintf("the collective %q", id), func(ev *domain.Event) { ev.CollectiveID = id })
			}
			try("a collective that was deleted", func(ev *domain.Event) { ev.CollectiveID = gone.ID })
			try("the ID of a user as the collective", func(ev *domain.Event) { ev.CollectiveID = ana.ID })
			for _, id := range unknownIDs {
				try(fmt.Sprintf("the creator %q (empty is no creator)", id), func(ev *domain.Event) { ev.CreatedBy = id })
			}
			try("a user that was deleted as the creator", func(ev *domain.Event) { ev.CreatedBy = dead.ID })
			try("the ID of a collective as the creator", func(ev *domain.Event) { ev.CreatedBy = c.ID })
			for _, id := range unknownRefs {
				try(fmt.Sprintf("the editor %q", id), func(ev *domain.Event) { ev.UpdatedBy = id })
			}
			try("a user that was deleted as the editor", func(ev *domain.Event) { ev.UpdatedBy = dead.ID })
			try("the ID of a collective as the editor", func(ev *domain.Event) { ev.UpdatedBy = c.ID })

			// The same events with references that exist are stored.
			ok := e.event(c.ID, ana.ID, func(ev *domain.Event) { ev.UpdatedBy = bia.ID })
			e.wantStoredEvent("an event with existing references", ok, c.Name)
		}},
		{"ByID returns the event with the name of its collective", func(t *testing.T, e env) {
			ana := e.user("ana")
			luz := e.collective("Bloco Luz", coletivo, cApproved)
			azul := e.collective("Casa Azul", produtora, cPending)
			first := e.event(luz.ID, ana.ID, eventNamed("Noite Verde"))
			second := e.event(azul.ID, ana.ID, eventNamed("Dia Claro"))
			e.wantStoredEvent("the first event", first, "Bloco Luz")
			e.wantStoredEvent("the second event", second, "Casa Azul")

			// The name is the collective's name now, not the one at the creation.
			noErr(t, "rename", e.r.Collectives.Update(e.ctx, renamed(luz, "Bloco Nova Luz")))
			e.wantStoredEvent("the first event after the rename", first, "Bloco Nova Luz")
			e.wantStoredEvent("the second event after the rename", second, "Casa Azul")
		}},
		{"ByID is ErrNotFound for an ID that names no event", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			ev := e.event(c.ID, ana.ID)
			for _, id := range append([]string{c.ID, ana.ID}, unknownIDs...) {
				_, err := e.r.Events.ByID(e.ctx, id)
				isErr(t, fmt.Sprintf("ByID(%q)", id), err, domain.ErrNotFound)
			}
			e.wantStoredEvent("the event", ev, c.Name)
		}},
		{"Update stores every editable field and the editor, adds 1 to the Version and never changes the collective, the creator or the creation time", func(t *testing.T, e env) {
			ana, bia, cleo := e.user("ana"), e.user("bia"), e.user("cleo")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			other := e.collective("Casa Azul", produtora, cApproved)
			ev := e.event(c.ID, ana.ID)

			edit := ev
			edit.Name = "Noite Roxa"
			ownTypeAndCatalogStyle(bia.ID)(&edit)
			// What Update never changes: the values here are ignored.
			edit.CollectiveID, edit.CreatedBy, edit.CreatedAt = other.ID, cleo.ID, at(time.Hour)
			noErr(t, "Update", e.r.Events.Update(e.ctx, edit))

			want := edit
			want.CollectiveID, want.CreatedBy, want.CreatedAt = ev.CollectiveID, ev.CreatedBy, ev.CreatedAt
			want.Version = ev.Version + 1
			e.wantStoredEvent("the edited event", want, c.Name)

			// A second edit, with values that could not name a collective or a creator.
			again := want
			again.Name = "Noite Dourada"
			ownStyleAndFree(cleo.ID)(&again)
			again.CollectiveID, again.CreatedBy = "not-an-id", "not-an-id"
			noErr(t, "Update again", e.r.Events.Update(e.ctx, again))
			want2 := again
			want2.CollectiveID, want2.CreatedBy = ev.CollectiveID, ev.CreatedBy
			want2.Version = ev.Version + 2
			e.wantStoredEvent("the event edited twice", want2, c.Name)
		}},
		{"Update removes the optional fields that became empty and can set them again", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			full := e.event(c.ID, ana.ID, ownTypeAndCatalogStyle(bia.ID))
			e.wantStoredEvent("the full event", full, c.Name)

			bare := full
			clearOptionals(&bare)
			bare.UpdatedBy, bare.UpdatedAt = bia.ID, at(5*time.Hour)
			noErr(t, "Update to empty", e.r.Events.Update(e.ctx, bare))
			bare.Version = 2
			e.wantStoredEvent("the event with no optional fields", bare, c.Name)
			got := e.eventView("ByID", bare.ID)
			if got.TypeOther != "" || got.TicketURL != "" || got.Substyle != "" || got.StyleOther != "" ||
				got.Venue != "" || got.Artists != "" || got.Description != "" {
				t.Errorf("an optional field kept its old value: %s", show(got.Event))
			}

			style := bare
			ownStyleAndFree(bia.ID)(&style)
			noErr(t, "Update to the typed style", e.r.Events.Update(e.ctx, style))
			style.Version = 3
			e.wantStoredEvent("the event with a typed style", style, c.Name)

			// Back to a catalog style: StyleOther must go with it.
			back := style
			back.Style, back.StyleOther = "Techno", ""
			noErr(t, "Update to the catalog style", e.r.Events.Update(e.ctx, back))
			back.Version = 4
			e.wantStoredEvent("the event with a catalog style again", back, c.Name)
		}},
		{"Update with another version is ErrConflict and changes nothing", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			ev := e.event(c.ID, ana.ID)

			first := ev
			first.Name, first.UpdatedBy, first.UpdatedAt = "Primeira edição", bia.ID, at(time.Hour)
			noErr(t, "Update", e.r.Events.Update(e.ctx, first))
			stored := first
			stored.Version = 2

			for _, v := range []int64{0, 1, 3, 1 << 40} {
				lost := ev
				lost.Name, lost.City, lost.UpdatedBy, lost.UpdatedAt = "Edição perdida", "Recife", bia.ID, at(2*time.Hour)
				lost.Version = v
				isErr(t, fmt.Sprintf("Update with Version %d (stored: 2)", v), e.r.Events.Update(e.ctx, lost), domain.ErrConflict)
				e.wantStoredEvent(fmt.Sprintf("the event after Update with Version %d", v), stored, c.Name)
			}

			// The version that was read still works.
			next := stored
			next.Name = "Segunda edição"
			noErr(t, "Update with the stored version", e.r.Events.Update(e.ctx, next))
			next.Version = 3
			e.wantStoredEvent("the event after the third write", next, c.Name)
		}},
		{"Update is ErrNotFound for an event or an editor that does not exist, before it looks at the version", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			ev := e.event(c.ID, ana.ID)
			dead := e.user("dead")
			noErr(t, "delete the user", e.r.Users.Delete(e.ctx, dead.ID))

			edit := func(mutate func(*domain.Event)) domain.Event {
				upd := ev
				upd.Name, upd.UpdatedBy, upd.UpdatedAt = "Edição", bia.ID, at(time.Hour)
				mutate(&upd)
				return upd
			}
			for _, id := range append([]string{c.ID, ana.ID}, unknownIDs...) {
				for _, v := range []int64{1, 5} {
					upd := edit(func(u *domain.Event) { u.ID, u.Version = id, v })
					isErr(t, fmt.Sprintf("Update of the event %q with Version %d", id, v), e.r.Events.Update(e.ctx, upd), domain.ErrNotFound)
				}
			}
			for _, id := range append([]string{c.ID, dead.ID}, unknownIDs...) {
				for _, v := range []int64{1, 5} {
					upd := edit(func(u *domain.Event) { u.UpdatedBy, u.Version = id, v })
					isErr(t, fmt.Sprintf("Update by the editor %q with Version %d", id, v), e.r.Events.Update(e.ctx, upd), domain.ErrNotFound)
				}
			}
			e.wantStoredEvent("the event", ev, c.Name)

			// An event that was deleted is no event either.
			noErr(t, "Delete", e.r.Events.Delete(e.ctx, ev.ID))
			isErr(t, "Update of a deleted event", e.r.Events.Update(e.ctx, edit(func(*domain.Event) {})), domain.ErrNotFound)
		}},
		{"Update validates the event before everything else and changes nothing", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			ev := e.event(c.ID, ana.ID)
			for _, tc := range invalidEvents {
				upd := ev
				upd.UpdatedBy, upd.UpdatedAt = bia.ID, at(time.Hour)
				tc.mutate(&upd)
				wantFieldErrors(t, "Update with "+tc.what, e.r.Events.Update(e.ctx, upd), tc.field)

				// Neither a stale version, nor an unknown event, nor an unknown editor hides them.
				upd.Version, upd.ID, upd.UpdatedBy = 9, "not-an-id", ""
				wantFieldErrors(t, "Update with "+tc.what+", a version, an event and an editor that do not match", e.r.Events.Update(e.ctx, upd), tc.field)
			}
			e.wantStoredEvent("the event", ev, c.Name)
		}},
		{"Update lets exactly one of concurrent calls with the same version win", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			ev := e.event(c.ID, ana.ID)
			errs := parallelErr(10, func(i int) error {
				upd := ev
				upd.Name, upd.UpdatedBy, upd.UpdatedAt = fmt.Sprintf("Edição %02d", i), bia.ID, at(time.Hour)
				return e.r.Events.Update(e.ctx, upd)
			})
			wantOneWinner(t, "Update", errs, domain.ErrConflict)
			for i, err := range errs {
				if err != nil {
					continue
				}
				want := ev
				want.Name, want.UpdatedBy, want.UpdatedAt, want.Version = fmt.Sprintf("Edição %02d", i), bia.ID, at(time.Hour), 2
				e.wantStoredEvent("the event the winner edited", want, c.Name)
			}
		}},
		{"Update loses no edit when callers read again and retry after ErrConflict", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			ev := e.event(c.ID, ana.ID)
			const workers, attempts = 6, 100
			errs := parallelErr(workers, func(i int) error {
				for range attempts {
					cur, err := e.r.Events.ByID(e.ctx, ev.ID)
					if err != nil {
						return err
					}
					upd := cur.Event
					upd.Description += fmt.Sprintf("[%d]", i)
					upd.UpdatedBy, upd.UpdatedAt = ana.ID, at(time.Hour)
					if err := e.r.Events.Update(e.ctx, upd); !errors.Is(err, domain.ErrConflict) {
						return err
					}
				}
				return errors.New("still in conflict after all the attempts")
			})
			for i, err := range errs {
				noErr(t, fmt.Sprintf("worker %d", i), err)
			}
			got := e.eventView("ByID", ev.ID)
			if got.Version != workers+1 {
				t.Errorf("Version = %d after %d edits, want %d", got.Version, workers, workers+1)
			}
			for i := range workers {
				if tag := fmt.Sprintf("[%d]", i); strings.Count(got.Description, tag) != 1 {
					t.Errorf("Description = %q, want %s exactly once", got.Description, tag)
				}
			}
		}},
		{"Delete removes the event and no other", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			keep := e.event(c.ID, ana.ID, eventNamed("Fica"))
			gone := e.event(c.ID, ana.ID, eventNamed("Some"))
			noErr(t, "Delete", e.r.Events.Delete(e.ctx, gone.ID))

			_, err := e.r.Events.ByID(e.ctx, gone.ID)
			isErr(t, "ByID of the deleted event", err, domain.ErrNotFound)
			isErr(t, "second Delete", e.r.Events.Delete(e.ctx, gone.ID), domain.ErrNotFound)
			gone.UpdatedBy = bia.ID
			isErr(t, "Update of the deleted event", e.r.Events.Update(e.ctx, gone), domain.ErrNotFound)
			e.wantStoredEvent("the other event", keep, c.Name)
			if _, err := e.r.Collectives.ByID(e.ctx, c.ID); err != nil {
				t.Errorf("the collective after Delete of its event: %v", err)
			}
		}},
		{"Delete is ErrNotFound for an ID that names no event", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			ev := e.event(c.ID, ana.ID)
			for _, id := range append([]string{c.ID, ana.ID}, unknownIDs...) {
				isErr(t, fmt.Sprintf("Delete(%q)", id), e.r.Events.Delete(e.ctx, id), domain.ErrNotFound)
			}
			e.wantStoredEvent("the event", ev, c.Name)
			if _, err := e.r.Collectives.ByID(e.ctx, c.ID); err != nil {
				t.Errorf("the collective after Delete of its own ID as an event: %v", err)
			}
			if _, err := e.r.Users.ByID(e.ctx, ana.ID); err != nil {
				t.Errorf("the user after Delete of its own ID as an event: %v", err)
			}
		}},
		{"ListBetween returns the events that start from From, included, up to To, excluded, in any zone", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			from, to := at(10*time.Hour), at(20*time.Hour)
			put := func(name string, start time.Time) domain.Event {
				return e.event(c.ID, ana.ID, eventNamed(name), func(ev *domain.Event) { ev.StartsAt = start })
			}
			put("Muito antes", at(-48*time.Hour))
			put("Antes", from.Add(-time.Millisecond))
			opens := put("Abre", inLocalZone(from))
			middle := put("Meio", at(15*time.Hour))
			closes := put("Fecha", to.Add(-time.Millisecond))
			put("Limite", to)
			put("Depois", to.Add(time.Millisecond))
			put("Muito depois", at(200*time.Hour))

			f := domain.EventFilter{From: from, To: to}
			wantEvents(t, "ListBetween [From, To)", e.listBetween("ListBetween", f, domain.ListLimitMax), opens, middle, closes)
			f = domain.EventFilter{From: inLocalZone(from), To: inLocalZone(to)}
			wantEvents(t, "ListBetween with the times in another zone", e.listBetween("ListBetween", f, domain.ListLimitMax), opens, middle, closes)

			// The range of a single millisecond holds the event that starts in it.
			f = domain.EventFilter{From: from, To: from.Add(time.Millisecond)}
			wantEvents(t, "ListBetween of one millisecond", e.listBetween("ListBetween", f, domain.ListLimitMax), opens)
			// A range that holds no event is an empty list.
			f = domain.EventFilter{From: at(30 * time.Hour), To: at(31 * time.Hour)}
			wantEvents(t, "ListBetween of a range with no event", e.listBetween("ListBetween", f, domain.ListLimitMax))
		}},
		{"ListBetween puts the earliest start first and breaks ties by ID", func(t *testing.T, e env) {
			ana := e.user("ana")
			luz := e.collective("Bloco Luz", coletivo, cApproved)
			azul := e.collective("Casa Azul", produtora, cApproved)
			late := e.event(luz.ID, ana.ID, eventNamed("Tarde"), eventAt(30*time.Hour))
			tie1 := e.event(azul.ID, ana.ID, eventNamed("Empate 1"), eventAt(20*time.Hour))
			early := e.event(luz.ID, ana.ID, eventNamed("Cedo"), eventAt(10*time.Hour))
			tie2 := e.event(luz.ID, ana.ID, eventNamed("Empate 2"), eventAt(20*time.Hour))
			justAfter := e.event(azul.ID, ana.ID, eventNamed("Logo depois"), eventAt(20*time.Hour+time.Millisecond))
			tie3 := e.event(azul.ID, ana.ID, eventNamed("Empate 3"), eventAt(20*time.Hour))
			wantDistinctIDs(t, tie1, tie2, tie3)

			want := slices.Concat([]domain.Event{early}, sortedByID(false, tie1, tie2, tie3), []domain.Event{justAfter, late})
			f := domain.EventFilter{From: at(0), To: at(100 * time.Hour)}
			wantEvents(t, "ListBetween", e.listBetween("ListBetween", f, domain.ListLimitMax), want...)
		}},
		{"ListBetween with a State keeps only the events of that state", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			sobral := e.event(c.ID, ana.ID, eventNamed("Sobral"), eventAt(1*time.Hour), eventIn("CE", "Sobral"))
			fortaleza := e.event(c.ID, ana.ID, eventNamed("Fortaleza"), eventAt(2*time.Hour), eventIn("CE", "Fortaleza"))
			recife := e.event(c.ID, ana.ID, eventNamed("Recife"), eventAt(3*time.Hour), eventIn("PE", "Recife"))
			salvador := e.event(c.ID, ana.ID, eventNamed("Salvador"), eventAt(4*time.Hour), eventIn("BA", "Salvador"))

			f := domain.EventFilter{From: at(0), To: at(10 * time.Hour)}
			wantEvents(t, "no State", e.listBetween("ListBetween", f, domain.ListLimitMax), sobral, fortaleza, recife, salvador)
			for _, tc := range []struct {
				state string
				want  []domain.Event
			}{
				{"CE", []domain.Event{sobral, fortaleza}},
				{"PE", []domain.Event{recife}},
				{"BA", []domain.Event{salvador}},
				{"RN", nil},
				// A State is a value to match, never an operator or a pattern.
				{"$ne", nil}, {".*", nil}, {"C.", nil}, {"ce", nil},
			} {
				f.State = tc.state
				wantEvents(t, "State "+tc.state, e.listBetween("ListBetween", f, domain.ListLimitMax), tc.want...)
			}

			// The range still applies with a State.
			f = domain.EventFilter{From: at(90 * time.Minute), To: at(3 * time.Hour), State: "CE"}
			wantEvents(t, "State CE from 1h30 to 3h", e.listBetween("ListBetween", f, domain.ListLimitMax), fortaleza)
		}},
		{"ListBetween with a State and a City keeps only the events of that city of that state", func(t *testing.T, e env) {
			ana := e.user("ana")
			luz := e.collective("Bloco Luz", coletivo, cApproved)
			azul := e.collective("Casa Azul", produtora, cApproved)
			santaRitaPB := e.event(luz.ID, ana.ID, eventNamed("Santa Rita PB"), eventAt(1*time.Hour), eventIn("PB", "Santa Rita"))
			santaRitaMA := e.event(luz.ID, ana.ID, eventNamed("Santa Rita MA"), eventAt(2*time.Hour), eventIn("MA", "Santa Rita"))
			fortaleza1 := e.event(luz.ID, ana.ID, eventNamed("Fortaleza 1"), eventAt(3*time.Hour), eventIn("CE", "Fortaleza"))
			fortaleza2 := e.event(azul.ID, ana.ID, eventNamed("Fortaleza 2"), eventAt(4*time.Hour), eventIn("CE", "Fortaleza"))
			sobral := e.event(azul.ID, ana.ID, eventNamed("Sobral"), eventAt(5*time.Hour), eventIn("CE", "Sobral"))

			f := domain.EventFilter{From: at(0), To: at(10 * time.Hour)}
			for _, tc := range []struct {
				state, city string
				want        []domain.Event
			}{
				{"PB", "Santa Rita", []domain.Event{santaRitaPB}},
				{"MA", "Santa Rita", []domain.Event{santaRitaMA}},
				{"CE", "Fortaleza", []domain.Event{fortaleza1, fortaleza2}},
				{"CE", "Sobral", []domain.Event{sobral}},
				{"CE", "Santa Rita", nil},
				{"PE", "Fortaleza", nil},
				{"CE", "Recife", nil},
				{"CE", "$ne", nil}, {"CE", ".*", nil}, {"CE", "fortaleza", nil},
			} {
				f.State, f.City = tc.state, tc.city
				wantEvents(t, fmt.Sprintf("State %s, City %s", tc.state, tc.city), e.listBetween("ListBetween", f, domain.ListLimitMax), tc.want...)
			}
		}},
		{"ListBetween is a FieldErrors for a City without a State and for a From that is not before To", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			e.event(c.ID, ana.ID, eventAt(15*time.Hour))
			from, to := at(10*time.Hour), at(20*time.Hour)
			for _, tc := range []struct {
				what   string
				f      domain.EventFilter
				fields []string
			}{
				{"a City without a State", domain.EventFilter{From: from, To: to, City: "Fortaleza"}, []string{"city"}},
				{"From equal to To", domain.EventFilter{From: from, To: from}, []string{"to"}},
				{"From after To", domain.EventFilter{From: to, To: from}, []string{"to"}},
				{"From one millisecond after To", domain.EventFilter{From: from.Add(time.Millisecond), To: from}, []string{"to"}},
				{"no times", domain.EventFilter{}, []string{"to"}},
				{"a City without a State and From after To", domain.EventFilter{From: to, To: from, City: "Fortaleza"}, []string{"city", "to"}},
			} {
				got, err := e.r.Events.ListBetween(e.ctx, tc.f, domain.ListLimitMax)
				wantFieldErrors(t, "ListBetween with "+tc.what, err, tc.fields...)
				if len(got) != 0 {
					t.Errorf("ListBetween with %s returned %d events with its error, want none", tc.what, len(got))
				}
			}

			// A State without a City, and a City with its State, are filters.
			f := domain.EventFilter{From: from, To: to, State: "CE"}
			if got := e.listBetween("ListBetween with a State", f, domain.ListLimitMax); len(got) != 1 {
				t.Errorf("ListBetween with a State returned %d events, want 1", len(got))
			}
			f.City = "Fortaleza"
			if got := e.listBetween("ListBetween with a State and a City", f, domain.ListLimitMax); len(got) != 1 {
				t.Errorf("ListBetween with a State and a City returned %d events, want 1", len(got))
			}
		}},
		{"ListBetween returns at most limit events, the earliest ones, and clamps a limit that is not in 1..ListLimitMax", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			first := e.event(c.ID, ana.ID, eventNamed("Primeiro"), eventAt(1*time.Hour))
			second := e.event(c.ID, ana.ID, eventNamed("Segundo"), eventAt(2*time.Hour))
			third := e.event(c.ID, ana.ID, eventNamed("Terceiro"), eventAt(3*time.Hour))
			f := domain.EventFilter{From: at(0), To: at(10 * time.Hour)}
			all := []domain.Event{first, second, third}
			for _, tc := range []struct {
				limit int
				want  []domain.Event
			}{
				{math.MinInt, all[:1]}, {-5, all[:1]}, {0, all[:1]}, {1, all[:1]}, {2, all[:2]}, {3, all}, {4, all},
				{domain.ListLimitMax, all}, {domain.ListLimitMax + 1, all}, {math.MaxInt, all},
			} {
				wantEvents(t, fmt.Sprintf("ListBetween with limit %d", tc.limit), e.listBetween("ListBetween", f, tc.limit), tc.want...)
			}
		}},
		{"ListBetween and ListByCollectives return at most ListLimitMax events", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			hi := domain.ListLimitMax
			events := make([]domain.Event, hi+10)
			for i := range events {
				events[i] = e.event(c.ID, ana.ID, eventNamed(fmt.Sprintf("Evento %03d", i)), eventAt(time.Duration(i)*time.Minute))
			}
			earliest := events[:hi]
			latest := make([]domain.Event, hi)
			for i := range latest {
				latest[i] = events[len(events)-1-i]
			}
			f := domain.EventFilter{From: at(0), To: at(time.Duration(len(events)) * time.Minute)}
			for _, limit := range []int{hi + 1, 2 * hi, math.MaxInt} {
				wantEvents(t, fmt.Sprintf("ListBetween with limit %d", limit), e.listBetween("ListBetween", f, limit), earliest...)
				wantEvents(t, fmt.Sprintf("ListByCollectives with limit %d", limit), e.listByCollectives("ListByCollectives", []string{c.ID}, limit), latest...)
			}
		}},
		{"ListBetween gives each event the current name of its collective, whatever the collective's status", func(t *testing.T, e env) {
			ana := e.user("ana")
			luz := e.collective("Bloco Luz", coletivo, cApproved)
			azul := e.collective("Casa Azul", produtora, cPending)
			preta := e.collective("Casa Preta", coletivo, cRejected, func(c *domain.Collective) { c.EventsBlocked = true })
			a := e.event(luz.ID, ana.ID, eventNamed("Evento A"), eventAt(1*time.Hour))
			b := e.event(azul.ID, ana.ID, eventNamed("Evento B"), eventAt(2*time.Hour))
			c := e.event(preta.ID, ana.ID, eventNamed("Evento C"), eventAt(3*time.Hour))
			d := e.event(luz.ID, ana.ID, eventNamed("Evento D"), eventAt(4*time.Hour))

			f := domain.EventFilter{From: at(0), To: at(10 * time.Hour)}
			wantNames := func(what string, names ...string) {
				t.Helper()
				got := e.listBetween("ListBetween "+what, f, domain.ListLimitMax)
				wantEvents(t, what, got, a, b, c, d)
				for i, v := range got {
					if v.CollectiveName != names[i] {
						t.Errorf("%s: %s has CollectiveName %q, want %q", what, v.Name, v.CollectiveName, names[i])
					}
				}
			}
			wantNames("before the rename", "Bloco Luz", "Casa Azul", "Casa Preta", "Bloco Luz")
			noErr(t, "rename", e.r.Collectives.Update(e.ctx, renamed(luz, "Bloco Nova Luz")))
			wantNames("after the rename", "Bloco Nova Luz", "Casa Azul", "Casa Preta", "Bloco Nova Luz")
		}},
		{"The lists return every field of the event as ByID does, with the times in UTC", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			luz := e.collective("Bloco Luz", coletivo, cApproved)
			bare := e.event(luz.ID, ana.ID, eventNamed("Simples"), eventAt(10*time.Hour))
			full := e.event(luz.ID, ana.ID, eventNamed("Completo"), ownTypeAndCatalogStyle(bia.ID))
			typed := e.event(luz.ID, ana.ID, eventNamed("Livre"), ownStyleAndFree(bia.ID))

			check := func(what string, got []domain.EventView) {
				t.Helper()
				for _, v := range got {
					stored := e.eventView("ByID for "+v.Name, v.ID)
					wantEqual(t, what+": "+v.Name, normEventView(v), normEventView(stored))
					wantUTC(t, what+": StartsAt of "+v.Name, &v.StartsAt, stored.StartsAt)
					wantUTC(t, what+": CreatedAt of "+v.Name, &v.CreatedAt, stored.CreatedAt)
					wantUTC(t, what+": UpdatedAt of "+v.Name, &v.UpdatedAt, stored.UpdatedAt)
				}
			}
			f := domain.EventFilter{From: at(0), To: at(200 * time.Hour)}
			between := e.listBetween("ListBetween", f, domain.ListLimitMax)
			wantEvents(t, "ListBetween", between, bare, full, typed)
			check("ListBetween", between)
			byCollective := e.listByCollectives("ListByCollectives", []string{luz.ID}, domain.ListLimitMax)
			wantEvents(t, "ListByCollectives", byCollective, typed, full, bare)
			check("ListByCollectives", byCollective)
		}},
		{"ListByCollectives returns the events of the given collectives, past ones too, the latest start first and ties by ID descending", func(t *testing.T, e env) {
			ana := e.user("ana")
			luz := e.collective("Bloco Luz", coletivo, cApproved)
			azul := e.collective("Casa Azul", produtora, cPending)
			verde := e.collective("Casa Verde", coletivo, cApproved)
			old := e.event(luz.ID, ana.ID, eventNamed("Antigo"), eventAt(-30*24*time.Hour))
			a1 := e.event(azul.ID, ana.ID, eventNamed("Azul 1"), eventAt(10*time.Hour))
			l2 := e.event(luz.ID, ana.ID, eventNamed("Luz 2"), eventAt(20*time.Hour))
			a2 := e.event(azul.ID, ana.ID, eventNamed("Azul 2"), eventAt(20*time.Hour))
			l3 := e.event(luz.ID, ana.ID, eventNamed("Luz 3"), eventAt(20*time.Hour))
			far := e.event(azul.ID, ana.ID, eventNamed("Distante"), eventAt(500*time.Hour))
			greenOne := e.event(verde.ID, ana.ID, eventNamed("Verde"), eventAt(15*time.Hour))
			wantDistinctIDs(t, l2, a2, l3)

			both := slices.Concat([]domain.Event{far}, sortedByID(true, l2, a2, l3), []domain.Event{a1, old})
			for _, ids := range [][]string{{luz.ID, azul.ID}, {azul.ID, luz.ID}, {luz.ID, azul.ID, luz.ID, azul.ID}} {
				wantEvents(t, fmt.Sprintf("ListByCollectives(%d IDs)", len(ids)), e.listByCollectives("ListByCollectives", ids, domain.ListLimitMax), both...)
			}
			wantEvents(t, "ListByCollectives of Bloco Luz", e.listByCollectives("ListByCollectives", []string{luz.ID}, domain.ListLimitMax),
				slices.Concat(sortedByID(true, l2, l3), []domain.Event{old})...)
			wantEvents(t, "ListByCollectives of Casa Azul", e.listByCollectives("ListByCollectives", []string{azul.ID}, domain.ListLimitMax),
				far, a2, a1)
			wantEvents(t, "ListByCollectives of Casa Verde", e.listByCollectives("ListByCollectives", []string{verde.ID}, domain.ListLimitMax), greenOne)

			names := map[string]string{luz.ID: "Bloco Luz", azul.ID: "Casa Azul"}
			for _, v := range e.listByCollectives("ListByCollectives", []string{luz.ID, azul.ID}, domain.ListLimitMax) {
				if v.CollectiveName != names[v.CollectiveID] {
					t.Errorf("%s has CollectiveName %q, want %q", v.Name, v.CollectiveName, names[v.CollectiveID])
				}
			}
		}},
		{"ListByCollectives gives an empty list for no IDs and for IDs that name no collective, and ignores them next to a real one", func(t *testing.T, e env) {
			ana := e.user("ana")
			luz := e.collective("Bloco Luz", coletivo, cApproved)
			ev := e.event(luz.ID, ana.ID, eventNamed("Noite"))
			gone := e.collective("Bloco Extinto", coletivo, cApproved)
			noErr(t, "delete the collective", e.r.Collectives.Delete(e.ctx, gone.ID))

			for _, tc := range []struct {
				what string
				ids  []string
			}{
				{"nil", nil},
				{"an empty slice", []string{}},
				{"IDs of nothing", unknownIDs},
				{"a collective that was deleted", []string{gone.ID}},
				{"the ID of a user", []string{ana.ID}},
				{"the ID of an event", []string{ev.ID}},
				{"all of them", slices.Concat(unknownIDs, []string{gone.ID, ana.ID, ev.ID})},
			} {
				if got := e.listByCollectives("ListByCollectives with "+tc.what, tc.ids, domain.ListLimitMax); len(got) != 0 {
					t.Errorf("ListByCollectives with %s returned %d events, want none", tc.what, len(got))
				}
			}
			ids := slices.Concat(unknownIDs, []string{gone.ID, luz.ID, ana.ID})
			wantEvents(t, "ListByCollectives with a real ID among the others", e.listByCollectives("ListByCollectives", ids, domain.ListLimitMax), ev)
		}},
		{"ListByCollectives returns at most limit events, the latest ones, and clamps a limit that is not in 1..ListLimitMax", func(t *testing.T, e env) {
			ana := e.user("ana")
			luz := e.collective("Bloco Luz", coletivo, cApproved)
			azul := e.collective("Casa Azul", produtora, cApproved)
			one := e.event(luz.ID, ana.ID, eventNamed("Um"), eventAt(1*time.Hour))
			two := e.event(azul.ID, ana.ID, eventNamed("Dois"), eventAt(2*time.Hour))
			three := e.event(luz.ID, ana.ID, eventNamed("Três"), eventAt(3*time.Hour))
			four := e.event(azul.ID, ana.ID, eventNamed("Quatro"), eventAt(4*time.Hour))
			ids := []string{luz.ID, azul.ID}
			all := []domain.Event{four, three, two, one}
			for _, tc := range []struct {
				limit int
				want  []domain.Event
			}{
				{math.MinInt, all[:1]}, {-5, all[:1]}, {0, all[:1]}, {1, all[:1]}, {2, all[:2]}, {4, all}, {5, all},
				{domain.ListLimitMax, all}, {domain.ListLimitMax + 1, all}, {math.MaxInt, all},
			} {
				wantEvents(t, fmt.Sprintf("ListByCollectives with limit %d", tc.limit), e.listByCollectives("ListByCollectives", ids, tc.limit), tc.want...)
			}
		}},
		{"ListPlaces returns each (State, City) pair once, ordered by State and then City in byte order", func(t *testing.T, e env) {
			ana := e.user("ana")
			luz := e.collective("Bloco Luz", coletivo, cApproved)
			azul := e.collective("Casa Azul", produtora, cPending)
			rosa := e.collective("Casa Rosa", coletivo, cRejected)
			for i, tc := range []struct {
				collective, state, city string
			}{
				{luz.ID, "PE", "Recife"},
				{luz.ID, "CE", "Sobral"},
				{azul.ID, "CE", "Fortaleza"},
				{luz.ID, "AL", "Maceió"},
				{rosa.ID, "PE", "Recife"},
				{luz.ID, "PB", "Santa Rita"},
				{azul.ID, "AL", "Água Branca"},
				{luz.ID, "CE", "Fortaleza"},
				{luz.ID, "MA", "Santa Rita"},
				{rosa.ID, "AL", "Arapiraca"},
				{azul.ID, "CE", "Fortaleza"},
				{azul.ID, "BA", "Salvador"},
			} {
				e.event(tc.collective, ana.ID, eventNamed(fmt.Sprintf("Evento %02d", i)), eventAt(time.Duration(i)*time.Hour), eventIn(tc.state, tc.city))
			}
			// "Água Branca" sorts after every city of Alagoas that starts with an ASCII
			// letter: byte order, not the order of a dictionary.
			wantEqual(t, "ListPlaces", e.listPlaces("ListPlaces"), []domain.Place{
				{State: "AL", City: "Arapiraca"}, {State: "AL", City: "Maceió"}, {State: "AL", City: "Água Branca"},
				{State: "BA", City: "Salvador"},
				{State: "CE", City: "Fortaleza"}, {State: "CE", City: "Sobral"},
				{State: "MA", City: "Santa Rita"},
				{State: "PB", City: "Santa Rita"},
				{State: "PE", City: "Recife"},
			})
		}},
		{"ListPlaces follows the stored events", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Bloco Luz", coletivo, cApproved)
			if got := e.listPlaces("ListPlaces of an empty store"); len(got) != 0 {
				t.Fatalf("ListPlaces of an empty store = %v, want none", got)
			}
			moved := e.event(c.ID, ana.ID, eventNamed("Muda"), eventIn("CE", "Fortaleza"))
			kept := e.event(c.ID, ana.ID, eventNamed("Fica"), eventIn("CE", "Sobral"))
			wantEqual(t, "ListPlaces", e.listPlaces("ListPlaces"), []domain.Place{{State: "CE", City: "Fortaleza"}, {State: "CE", City: "Sobral"}})

			edit := moved
			edit.State, edit.City, edit.UpdatedBy, edit.UpdatedAt = "PE", "Recife", ana.ID, at(time.Hour)
			noErr(t, "Update", e.r.Events.Update(e.ctx, edit))
			wantEqual(t, "ListPlaces after the event moved", e.listPlaces("ListPlaces"), []domain.Place{{State: "CE", City: "Sobral"}, {State: "PE", City: "Recife"}})

			noErr(t, "Delete", e.r.Events.Delete(e.ctx, kept.ID))
			wantEqual(t, "ListPlaces after the other event was deleted", e.listPlaces("ListPlaces"), []domain.Place{{State: "PE", City: "Recife"}})
		}},
	})
}
