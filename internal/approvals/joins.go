package approvals

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// The join and role flows of an approved collective. A request to join has two
// sides, the site admin's and an Administrador's, and is approved when both are
// set, in either order; the person is emailed once, when the decision that
// settles it is made (or, for the admin's side, to say that the request waits for
// an Administrador). A rejection by either side deletes the request, so the
// person can ask again. The lock (Collective.Locked) freezes the Administradores'
// decisions, promotions and demotions but not the site admin's decisions.
//
// Every write path reads the collective in its transaction and writes it back,
// with UpdatedAt set to now, before it commits: that write is what makes two
// transactions on one collective conflict, so the loser runs again and reads what
// the winner stored (see the conventions at the top of internal/domain/repo.go).

// AdminApproveJoin sets the admin side of the pending request of userID to join
// the approved collective, for the site admin actor. When the Administrador side
// is set too, the membership becomes approved and the person is emailed that the
// request was approved; otherwise the email says the request waits for an
// Administrador. The links in the email are under origin. The call writes an
// audit entry in the same transaction. The lock does not stop the admin.
//
// It returns ErrNotAllowed when actor is not an active site admin, the collective
// is not approved, or the requester's account is not active; domain.ErrNotFound
// when the collective or the request does not exist; and ErrDecided when the
// request is no longer pending or its admin side is already set.
func AdminApproveJoin(ctx context.Context, store domain.Store, origin string, actor domain.User, userID, collectiveID string, now time.Time) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "admin approve join"
		c, err := joinApprovedCollective(ctx, r, collectiveID, step)
		if err != nil {
			return err
		}
		m, err := r.Memberships.Get(ctx, userID, collectiveID)
		if err != nil {
			return wrap(step+": read membership", err)
		}
		if m.Status != domain.MembershipStatusPending || m.AdminApprovedAt != nil {
			return ErrDecided
		}
		requester, err := joinActiveRequester(ctx, r, userID, step)
		if err != nil {
			return err
		}
		d := newDecision(requester.Email, requester.Name)
		if err := adminApproveJoinTx(ctx, r, actor, requester, c, m, d, now); err != nil {
			return err
		}
		return d.send(ctx, r, origin+pathCollectives, now)
	})
}

// OwnerApproveJoin sets the Administrador side of the pending request of userID
// to join the approved collective, for actor, an approved Administrador of it.
// When the admin side is set too, the membership becomes approved and the person
// is emailed (links under origin); otherwise the person is told nothing yet, as
// the request is still the admin's to decide. The call writes an audit entry in
// the same transaction.
//
// It returns ErrNotAllowed when actor is not an approved Administrador of the
// collective (an unknown collective included, so nobody learns it exists), the
// collective is not approved or the requester's account is not active; ErrLocked
// when the collective is locked; domain.ErrNotFound when the request does not
// exist; and ErrDecided when it is no longer pending or its Administrador side
// is already set.
func OwnerApproveJoin(ctx context.Context, store domain.Store, origin string, actor domain.User, userID, collectiveID string, now time.Time) error {
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "owner approve join"
		c, err := joinOwnerOf(ctx, r, actor, collectiveID, step)
		if err != nil {
			return err
		}
		m, err := r.Memberships.Get(ctx, userID, collectiveID)
		if err != nil {
			return wrap(step+": read membership", err)
		}
		if m.Status != domain.MembershipStatusPending || m.OwnerApprovedAt != nil {
			return ErrDecided
		}
		requester, err := joinActiveRequester(ctx, r, userID, step)
		if err != nil {
			return err
		}

		at := now
		m.OwnerApprovedBy, m.OwnerApprovedAt = actor.ID, &at
		settled := m.AdminApprovedAt != nil
		if settled {
			m.Status, m.DecidedAt = domain.MembershipStatusApproved, &at
		}
		if err := r.Memberships.Update(ctx, m); err != nil {
			return wrap(step+": update membership", err)
		}
		if err := joinTouch(ctx, r, c, now, step); err != nil {
			return err
		}
		if err := joinAudit(ctx, r, domain.AuditJoinOwnerApproved, actor, requester, c, RoleLabel(m.Role), "", now); err != nil {
			return err
		}
		if !settled {
			return nil
		}
		d := newDecision(requester.Email, requester.Name)
		d.add(whatJoin(c.Name, m.Role), mail.DecisionApproved, "")
		return d.send(ctx, r, origin+pathCollectives, now)
	})
}

// RejectJoin rejects the pending request of userID to join the approved
// collective, for actor: the site admin, or an approved Administrador of the
// collective. It deletes the request, emails the person (with the reason, when
// there is one, and a link under origin) and writes an audit entry, in the same
// transaction. The reason is optional and is cleaned by CheckReason. The lock
// does not stop the admin.
//
// It returns domain.FieldErrors with the key "reason" for a bad reason (checked
// before anything else); ErrNotAllowed when actor is neither an active site admin
// nor an approved Administrador of the collective (for an Administrador, an
// unknown collective is this too) or the collective is not approved; ErrLocked
// when the collective is locked and actor is not the admin; domain.ErrNotFound
// when the request (or, for the admin, the collective) does not exist; and
// ErrDecided when the request is no longer pending.
func RejectJoin(ctx context.Context, store domain.Store, origin string, actor domain.User, userID, collectiveID, reason string, now time.Time) error {
	reason, err := CheckReason(reason)
	if err != nil {
		return err
	}
	isAdmin := requireAdmin(actor) == nil
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "reject join"
		var c domain.Collective
		var err error
		if isAdmin {
			c, err = joinApprovedCollective(ctx, r, collectiveID, step)
		} else {
			c, err = joinOwnerOf(ctx, r, actor, collectiveID, step)
		}
		if err != nil {
			return err
		}
		m, err := r.Memberships.Get(ctx, userID, collectiveID)
		if err != nil {
			return wrap(step+": read membership", err)
		}
		if m.Status != domain.MembershipStatusPending {
			return ErrDecided
		}
		requester, err := joinMemberUser(ctx, r, userID, step)
		if err != nil {
			return err
		}
		d := newDecision(requester.Email, requester.Name)
		if err := rejectJoinTx(ctx, r, actor, requester, c, m, reason, d, now); err != nil {
			return err
		}
		return d.send(ctx, r, origin+pathCollectives, now)
	})
}

// Promote makes the Membro userID an Administrador of the approved collective,
// for actor, an approved Administrador of it, and writes an audit entry, in the
// same transaction. It queues no email: a promotion is not a decision about
// anybody's request.
//
// It returns ErrNotAllowed when actor is not an approved Administrador of the
// collective (the site admin is not one unless they are a member; an unknown
// collective is this too) or the collective is not approved; ErrLocked when it is
// locked; domain.ErrNotFound when userID is not in the collective; and ErrDecided
// when their membership is not an approved Membro.
func Promote(ctx context.Context, store domain.Store, actor domain.User, userID, collectiveID string, now time.Time) error {
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "promote member"
		c, err := joinOwnerOf(ctx, r, actor, collectiveID, step)
		if err != nil {
			return err
		}
		m, err := r.Memberships.Get(ctx, userID, collectiveID)
		if err != nil {
			return wrap(step+": read membership", err)
		}
		if m.Status != domain.MembershipStatusApproved || m.Role != domain.RoleMember {
			return ErrDecided
		}
		u, err := joinMemberUser(ctx, r, userID, step)
		if err != nil {
			return err
		}
		return joinChangeRole(ctx, r, domain.AuditMemberPromoted, actor, u, c, m, domain.RoleOwner, now, step)
	})
}

// Demote makes the Administrador userID a Membro of the approved collective, for
// actor, an approved Administrador of it, and writes an audit entry, in the same
// transaction. It queues no email. An Administrador may demote themself while
// another Administrador with an active account remains.
//
// It returns ErrNotAllowed when actor is not an approved Administrador of the
// collective (the site admin is not one unless they are a member; an unknown
// collective is this too) or the collective is not approved; ErrLocked when it is
// locked; domain.ErrNotFound when userID is not in the collective; ErrDecided
// when their membership is not an approved Administrador; ErrMaster when they are
// the Master (the creator); and ErrLastOwner when no other approved
// Administrador with an active account would remain. Two Administradores who
// demote each other at the same time cannot both succeed: the loser runs again
// and, no longer an Administrador, gets ErrNotAllowed.
func Demote(ctx context.Context, store domain.Store, actor domain.User, userID, collectiveID string, now time.Time) error {
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "demote member"
		c, err := joinOwnerOf(ctx, r, actor, collectiveID, step)
		if err != nil {
			return err
		}
		m, err := r.Memberships.Get(ctx, userID, collectiveID)
		if err != nil {
			return wrap(step+": read membership", err)
		}
		if m.Status != domain.MembershipStatusApproved || m.Role != domain.RoleOwner {
			return ErrDecided
		}
		if c.CreatedBy != "" && m.UserID == c.CreatedBy {
			return ErrMaster
		}
		views, err := r.Memberships.ListByCollective(ctx, collectiveID)
		if err != nil {
			return wrap(step+": list members", err)
		}
		if !slices.ContainsFunc(views, func(v domain.MemberView) bool {
			return v.UserID != m.UserID && v.Role == domain.RoleOwner &&
				v.Status == domain.MembershipStatusApproved && v.UserStatus == domain.UserStatusActive
		}) {
			return ErrLastOwner
		}
		u, err := joinMemberUser(ctx, r, userID, step)
		if err != nil {
			return err
		}
		return joinChangeRole(ctx, r, domain.AuditMemberDemoted, actor, u, c, m, domain.RoleMember, now, step)
	})
}

// CollectivePage is what an Administrador sees of their collective: its members
// and the join requests they can still decide.
type CollectivePage struct {
	Collective domain.Collective
	// Members are the approved memberships, the Administradores first.
	Members []domain.MemberView
	// Requests are the pending requests whose Administrador side is open and whose
	// account is active, the Administradores first.
	Requests []domain.MemberView
}

// IsMaster reports whether userID is the collective's Master, its creator. A
// collective whose creator's account was deleted has none.
func (p CollectivePage) IsMaster(userID string) bool {
	return userID != "" && userID == p.Collective.CreatedBy
}

// CollectiveView returns the page of the approved collective for actor, an
// approved Administrador of it. It runs no transaction: the page is a read. Every
// refusal (actor is not an approved Administrador of the collective, the
// collective is unknown or not approved) is domain.ErrNotFound, so the page never
// confirms that a collective exists to someone who may not manage it.
func CollectiveView(ctx context.Context, store domain.Store, actor domain.User, collectiveID string) (CollectivePage, error) {
	const step = "collective view"
	r := store.Repos()
	if _, err := requireOwner(ctx, r, actor, collectiveID); err != nil {
		if errors.Is(err, ErrNotAllowed) {
			return CollectivePage{}, domain.ErrNotFound
		}
		return CollectivePage{}, err
	}
	c, err := r.Collectives.ByID(ctx, collectiveID)
	if err != nil {
		return CollectivePage{}, wrap(step+": read collective", err)
	}
	if c.Status != domain.CollectiveStatusApproved {
		return CollectivePage{}, domain.ErrNotFound
	}
	views, err := r.Memberships.ListByCollective(ctx, collectiveID)
	if err != nil {
		return CollectivePage{}, wrap(step+": list members", err)
	}
	page := CollectivePage{Collective: c}
	for _, v := range views {
		switch {
		case v.Status == domain.MembershipStatusApproved:
			page.Members = append(page.Members, v)
		case v.Status == domain.MembershipStatusPending && v.OwnerApprovedAt == nil && v.UserStatus == domain.UserStatusActive:
			page.Requests = append(page.Requests, v)
		}
	}
	return page, nil
}

// adminApproveJoinTx sets the admin side of the pending request m of requester to
// join c, which the caller read in the transaction r, writes the audit entry and
// adds the request's line to the caller's decision d (the caller sends d, so the
// person gets one email for everything decided). The request becomes approved,
// and decided now, only when its Administrador side is set; otherwise the line
// says it waits for an Administrador. The caller has checked that the actor is
// the site admin, that c is approved and that m is pending with its admin side
// open. It does not check the requester's account: the admin's queue also decides
// the requests of an account it approves in the same call.
func adminApproveJoinTx(ctx context.Context, r domain.Repos, actor, requester domain.User, c domain.Collective, m domain.Membership, d *decision, now time.Time) error {
	const step = "admin approve join"
	at := now
	m.AdminApprovedBy, m.AdminApprovedAt = actor.ID, &at
	outcome := mail.DecisionWaiting
	if m.OwnerApprovedAt != nil {
		m.Status, m.DecidedAt = domain.MembershipStatusApproved, &at
		outcome = mail.DecisionApproved
	}
	if err := r.Memberships.Update(ctx, m); err != nil {
		return wrap(step+": update membership", err)
	}
	if err := joinTouch(ctx, r, c, now, step); err != nil {
		return err
	}
	d.add(whatJoin(c.Name, m.Role), outcome, "")
	return joinAudit(ctx, r, domain.AuditJoinAdminApproved, actor, requester, c, RoleLabel(m.Role), "", now)
}

// rejectJoinTx deletes the pending request m of requester to join c, which the
// caller read in the transaction r, writes the audit entry and adds the request's
// line to the caller's decision d. reason is already cleaned by CheckReason. The
// caller has checked that actor may reject it, that c is approved and that m is
// pending.
func rejectJoinTx(ctx context.Context, r domain.Repos, actor, requester domain.User, c domain.Collective, m domain.Membership, reason string, d *decision, now time.Time) error {
	const step = "reject join"
	if err := r.Memberships.Delete(ctx, m.UserID, c.ID); err != nil {
		return wrap(step+": delete membership", err)
	}
	if err := joinTouch(ctx, r, c, now, step); err != nil {
		return err
	}
	d.add(whatJoin(c.Name, m.Role), mail.DecisionRejected, reason)
	return joinAudit(ctx, r, domain.AuditJoinRejected, actor, requester, c, RoleLabel(m.Role), reason, now)
}

// joinApprovedCollective reads the collective in the caller's transaction. A
// collective that is not approved has no members to decide about, so it is
// ErrNotAllowed; an unknown one is domain.ErrNotFound.
func joinApprovedCollective(ctx context.Context, r domain.Repos, collectiveID, step string) (domain.Collective, error) {
	c, err := r.Collectives.ByID(ctx, collectiveID)
	if err != nil {
		return domain.Collective{}, wrap(step+": read collective", err)
	}
	if c.Status != domain.CollectiveStatusApproved {
		return domain.Collective{}, ErrNotAllowed
	}
	return c, nil
}

// joinOwnerOf checks, in the caller's transaction, that actor is an approved
// Administrador of the collective, which is approved and not locked, and returns
// the collective. The actor comes first, so someone who is not an Administrador
// learns nothing about the collective, not even that it exists.
func joinOwnerOf(ctx context.Context, r domain.Repos, actor domain.User, collectiveID, step string) (domain.Collective, error) {
	if _, err := requireOwner(ctx, r, actor, collectiveID); err != nil {
		return domain.Collective{}, err
	}
	c, err := joinApprovedCollective(ctx, r, collectiveID, step)
	if err != nil {
		return domain.Collective{}, err
	}
	if c.Locked {
		return domain.Collective{}, ErrLocked
	}
	return c, nil
}

// joinActiveRequester reads the user who asked to join. A request is approved
// only for an account that is active, so one that is not (or is gone) is
// ErrNotAllowed.
func joinActiveRequester(ctx context.Context, r domain.Repos, userID, step string) (domain.User, error) {
	u, err := r.Users.ByID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, ErrNotAllowed
	}
	if err != nil {
		return domain.User{}, wrap(step+": read requester", err)
	}
	if u.Status != domain.UserStatusActive {
		return domain.User{}, ErrNotAllowed
	}
	return u, nil
}

// joinMemberUser reads the user of a membership the caller has just read. A
// deleted user's memberships go with them, so a membership without its user is a
// broken invariant: an error that is not domain.ErrNotFound, so the web layer
// does not answer "not found" for a request that exists.
func joinMemberUser(ctx context.Context, r domain.Repos, userID, step string) (domain.User, error) {
	u, err := r.Users.ByID(ctx, userID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.User{}, errors.New("approvals: " + step + ": the membership's user does not exist")
	}
	if err != nil {
		return domain.User{}, wrap(step+": read user", err)
	}
	return u, nil
}

// joinTouch writes the collective back with UpdatedAt set to now. The write
// changes no decision; it makes this transaction conflict with every other that
// writes the collective, which is what serializes the flows that depend on its
// members.
func joinTouch(ctx context.Context, r domain.Repos, c domain.Collective, now time.Time, step string) error {
	c.UpdatedAt = now
	if err := r.Collectives.Update(ctx, c); err != nil {
		return wrap(step+": update collective", err)
	}
	return nil
}

// joinAudit writes the audit entry of an action by actor about user in c.
func joinAudit(ctx context.Context, r domain.Repos, action domain.AuditAction, actor, user domain.User, c domain.Collective, detail, reason string, now time.Time) error {
	e := auditBy(action, actor, now)
	e.UserID, e.UserName, e.CollectiveID, e.CollectiveName, e.Detail, e.Reason = user.ID, user.Name, c.ID, c.Name, detail, reason
	return audit(ctx, r, e)
}

// joinChangeRole gives the approved membership m of user in c the role, writes
// the collective back and the audit entry. It queues no email.
func joinChangeRole(ctx context.Context, r domain.Repos, action domain.AuditAction, actor, user domain.User, c domain.Collective, m domain.Membership, role domain.Role, now time.Time, step string) error {
	m.Role = role
	if err := r.Memberships.Update(ctx, m); err != nil {
		return wrap(step+": update membership", err)
	}
	if err := joinTouch(ctx, r, c, now, step); err != nil {
		return err
	}
	return joinAudit(ctx, r, action, actor, user, c, "", "", now)
}
