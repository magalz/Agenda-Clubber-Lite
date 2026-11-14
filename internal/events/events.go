// Package events holds the calendar's event flows: turning the event form's
// strings into a domain.Event (input.go), creating, editing and deleting an
// event (write.go), and the views the pages render: the month grid, the day
// list, Meus Eventos, the detail and the collectives the form may offer
// (read.go, calendar.go). Each write is a service function that takes the domain
// store, the acting user and the current time, runs in one Store.WithTx and
// returns one of the errors below, domain.ErrNotFound for an unknown event, or
// domain.FieldErrors for input the person can fix.
//
// Who may do what (the tests check this table):
//   - Everyone signed in with an active account sees the calendar, the detail
//     and the day list.
//   - Creating needs an active account that is an approved Administrador (the
//     stored role owner) of an approved collective without the event block
//     (Collective.EventsBlocked). The form offers only such collectives.
//   - Editing and deleting need an approved Administrador of the event's
//     collective, or the site admin (User.IsAdmin). The event block stops the
//     Administradores from editing and deleting too; it never stops the admin.
//   - Meus Eventos lists the events of every collective where the person has an
//     approved membership, Membro included; a Membro's rows and the rows of a
//     blocked collective are read-only.
//   - The audit log gets an entry for every deletion and for every edit by the
//     admin in a collective where the admin is not an approved Administrador.
//
// A write depends on the collective's state (its status, the event block and the
// actor's role in it), so it reads the collective in its transaction and writes
// it back there (see the conventions at the top of internal/domain/repo.go).
//
// For data access this package uses only internal/domain, never a database
// driver or a store implementation; it also uses internal/catalog for the music
// styles, the states and the cities. Files and test fixtures carry the name of
// their flow (input…, write…, read…, calendar…), so flows written in parallel
// do not collide.
package events

import "errors"

var (
	// ErrNotAllowed is the answer when the actor may not do what was asked: the
	// web layer answers 403.
	ErrNotAllowed = errors.New("events: not allowed")

	// ErrBlocked is the answer when the site admin blocked the events of the
	// collective and the actor is not the admin: the web layer answers 409.
	ErrBlocked = errors.New("events: the collective's events are blocked")

	// ErrChanged is the answer when the event was saved by someone else after
	// the form was read (its version differs): the web layer answers 409.
	ErrChanged = errors.New("events: the event changed since the form was read")
)
