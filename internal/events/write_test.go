package events

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// writeMissingID is a well-formed ID that no document has.
const writeMissingID = "000000000000000000000000"

// writeForm is a form that creates a valid event in the collective: a Festa on
// Friday 20/11/2026, 21:00 in Fortaleza, for 4h30, R$ 30,00, in Fortaleza/CE.
// Every field differs from insertEvent's defaults, so an edit shows.
func writeForm(collectiveID string) Form {
	return Form{
		CollectiveID: collectiveID,
		Name:         "Festa Nova",
		Type:         "festa",
		Date:         "20/11/2026",
		Time:         "21:00",
		DurHours:     "4",
		DurMinutes:   "30",
		Price:        "30,00",
		TicketURL:    "https://ingressos.example/nova",
		Style:        "techno",
		Substyle:     "acid techno",
		State:        "CE",
		City:         "Fortaleza",
		Venue:        "Galpão 7",
		Artists:      "Ana\nBeto",
		Description:  "Uma festa nova.",
	}
}

// writeWant is the event writeForm turns into.
func writeWant(collectiveID string) domain.Event {
	return domain.Event{
		CollectiveID: collectiveID,
		Name:         "Festa Nova",
		Type:         domain.EventTypeFesta,
		StartsAt:     time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC), // 20/11, 21:00 in Fortaleza
		DurationMin:  270,
		PriceCents:   3000,
		TicketURL:    "https://ingressos.example/nova",
		Style:        "techno",
		Substyle:     "acid techno",
		State:        "CE",
		City:         "Fortaleza",
		Venue:        "Galpão 7",
		Artists:      "Ana\nBeto",
		Description:  "Uma festa nova.",
	}
}

// writeStored reads the event from the store.
func writeStored(t *testing.T, s domain.Store, id string) domain.Event {
	t.Helper()
	v, err := s.Repos().Events.ByID(t.Context(), id)
	if err != nil {
		t.Fatalf("read event %q: %v", id, err)
	}
	return v.Event
}

// writeCount returns how many events the store holds.
func writeCount(t *testing.T, s domain.Store) int {
	t.Helper()
	f := domain.EventFilter{From: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), To: time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)}
	list, err := s.Repos().Events.ListBetween(t.Context(), f, domain.ListLimitMax)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return len(list)
}

// writeSameEvent fails the test unless got is want, with the times compared as
// instants at the store's millisecond precision.
func writeSameEvent(t *testing.T, got, want domain.Event) {
	t.Helper()
	for name, pair := range map[string][2]time.Time{
		"StartsAt":  {got.StartsAt, want.StartsAt},
		"CreatedAt": {got.CreatedAt, want.CreatedAt},
		"UpdatedAt": {got.UpdatedAt, want.UpdatedAt},
	} {
		if !pair[0].Equal(pair[1].Truncate(time.Millisecond)) {
			t.Errorf("%s = %v, want %v", name, pair[0], pair[1])
		}
	}
	got.StartsAt, got.CreatedAt, got.UpdatedAt = time.Time{}, time.Time{}, time.Time{}
	want.StartsAt, want.CreatedAt, want.UpdatedAt = time.Time{}, time.Time{}, time.Time{}
	if got != want {
		t.Errorf("event = %+v\nwant    %+v", got, want)
	}
}

// writeOp is one of the three writes, run as actor on an event of the collective
// (the update and the delete first store one) with the form tweaked by mutate.
type writeOp struct {
	name string
	run  func(t *testing.T, w world, actor domain.User, c domain.Collective, mutate func(*Form)) (id string, err error)
}

// The order of writeOps is the order of the want arrays below.
var writeOps = []writeOp{
	{"create", func(t *testing.T, w world, actor domain.User, c domain.Collective, mutate func(*Form)) (string, error) {
		f := writeForm(c.ID)
		if mutate != nil {
			mutate(&f)
		}
		return Create(t.Context(), w.S, actor, f, testNow, testLoc)
	}},
	{"update", func(t *testing.T, w world, actor domain.User, c domain.Collective, mutate func(*Form)) (string, error) {
		ev := insertEvent(t, w.S, c.ID, w.Owner.ID)
		f := writeForm(c.ID)
		f.Version = "1"
		if mutate != nil {
			mutate(&f)
		}
		return ev.ID, Update(t.Context(), w.S, actor, ev.ID, f, testNow, testLoc)
	}},
	{"delete", func(t *testing.T, w world, actor domain.User, c domain.Collective, _ func(*Form)) (string, error) {
		ev := insertEvent(t, w.S, c.ID, w.Owner.ID)
		return ev.ID, Delete(t.Context(), w.S, actor, ev.ID, testNow, testLoc)
	}},
}

// writeInvalid blanks every field of the form but the collective and the version.
func writeInvalid(f *Form) {
	*f = Form{CollectiveID: f.CollectiveID, Version: f.Version}
}

// writeDone reports whether the write left its mark: the event created, renamed
// by the form or gone.
func writeDone(t *testing.T, s domain.Store, op, id string) bool {
	t.Helper()
	switch op {
	case "create":
		return writeCount(t, s) == 1
	case "update":
		return writeStored(t, s, id).Name == writeForm("").Name
	default:
		_, err := s.Repos().Events.ByID(t.Context(), id)
		if err != nil && !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("read event %q: %v", id, err)
		}
		return err != nil
	}
}

// TestWritePermissions checks the permission table through the three writes:
// rights come before the form's errors, and a refused write changes nothing.
func TestWritePermissions(t *testing.T) {
	owner := func(w world) domain.User { return w.Owner }
	member := func(w world) domain.User { return w.Member }
	outsider := func(w world) domain.User { return w.Outsider }
	admin := func(w world) domain.User { return w.Admin }
	alfa := func(w world) domain.Collective { return w.Alfa }
	delta := func(w world) domain.Collective { return w.Delta }
	denied := [3]error{ErrNotAllowed, ErrNotAllowed, ErrNotAllowed}

	rows := []struct {
		name  string
		actor func(w world) domain.User
		in    func(w world) domain.Collective
		want  [3]error // create, update, delete
	}{
		{"Administrador", owner, alfa, [3]error{}},
		{"Administrador of a blocked collective", owner, delta, [3]error{ErrBlocked, ErrBlocked, ErrBlocked}},
		{"Membro", member, alfa, denied},
		{"no membership", outsider, alfa, denied},
		{"admin outside the collective", admin, alfa, [3]error{ErrNotAllowed, nil, nil}},
		{"admin outside a blocked collective", admin, delta, [3]error{ErrNotAllowed, nil, nil}},
	}
	for _, row := range rows {
		for i, op := range writeOps {
			for _, invalid := range []bool{false, true} {
				if invalid && op.name == "delete" {
					continue // a delete has no form
				}
				name := row.name + "/" + op.name
				var mutate func(*Form)
				if invalid {
					name += " with an invalid form"
					mutate = writeInvalid
				}
				t.Run(name, func(t *testing.T) {
					w := newWorld(t)
					id, err := op.run(t, w, row.actor(w), row.in(w), mutate)
					want := row.want[i]
					switch {
					case want == nil && invalid:
						var fe domain.FieldErrors
						if !errors.As(err, &fe) {
							t.Fatalf("err = %v, want a domain.FieldErrors", err)
						}
					case want == nil:
						if err != nil {
							t.Fatalf("err = %v, want success", err)
						}
					default:
						requireErrorIs(t, err, want)
					}
					done := want == nil && !invalid
					if got := writeDone(t, w.S, op.name, id); got != done {
						t.Errorf("write left its mark = %v, want %v", got, done)
					}
					if !done {
						if n := len(auditEntries(t, w.S)); n != 0 {
							t.Errorf("a write that changed nothing wrote %d audit entries", n)
						}
					}
				})
			}
		}
	}
}

// TestWriteReReadsTheActor checks that the write trusts the store, not the
// actor value it was handed: a disabled account, a forged admin flag and an
// unknown ID are all refused.
func TestWriteReReadsTheActor(t *testing.T) {
	actors := []struct {
		name  string
		actor func(t *testing.T, w world) domain.User
	}{
		{"disabled since the session was read", func(t *testing.T, w world) domain.User {
			stale := w.Owner // still active here
			if err := w.S.Repos().Users.SetStatus(t.Context(), w.Owner.ID, domain.UserStatusDisabled, testNow); err != nil {
				t.Fatalf("disable the owner: %v", err)
			}
			return stale
		}},
		{"forged admin flag", func(t *testing.T, w world) domain.User {
			forged := w.Outsider
			forged.IsAdmin = true
			return forged
		}},
		{"unknown account", func(t *testing.T, w world) domain.User {
			return domain.User{ID: writeMissingID, Name: "Fantasma", Status: domain.UserStatusActive, IsAdmin: true}
		}},
	}
	for _, a := range actors {
		for _, op := range writeOps {
			t.Run(a.name+"/"+op.name, func(t *testing.T) {
				w := newWorld(t)
				actor := a.actor(t, w)
				id, err := op.run(t, w, actor, w.Alfa, nil)
				requireErrorIs(t, err, ErrNotAllowed)
				if writeDone(t, w.S, op.name, id) {
					t.Error("the refused write left its mark")
				}
			})
		}
	}
}

// TestCreateNeedsAKnownCollective checks the two ways the form can fail to name
// a collective: none (the form is incomplete) or one the actor may not learn
// about (refused as not allowed, whether or not it exists).
func TestCreateNeedsAKnownCollective(t *testing.T) {
	w := newWorld(t)
	for _, id := range []string{"", "   "} {
		_, err := Create(t.Context(), w.S, w.Owner, writeForm(id), testNow, testLoc)
		requireFieldErrors(t, err, "collective_id")
	}
	for _, id := range []string{writeMissingID, "nope"} {
		_, err := Create(t.Context(), w.S, w.Owner, writeForm(id), testNow, testLoc)
		requireErrorIs(t, err, ErrNotAllowed)
	}
	if n := writeCount(t, w.S); n != 0 {
		t.Errorf("%d events stored, want none", n)
	}
}

// TestWriteUnknownEvent checks that an event that is not there is not found, for
// anyone, and whatever the ID looks like.
func TestWriteUnknownEvent(t *testing.T) {
	w := newWorld(t)
	for _, id := range []string{writeMissingID, "nope", ""} {
		for _, actor := range []domain.User{w.Owner, w.Outsider, w.Admin} {
			f := writeForm(w.Alfa.ID)
			f.Version = "1"
			requireErrorIs(t, Update(t.Context(), w.S, actor, id, f, testNow, testLoc), domain.ErrNotFound)
			requireErrorIs(t, Delete(t.Context(), w.S, actor, id, testNow, testLoc), domain.ErrNotFound)
		}
	}
}

// writeGoneStore wraps a store so that one collective is not found, as if its
// document were gone while its events still pointed at it. The store's own delete
// removes the events with the collective, so only a wrapper can stage this.
type writeGoneStore struct {
	domain.Store
	gone string
}

func (s writeGoneStore) WithTx(ctx context.Context, fn func(ctx context.Context, r domain.Repos) error) error {
	return s.Store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		r.Collectives = writeGoneCollectives{Collectives: r.Collectives, gone: s.gone}
		return fn(ctx, r)
	})
}

type writeGoneCollectives struct {
	domain.Collectives
	gone string
}

func (c writeGoneCollectives) ByID(ctx context.Context, id string) (domain.Collective, error) {
	if id == c.gone {
		return domain.Collective{}, domain.ErrNotFound
	}
	return c.Collectives.ByID(ctx, id)
}

// TestWriteEventOfAGoneCollective checks that an Update or a Delete of an event
// whose collective is no longer there is not found, for anyone, and changes
// nothing: it is neither a permission error nor a failure of the flow.
func TestWriteEventOfAGoneCollective(t *testing.T) {
	w := newWorld(t)
	ev := insertEvent(t, w.S, w.Alfa.ID, w.Owner.ID)
	s := writeGoneStore{Store: w.S, gone: w.Alfa.ID}
	for _, actor := range []domain.User{w.Owner, w.Outsider, w.Admin} {
		f := writeForm(w.Alfa.ID)
		f.Version = "1"
		requireErrorIs(t, Update(t.Context(), s, actor, ev.ID, f, testNow, testLoc), domain.ErrNotFound)
		requireErrorIs(t, Delete(t.Context(), s, actor, ev.ID, testNow, testLoc), domain.ErrNotFound)
	}
	writeSameEvent(t, writeStored(t, w.S, ev.ID), ev)
	if n := len(auditEntries(t, w.S)); n != 0 {
		t.Errorf("a refused write left %d audit entries", n)
	}
}

func TestCreateStoresTheForm(t *testing.T) {
	w := newWorld(t)
	f := writeForm(w.Alfa.ID)
	f.Version = "7" // a new event always starts at version 1
	now := testNow.Add(3 * time.Hour)

	id, err := Create(t.Context(), w.S, w.Owner, f, now, testLoc)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Fatal("Create returned an empty ID")
	}
	want := writeWant(w.Alfa.ID)
	want.ID, want.CreatedBy, want.UpdatedBy, want.CreatedAt, want.UpdatedAt, want.Version = id, w.Owner.ID, w.Owner.ID, now, now, 1
	writeSameEvent(t, writeStored(t, w.S, id), want)
	if n := len(auditEntries(t, w.S)); n != 0 {
		t.Errorf("a create wrote %d audit entries, want none", n)
	}
}

// TestAdminCreatesAsAdministrador checks that the event block never stops the
// admin, but only when the admin is an Administrador of the collective.
func TestAdminCreatesAsAdministrador(t *testing.T) {
	w := newWorld(t)
	insertMember(t, w.S, w.Admin.ID, w.Delta.ID, domain.RoleOwner)
	id, err := Create(t.Context(), w.S, w.Admin, writeForm(w.Delta.ID), testNow, testLoc)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := writeStored(t, w.S, id); got.CreatedBy != w.Admin.ID || got.CollectiveID != w.Delta.ID {
		t.Errorf("stored %+v", got)
	}
}

func TestCreateWithAnInvalidFormStoresNothing(t *testing.T) {
	w := newWorld(t)
	f := writeForm(w.Alfa.ID)
	f.Name = ""
	f.Date = "31/02/2026"
	_, err := Create(t.Context(), w.S, w.Owner, f, testNow, testLoc)
	requireFieldErrors(t, err, "name", "date")
	if n := writeCount(t, w.S); n != 0 {
		t.Errorf("%d events stored, want none", n)
	}
}

func TestUpdateWritesTheForm(t *testing.T) {
	w := newWorld(t)
	ev := insertEvent(t, w.S, w.Alfa.ID, w.Admin.ID) // created by someone else
	f := writeForm(w.Delta.ID)                       // the form names another collective: ignored
	f.Version = "1"
	now := testNow.Add(2 * time.Hour)

	if err := Update(t.Context(), w.S, w.Owner, ev.ID, f, now, testLoc); err != nil {
		t.Fatalf("Update: %v", err)
	}
	want := writeWant(w.Alfa.ID)
	want.ID, want.CreatedBy, want.UpdatedBy, want.CreatedAt, want.UpdatedAt, want.Version = ev.ID, w.Admin.ID, w.Owner.ID, testNow, now, 2
	writeSameEvent(t, writeStored(t, w.S, ev.ID), want)
	if n := len(auditEntries(t, w.S)); n != 0 {
		t.Errorf("an Administrador's edit wrote %d audit entries, want none", n)
	}
}

func TestUpdateWithAnInvalidFormChangesNothing(t *testing.T) {
	w := newWorld(t)
	ev := insertEvent(t, w.S, w.Alfa.ID, w.Owner.ID)
	f := writeForm(w.Alfa.ID)
	f.Name = ""
	// No version: the form must say which version it edits.
	err := Update(t.Context(), w.S, w.Owner, ev.ID, f, testNow, testLoc)
	requireFieldErrors(t, err, "name", "version")
	writeSameEvent(t, writeStored(t, w.S, ev.ID), ev)
}

func TestUpdateWithAStaleVersion(t *testing.T) {
	w := newWorld(t)
	ev := insertEvent(t, w.S, w.Alfa.ID, w.Owner.ID)
	first := writeForm(w.Alfa.ID)
	first.Version = "1"
	first.Name = "Primeira edição"
	if err := Update(t.Context(), w.S, w.Owner, ev.ID, first, testNow, testLoc); err != nil {
		t.Fatalf("first Update: %v", err)
	}

	// The admin read the event before that edit.
	stale := writeForm(w.Alfa.ID)
	stale.Version = "1"
	stale.Name = "Edição velha"
	requireErrorIs(t, Update(t.Context(), w.S, w.Admin, ev.ID, stale, testNow, testLoc), ErrChanged)

	got := writeStored(t, w.S, ev.ID)
	if got.Name != "Primeira edição" || got.Version != 2 || got.UpdatedBy != w.Owner.ID {
		t.Errorf("after the stale edit: name %q, version %d, updated by %q; want the first edit untouched", got.Name, got.Version, got.UpdatedBy)
	}
	if n := len(auditEntries(t, w.S)); n != 0 {
		t.Errorf("a refused edit wrote %d audit entries", n)
	}

	// With the current version the same edit goes through.
	stale.Version = "2"
	if err := Update(t.Context(), w.S, w.Admin, ev.ID, stale, testNow, testLoc); err != nil {
		t.Fatalf("Update with the current version: %v", err)
	}
	if got := writeStored(t, w.S, ev.ID); got.Name != "Edição velha" || got.Version != 3 {
		t.Errorf("after the retry: name %q, version %d", got.Name, got.Version)
	}
}

// TestUpdateKeepsAPastDate checks that an event of the past can be edited while
// it keeps its date, and cannot be moved to another past date.
func TestUpdateKeepsAPastDate(t *testing.T) {
	w := newWorld(t)
	startsAt := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC) // 04/11, 22:00 in Fortaleza
	ev := insertEvent(t, w.S, w.Alfa.ID, w.Owner.ID, func(e *domain.Event) { e.StartsAt = startsAt })
	f := writeForm(w.Alfa.ID)
	f.Date, f.Time, f.Version = "04/11/2026", "22:00", "1"

	if err := Update(t.Context(), w.S, w.Owner, ev.ID, f, testNow, testLoc); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got := writeStored(t, w.S, ev.ID)
	if got.Name != "Festa Nova" || !got.StartsAt.Equal(startsAt) {
		t.Errorf("name %q, starts at %v; want the new name and %v", got.Name, got.StartsAt, startsAt)
	}

	f.Date, f.Version = "03/11/2026", "2"
	err := Update(t.Context(), w.S, w.Owner, ev.ID, f, testNow, testLoc)
	requireFieldErrors(t, err, "date")
}

func TestDeleteRemovesTheEvent(t *testing.T) {
	w := newWorld(t)
	ev := insertEvent(t, w.S, w.Alfa.ID, w.Owner.ID)
	other := insertEvent(t, w.S, w.Alfa.ID, w.Owner.ID)
	if err := Delete(t.Context(), w.S, w.Owner, ev.ID, testNow, testLoc); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := w.S.Repos().Events.ByID(t.Context(), ev.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ByID of the deleted event: %v, want ErrNotFound", err)
	}
	writeSameEvent(t, writeStored(t, w.S, other.ID), other)
	requireErrorIs(t, Delete(t.Context(), w.S, w.Owner, ev.ID, testNow, testLoc), domain.ErrNotFound)
}

// TestWriteAudit checks which writes leave an entry, and what the entry holds.
func TestWriteAudit(t *testing.T) {
	now := testNow.Add(time.Hour)
	// wantEntry is the entry an actor's write leaves in the collective.
	wantEntry := func(action domain.AuditAction, actor domain.User, c domain.Collective, detail string) domain.AuditEntry {
		return domain.AuditEntry{At: now, Action: action, ActorID: actor.ID, ActorName: actor.Name, CollectiveID: c.ID, CollectiveName: c.Name, Detail: detail}
	}
	// onlyEntry fails the test unless the log holds exactly want.
	onlyEntry := func(t *testing.T, s domain.Store, want domain.AuditEntry) {
		t.Helper()
		entries := auditEntries(t, s)
		if len(entries) != 1 {
			t.Fatalf("audit log: %+v, want exactly one entry", entries)
		}
		got := entries[0]
		if !got.At.Equal(want.At) {
			t.Errorf("At = %v, want %v", got.At, want.At)
		}
		got.ID, got.At, want.At = "", time.Time{}, time.Time{}
		if got != want {
			t.Errorf("entry = %+v\nwant    %+v", got, want)
		}
	}
	noEntry := func(t *testing.T, s domain.Store) {
		t.Helper()
		if entries := auditEntries(t, s); len(entries) != 0 {
			t.Errorf("audit log: %+v, want no entry", entries)
		}
	}
	edit := func(t *testing.T, w world, actor domain.User, c domain.Collective) error {
		ev := insertEvent(t, w.S, c.ID, w.Owner.ID)
		f := writeForm(c.ID)
		f.Version = "1"
		return Update(t.Context(), w.S, actor, ev.ID, f, now, testLoc)
	}

	t.Run("an Administrador's delete", func(t *testing.T) {
		w := newWorld(t)
		ev := insertEvent(t, w.S, w.Alfa.ID, w.Owner.ID)
		if err := Delete(t.Context(), w.S, w.Owner, ev.ID, now, testLoc); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		onlyEntry(t, w.S, wantEntry(domain.AuditEventDeleted, w.Owner, w.Alfa, "Noite de Teste · 15/11/2026"))
	})
	t.Run("the date is read in the given zone", func(t *testing.T) {
		w := newWorld(t)
		ev := insertEvent(t, w.S, w.Alfa.ID, w.Owner.ID) // 16/11 01:00 UTC
		if err := Delete(t.Context(), w.S, w.Owner, ev.ID, now, time.UTC); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		onlyEntry(t, w.S, wantEntry(domain.AuditEventDeleted, w.Owner, w.Alfa, "Noite de Teste · 16/11/2026"))
	})
	t.Run("the admin's delete in a blocked collective", func(t *testing.T) {
		w := newWorld(t)
		ev := insertEvent(t, w.S, w.Delta.ID, w.Owner.ID)
		if err := Delete(t.Context(), w.S, w.Admin, ev.ID, now, testLoc); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		onlyEntry(t, w.S, wantEntry(domain.AuditEventDeleted, w.Admin, w.Delta, "Noite de Teste · 15/11/2026"))
	})
	t.Run("the admin's delete as an Administrador", func(t *testing.T) {
		w := newWorld(t)
		insertMember(t, w.S, w.Admin.ID, w.Alfa.ID, domain.RoleOwner)
		ev := insertEvent(t, w.S, w.Alfa.ID, w.Owner.ID)
		if err := Delete(t.Context(), w.S, w.Admin, ev.ID, now, testLoc); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		onlyEntry(t, w.S, wantEntry(domain.AuditEventDeleted, w.Admin, w.Alfa, "Noite de Teste · 15/11/2026"))
	})
	t.Run("the admin's edit in a collective they are not Administrador of", func(t *testing.T) {
		w := newWorld(t)
		if err := edit(t, w, w.Admin, w.Delta); err != nil {
			t.Fatalf("Update: %v", err)
		}
		onlyEntry(t, w.S, wantEntry(domain.AuditEventEdited, w.Admin, w.Delta, "Festa Nova · 20/11/2026"))
	})
	t.Run("the admin's edit as a Membro", func(t *testing.T) {
		w := newWorld(t)
		insertMember(t, w.S, w.Admin.ID, w.Alfa.ID, domain.RoleMember)
		if err := edit(t, w, w.Admin, w.Alfa); err != nil {
			t.Fatalf("Update: %v", err)
		}
		onlyEntry(t, w.S, wantEntry(domain.AuditEventEdited, w.Admin, w.Alfa, "Festa Nova · 20/11/2026"))
	})
	t.Run("the admin's edit as an Administrador", func(t *testing.T) {
		w := newWorld(t)
		insertMember(t, w.S, w.Admin.ID, w.Alfa.ID, domain.RoleOwner)
		if err := edit(t, w, w.Admin, w.Alfa); err != nil {
			t.Fatalf("Update: %v", err)
		}
		noEntry(t, w.S)
	})
	t.Run("an Administrador's edit", func(t *testing.T) {
		w := newWorld(t)
		if err := edit(t, w, w.Owner, w.Alfa); err != nil {
			t.Fatalf("Update: %v", err)
		}
		noEntry(t, w.S)
	})
	t.Run("a create", func(t *testing.T) {
		w := newWorld(t)
		if _, err := Create(t.Context(), w.S, w.Owner, writeForm(w.Alfa.ID), now, testLoc); err != nil {
			t.Fatalf("Create: %v", err)
		}
		noEntry(t, w.S)
	})
}

// writeRaceStore wraps a store to stage a race: the first time a transaction
// reads a collective, the hook runs and commits a competing change in a
// transaction of its own, so the flow under test reads the collective as it was
// and then writes it after the change. attempts counts the runs of the
// transaction body, so a test can tell that the store retried.
type writeRaceStore struct {
	domain.Store
	hook     func()
	fired    bool
	attempts int
}

func (s *writeRaceStore) WithTx(ctx context.Context, fn func(ctx context.Context, r domain.Repos) error) error {
	return s.Store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		s.attempts++
		r.Collectives = writeRaceCollectives{Collectives: r.Collectives, s: s}
		return fn(ctx, r)
	})
}

type writeRaceCollectives struct {
	domain.Collectives
	s *writeRaceStore
}

func (c writeRaceCollectives) ByID(ctx context.Context, id string) (domain.Collective, error) {
	got, err := c.Collectives.ByID(ctx, id)
	if err == nil && !c.s.fired {
		c.s.fired = true
		c.s.hook()
	}
	return got, err
}

// writeRaced runs a competing change as its own transaction on the test's
// context, never the flow's, so it does not join the flow's transaction.
func writeRaced(t *testing.T, s domain.Store, change func(ctx context.Context, r domain.Repos) error) func() {
	return func() {
		if err := s.WithTx(t.Context(), change); err != nil {
			t.Errorf("competing change: %v", err)
		}
	}
}

// requireRetried fails the test unless the competing change ran in the first
// attempt and the store ran the flow a second time.
func requireRetried(t *testing.T, s *writeRaceStore) {
	t.Helper()
	if !s.fired || s.attempts != 2 {
		t.Fatalf("competing change ran = %v, attempts = %d; want it on the first attempt and one retry", s.fired, s.attempts)
	}
}

// TestCreateAgainstABlock stages the admin blocking a collective's events while
// an Administrador's Create is under way: the Create must not win.
func TestCreateAgainstABlock(t *testing.T) {
	w := newWorld(t)
	s := &writeRaceStore{Store: w.S}
	s.hook = writeRaced(t, w.S, func(ctx context.Context, r domain.Repos) error {
		c, err := r.Collectives.ByID(ctx, w.Alfa.ID)
		if err != nil {
			return err
		}
		c.EventsBlocked = true
		return r.Collectives.Update(ctx, c)
	})

	_, err := Create(t.Context(), s, w.Owner, writeForm(w.Alfa.ID), testNow, testLoc)
	requireErrorIs(t, err, ErrBlocked)
	requireRetried(t, s)
	if n := writeCount(t, w.S); n != 0 {
		t.Errorf("%d events stored by someone who was blocked, want none", n)
	}
}

// TestEditAgainstADemotion stages an Administrador being demoted while their
// Update or Delete is under way: the write must not win.
func TestEditAgainstADemotion(t *testing.T) {
	for _, op := range []string{"update", "delete"} {
		t.Run(op, func(t *testing.T) {
			w := newWorld(t)
			ev := insertEvent(t, w.S, w.Alfa.ID, w.Owner.ID)
			s := &writeRaceStore{Store: w.S}
			s.hook = writeRaced(t, w.S, func(ctx context.Context, r domain.Repos) error {
				m, err := r.Memberships.Get(ctx, w.Owner.ID, w.Alfa.ID)
				if err != nil {
					return err
				}
				m.Role = domain.RoleMember
				if err := r.Memberships.Update(ctx, m); err != nil {
					return err
				}
				c, err := r.Collectives.ByID(ctx, w.Alfa.ID)
				if err != nil {
					return err
				}
				return r.Collectives.Update(ctx, c)
			})

			var err error
			if op == "update" {
				f := writeForm(w.Alfa.ID)
				f.Version = "1"
				err = Update(t.Context(), s, w.Owner, ev.ID, f, testNow, testLoc)
			} else {
				err = Delete(t.Context(), s, w.Owner, ev.ID, testNow, testLoc)
			}
			requireErrorIs(t, err, ErrNotAllowed)
			requireRetried(t, s)
			writeSameEvent(t, writeStored(t, w.S, ev.ID), ev)
			if n := len(auditEntries(t, w.S)); n != 0 {
				t.Errorf("a refused write left %d audit entries", n)
			}
		})
	}
}
