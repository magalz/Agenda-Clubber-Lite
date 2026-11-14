package approvals

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// The site admin's decisions on collectives. A new collective needs only the
// admin's approval: the creator's own approval is already on their membership,
// so approving the collective approves that membership with it. Rejecting a
// collective deletes it (and its membership), so the person can ask again.

// ApproveCollective approves the pending collective for the site admin actor:
// the collective becomes approved and the admin side of its creator's membership
// is set, which approves that membership (and lets the creator publish events)
// when its owner side is set too, as a signup leaves it. It emails the creator,
// with a link under origin, and writes an audit entry, in the same transaction.
//
// It returns ErrNotAllowed when actor is not an active site admin, or when the
// creator's account is not active (an account that awaits approval, is disabled
// or was deleted: the account comes first); domain.ErrNotFound when the
// collective does not exist; and ErrDecided when it is not pending. An approval
// that races with another decision on the same collective gets ErrDecided (it
// lost to another approval) or domain.ErrNotFound (it lost to a rejection).
func ApproveCollective(ctx context.Context, store domain.Store, origin string, actor domain.User, collectiveID string, now time.Time) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		c, err := r.Collectives.ByID(ctx, collectiveID)
		if err != nil {
			return wrap("approve collective: read collective", err)
		}
		if c.Status != domain.CollectiveStatusPending {
			return ErrDecided
		}
		creator, err := collectiveCreator(ctx, r, c)
		if err != nil {
			return wrap("approve collective: read creator", err)
		}
		if creator.Status != domain.UserStatusActive {
			// Also true of the zero User, the creator who is gone.
			return ErrNotAllowed
		}
		d := newDecision(creator.Email, creator.Name)
		if err := approveCollectiveTx(ctx, r, actor, creator, c, d, now); err != nil {
			return err
		}
		return d.send(ctx, r, origin+pathCollectives, now)
	})
}

// RejectCollective rejects the pending collective for the site admin actor: it
// deletes the collective and its membership, emails the creator (with the
// reason, when there is one, and a link to the login page under origin) and
// writes an audit entry, in the same transaction. The reason is optional and is
// cleaned by CheckReason. A creator whose account was deleted gets no email, and
// the audit entry names nobody.
//
// It returns ErrNotAllowed when actor is not an active site admin (checked
// before the reason), domain.FieldErrors with the key "reason" for a bad reason
// (checked before the collective is read), domain.ErrNotFound when the
// collective does not exist (a second rejection included), and ErrDecided when
// it is already approved.
func RejectCollective(ctx context.Context, store domain.Store, origin string, actor domain.User, collectiveID, reason string, now time.Time) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	reason, err := CheckReason(reason)
	if err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		c, err := r.Collectives.ByID(ctx, collectiveID)
		if err != nil {
			return wrap("reject collective: read collective", err)
		}
		if c.Status != domain.CollectiveStatusPending {
			return ErrDecided
		}
		creator, err := collectiveCreator(ctx, r, c)
		if err != nil {
			return wrap("reject collective: read creator", err)
		}
		d := newDecision(creator.Email, creator.Name)
		if err := rejectCollectiveTx(ctx, r, actor, creator, c, reason, d, now); err != nil {
			return err
		}
		return d.send(ctx, r, origin+pathLogin, now)
	})
}

// SetCollectiveBlocks sets the two independent blocks of the approved
// collective for the site admin actor: eventsBlocked (its members cannot
// publish events) and locked (no join decision, promotion, demotion or rename).
// It writes an audit entry naming what changed, with the optional reason
// cleaned by CheckReason, and queues no email: a block is not a decision about
// anybody's request. A call that asks for what the collective already has
// changes and records nothing.
//
// It returns ErrNotAllowed when actor is not an active site admin (checked
// before the reason) or the collective is not approved, domain.FieldErrors with
// the key "reason" for a bad reason (checked before the collective is read) and
// domain.ErrNotFound when the collective does not exist.
func SetCollectiveBlocks(ctx context.Context, store domain.Store, actor domain.User, collectiveID string, eventsBlocked, locked bool, reason string, now time.Time) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	reason, err := CheckReason(reason)
	if err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		c, err := r.Collectives.ByID(ctx, collectiveID)
		if err != nil {
			return wrap("set collective blocks: read collective", err)
		}
		if c.Status != domain.CollectiveStatusApproved {
			return ErrNotAllowed
		}
		detail := blocksDetail(c, eventsBlocked, locked)
		if detail == "" {
			return nil
		}
		c.EventsBlocked, c.Locked, c.UpdatedAt = eventsBlocked, locked, now
		if err := r.Collectives.Update(ctx, c); err != nil {
			return wrap("set collective blocks: update collective", err)
		}
		e := auditBy(domain.AuditCollectiveBlocks, actor, now)
		e.CollectiveID, e.CollectiveName, e.Detail, e.Reason = c.ID, c.Name, detail, reason
		return audit(ctx, r, e)
	})
}

// approveCollectiveTx approves the pending collective c, which the caller read
// in the transaction r, and the admin side of the membership of its creator,
// with the audit entry of the collective, and adds the collective's line to the
// caller's decision d (the caller sends d, so the person gets one email for
// everything decided). The membership becomes approved, and decided now, only
// when its owner side is set too; a signup that creates a collective leaves it
// set, so the creator is approved with the collective. The caller has checked
// that c is pending and that creator, who asked for it, is an active account.
//
// The membership of the creator is missing only when an invariant is broken,
// and that is an error that is not domain.ErrNotFound, so the web layer does not
// answer "not found" for a collective that exists.
func approveCollectiveTx(ctx context.Context, r domain.Repos, actor, creator domain.User, c domain.Collective, d *decision, now time.Time) error {
	at := now
	c.Status, c.ApprovedBy, c.ApprovedAt, c.UpdatedAt = domain.CollectiveStatusApproved, actor.ID, &at, now
	if err := r.Collectives.Update(ctx, c); err != nil {
		return wrap("approve collective: update collective", err)
	}

	m, err := r.Memberships.Get(ctx, creator.ID, c.ID)
	if errors.Is(err, domain.ErrNotFound) {
		return errors.New("approvals: approve collective: the creator has no membership in the collective")
	}
	if err != nil {
		return wrap("approve collective: read creator membership", err)
	}
	m.AdminApprovedBy, m.AdminApprovedAt = actor.ID, &at
	if m.OwnerApprovedAt != nil {
		m.Status, m.DecidedAt = domain.MembershipStatusApproved, &at
	}
	if err := r.Memberships.Update(ctx, m); err != nil {
		return wrap("approve collective: update creator membership", err)
	}

	d.add(whatCollective(c.Name, c.Kind), mail.DecisionApproved, "")
	e := auditBy(domain.AuditCollectiveApproved, actor, now)
	e.UserID, e.UserName, e.CollectiveID, e.CollectiveName = creator.ID, creator.Name, c.ID, c.Name
	return audit(ctx, r, e)
}

// rejectCollectiveTx deletes the pending collective c (and so its memberships),
// which the caller read in the transaction r, with the audit entry of the
// rejection, and adds the collective's line to the caller's decision d. creator
// is the user who asked for it, or the zero User when the account was deleted,
// and reason is already cleaned by CheckReason.
func rejectCollectiveTx(ctx context.Context, r domain.Repos, actor, creator domain.User, c domain.Collective, reason string, d *decision, now time.Time) error {
	if err := r.Collectives.Delete(ctx, c.ID); err != nil {
		return wrap("reject collective: delete collective", err)
	}
	d.add(whatCollective(c.Name, c.Kind), mail.DecisionRejected, reason)
	e := auditBy(domain.AuditCollectiveRejected, actor, now)
	e.UserID, e.UserName, e.CollectiveID, e.CollectiveName, e.Reason = creator.ID, creator.Name, c.ID, c.Name, reason
	return audit(ctx, r, e)
}

// collectiveCreator returns the user who asked for the collective, or the zero
// User when nobody is left: the account was deleted (Users.Delete clears
// CreatedBy, and an empty ID is domain.ErrNotFound). Any other error is returned
// as it is.
func collectiveCreator(ctx context.Context, r domain.Repos, c domain.Collective) (domain.User, error) {
	u, err := r.Users.ByID(ctx, c.CreatedBy)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, nil
	}
	return u, err
}

// blocksDetail describes, for the audit log, what changes when c takes the
// given blocks, for example "eventos bloqueados; membros e nome congelados". It
// is empty when nothing changes.
func blocksDetail(c domain.Collective, eventsBlocked, locked bool) string {
	var parts []string
	if eventsBlocked != c.EventsBlocked {
		if eventsBlocked {
			parts = append(parts, "eventos bloqueados")
		} else {
			parts = append(parts, "eventos liberados")
		}
	}
	if locked != c.Locked {
		if locked {
			parts = append(parts, "membros e nome congelados")
		} else {
			parts = append(parts, "membros e nome liberados")
		}
	}
	return strings.Join(parts, "; ")
}
