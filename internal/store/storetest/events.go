package storetest

import (
	"errors"
	"fmt"
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

// RunEvents runs the contract of domain.Events that writes and reads one event:
// Create, ByID, Update and Delete. The lists have their own subtests.
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
			first := e.event(luz.ID, ana.ID, func(ev *domain.Event) { ev.Name = "Noite Verde" })
			second := e.event(azul.ID, ana.ID, func(ev *domain.Event) { ev.Name = "Dia Claro" })
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
			keep := e.event(c.ID, ana.ID, func(ev *domain.Event) { ev.Name = "Fica" })
			gone := e.event(c.ID, ana.ID, func(ev *domain.Event) { ev.Name = "Some" })
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
	})
}
