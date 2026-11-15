package events

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// writeDateLayout is dd/mm/aaaa, the way dates are shown.
const writeDateLayout = "02/01/2006"

// Create validates the form and stores a new event of the collective it names, as
// actor, and returns the new event's ID. It runs in one transaction that re-reads
// the actor and the collective and checks the permission table (mayCreate) before
// it looks at the rest of the form, so a person who may not create learns that
// and nothing else about the input. It returns ErrNotAllowed or ErrBlocked when
// the actor may not create, and domain.FieldErrors for a form the person can fix;
// an empty collective_id is the one form error returned without a transaction,
// because there is no collective to check rights against. No audit entry is
// written: only deletions and an admin's edits are.
func Create(ctx context.Context, s domain.Store, actor domain.User, f Form, now time.Time, loc *time.Location) (string, error) {
	form, fe := f.Event(now, loc, nil)
	if form.CollectiveID == "" {
		return "", fe.Err()
	}
	var id string
	err := s.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		sc, err := writeRead(ctx, r, actor.ID, form.CollectiveID, ErrNotAllowed)
		if err != nil {
			return err
		}
		if err := mayCreate(sc.actor, sc.c, sc.m); err != nil {
			return err
		}
		if err := fe.Err(); err != nil {
			return err
		}
		ev := form // a copy for each run: fn may run again and must not keep an ID
		ev.CreatedBy, ev.UpdatedBy = sc.actor.ID, sc.actor.ID
		ev.CreatedAt, ev.UpdatedAt = now, now
		if err := r.Events.Create(ctx, &ev); err != nil {
			return writeWrap("create event", err)
		}
		if err := r.Collectives.Update(ctx, sc.c); err != nil {
			return writeWrap("serialize on the collective", err)
		}
		id = ev.ID
		return nil
	})
	if err != nil {
		return "", err
	}
	return id, nil
}

// Update saves the form over the event id, as actor, in one transaction that
// re-reads the event's collective and checks mayEdit first. The form's
// collective_id is ignored: an event never changes collective. It returns
// domain.ErrNotFound for an unknown event, ErrNotAllowed or ErrBlocked when the
// actor may not edit, domain.FieldErrors for a form the person can fix, and
// ErrChanged when the event was saved after the form was read (its version
// differs). The site admin's edit in a collective where the admin is not an
// approved Administrador is written to the audit log.
func Update(ctx context.Context, s domain.Store, actor domain.User, id string, f Form, now time.Time, loc *time.Location) error {
	return s.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		prev, err := r.Events.ByID(ctx, id)
		if err != nil {
			return writeWrap("read event", err)
		}
		sc, err := writeRead(ctx, r, actor.ID, prev.CollectiveID, domain.ErrNotFound)
		if err != nil {
			return err
		}
		if err := mayEdit(sc.actor, sc.c, sc.m); err != nil {
			return err
		}
		ev, fe := f.Event(now, loc, &prev.Event)
		if err := fe.Err(); err != nil {
			return err
		}
		ev.UpdatedBy, ev.UpdatedAt = sc.actor.ID, now
		if err := r.Events.Update(ctx, ev); errors.Is(err, domain.ErrConflict) {
			return ErrChanged
		} else if err != nil {
			return writeWrap("update event", err)
		}
		if err := r.Collectives.Update(ctx, sc.c); err != nil {
			return writeWrap("serialize on the collective", err)
		}
		if sc.actor.IsAdmin && !approvedOwner(sc.m) {
			return writeAudit(ctx, r, domain.AuditEventEdited, sc, ev, now, loc)
		}
		return nil
	})
}

// Delete removes the event id, as actor, in one transaction that re-reads the
// event's collective and checks mayEdit first. It returns domain.ErrNotFound for
// an unknown event and ErrNotAllowed or ErrBlocked when the actor may not delete.
// Every deletion is written to the audit log.
func Delete(ctx context.Context, s domain.Store, actor domain.User, id string, now time.Time, loc *time.Location) error {
	return s.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		ev, err := r.Events.ByID(ctx, id)
		if err != nil {
			return writeWrap("read event", err)
		}
		sc, err := writeRead(ctx, r, actor.ID, ev.CollectiveID, domain.ErrNotFound)
		if err != nil {
			return err
		}
		if err := mayEdit(sc.actor, sc.c, sc.m); err != nil {
			return err
		}
		if err := r.Events.Delete(ctx, id); err != nil {
			return writeWrap("delete event", err)
		}
		if err := r.Collectives.Update(ctx, sc.c); err != nil {
			return writeWrap("serialize on the collective", err)
		}
		return writeAudit(ctx, r, domain.AuditEventDeleted, sc, ev.Event, now, loc)
	})
}

// writeScene is what a write reads in its transaction before it asks the
// permission table: the actor's account as stored (never the copy the caller
// holds, which may be stale), the collective and the actor's membership in it
// (nil when there is none).
type writeScene struct {
	actor domain.User
	c     domain.Collective
	m     *domain.Membership
}

// writeRead reads the scene of a write. An actor the store does not know is
// ErrNotAllowed; a collective it does not know is noCollective, which differs
// between creating (ErrNotAllowed: the form named it) and editing (the event's
// collective is gone: domain.ErrNotFound).
func writeRead(ctx context.Context, r domain.Repos, actorID, collectiveID string, noCollective error) (writeScene, error) {
	var sc writeScene
	var err error
	if sc.actor, err = r.Users.ByID(ctx, actorID); errors.Is(err, domain.ErrNotFound) {
		return sc, ErrNotAllowed
	} else if err != nil {
		return sc, writeWrap("read actor", err)
	}
	if sc.c, err = r.Collectives.ByID(ctx, collectiveID); errors.Is(err, domain.ErrNotFound) {
		return sc, noCollective
	} else if err != nil {
		return sc, writeWrap("read collective", err)
	}
	m, err := r.Memberships.Get(ctx, actorID, collectiveID)
	switch {
	case err == nil:
		sc.m = &m
	case !errors.Is(err, domain.ErrNotFound):
		return sc, writeWrap("read membership", err)
	}
	return sc, nil
}

// writeAudit writes the audit entry of an action on event e inside the caller's
// transaction. The entry keeps IDs and name snapshots, never an email address;
// its detail is the event's name and its start date in loc.
func writeAudit(ctx context.Context, r domain.Repos, action domain.AuditAction, sc writeScene, e domain.Event, now time.Time, loc *time.Location) error {
	entry := domain.AuditEntry{
		At:             now,
		Action:         action,
		ActorID:        sc.actor.ID,
		ActorName:      sc.actor.Name,
		CollectiveID:   sc.c.ID,
		CollectiveName: sc.c.Name,
		Detail:         e.Name + " · " + e.StartsAt.In(loc).Format(writeDateLayout),
	}
	if err := r.AuditEntries.Create(ctx, &entry); err != nil {
		return writeWrap("write audit entry", err)
	}
	return nil
}

// writeWrap adds the step to a store error. A FieldErrors from the store means
// the flow stored something its own validation should have caught, a bug and not
// something the person can fix, so it is flattened to text and no longer matches
// errors.As. Any other error stays matchable with errors.Is, which also keeps the
// transient-transaction label the driver reads to retry the transaction.
func writeWrap(step string, err error) error {
	var fe domain.FieldErrors
	if errors.As(err, &fe) {
		return fmt.Errorf("events: %s: the store rejected a validated entity: %s", step, fe.Error())
	}
	return fmt.Errorf("events: %s: %w", step, err)
}
