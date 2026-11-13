package approvals

import (
	"context"
	"errors"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// The account decisions of the site admin. The admin's panel shows one card per
// person (PersonCard). For an account that awaits approval the card offers to
// approve everything at once (ApprovePerson), to approve only the account
// (ApproveAccount), to reject the account (RejectAccount) and to reject each
// request (RejectCollective, RejectJoin). Approving one request alone is only for
// an active account: ApproveCollective and AdminApproveJoin refuse a person who
// is still pending.
//
// Every write path reads each collective it decides in its transaction right
// before it writes it, and never writes an older copy of it afterwards: the
// helpers of collectives.go and joins.go write the whole collective back (see
// the conventions at the top of internal/domain/repo.go). The membership views
// from the store only classify the items and order them.

// PersonCard is one person in the site admin's queue: the account and the
// requests of the person that wait for the admin (a collective the person asked
// to create, or a request to join one), oldest first when the account is active
// and in the order of the collective names when it is pending.
type PersonCard struct {
	User  domain.User
	Items []domain.MembershipView
}

// AccountPending reports whether the account still awaits the admin.
func (p PersonCard) AccountPending() bool { return p.User.Status == domain.UserStatusPendingApproval }

// ApproveAccount approves only the account of userID, for the site admin actor:
// the person becomes active, is emailed (links under origin) and an audit entry
// is written, in the same transaction. The collectives the person created and
// the join requests stay as they are, for the admin to decide one by one. The
// Administradores of each approved collective with a pending request of the
// person that they have not decided get the request notice, as the request
// becomes visible to them now.
//
// It returns ErrNotAllowed when actor is not an active site admin or the account
// has not confirmed its email or is disabled; domain.ErrNotFound when it does
// not exist; and ErrDecided when it is already active. An approval that races
// with another decision on the same account gets ErrDecided (it lost to an
// approval) or domain.ErrNotFound (it lost to a rejection).
func ApproveAccount(ctx context.Context, store domain.Store, origin string, actor domain.User, userID string, now time.Time) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "approve account"
		person, err := accountPending(ctx, r, userID, step)
		if err != nil {
			return err
		}
		views, err := r.Memberships.ListByUser(ctx, person.ID)
		if err != nil {
			return wrap(step+": list memberships", err)
		}
		d := newDecision(person.Email, person.Name)
		if err := approveAccountTx(ctx, r, origin, actor, person, views, d, now); err != nil {
			return err
		}
		return d.send(ctx, r, origin+pathCollectives, now)
	})
}

// ApprovePerson is "Aprovar tudo": it approves the account of userID, every
// collective the person created that still awaits the admin and the admin side
// of every join request of the person that awaits it, for the site admin actor.
// The person gets one email for all of it and each decision writes its own audit
// entry, all in the same transaction. A join request becomes approved only when
// the Administradores of the collective have decided it too; otherwise the email
// says it waits for them. The Administradores of each approved collective with a
// request that they have not decided get the request notice (never the person's
// own new collectives: the person is their Master).
//
// It returns the errors of ApproveAccount, and an error that is none of the
// errors of this package when a request of the person points at a collective
// that cannot be decided (a broken invariant: the transaction is rolled back). An
// active account is ErrDecided: what waits for an active person is decided one
// item at a time.
func ApprovePerson(ctx context.Context, store domain.Store, origin string, actor domain.User, userID string, now time.Time) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "approve person"
		person, err := accountPending(ctx, r, userID, step)
		if err != nil {
			return err
		}
		views, err := r.Memberships.ListByUser(ctx, person.ID)
		if err != nil {
			return wrap(step+": list memberships", err)
		}
		d := newDecision(person.Email, person.Name)
		if err := approveAccountTx(ctx, r, origin, actor, person, views, d, now); err != nil {
			return err
		}
		for _, v := range views {
			if v.Status != domain.MembershipStatusPending || v.AdminApprovedAt != nil {
				continue
			}
			c, err := r.Collectives.ByID(ctx, v.CollectiveID)
			if errors.Is(err, domain.ErrNotFound) {
				return errors.New("approvals: " + step + ": the collective of a membership does not exist")
			}
			if err != nil {
				return wrap(step+": read collective", err)
			}
			switch c.Status {
			case domain.CollectiveStatusPending:
				if c.CreatedBy != person.ID {
					return errors.New("approvals: " + step + ": the person has a request to join a collective that is not approved")
				}
				err = approveCollectiveTx(ctx, r, actor, person, c, d, now)
			case domain.CollectiveStatusApproved:
				err = adminApproveJoinTx(ctx, r, actor, person, c, v.Membership, d, now)
			}
			if err != nil {
				return err
			}
		}
		return d.send(ctx, r, origin+pathCollectives, now)
	})
}

// RejectAccount rejects the account of userID for the site admin actor: it
// deletes the account, the collectives the person created that still await the
// admin and the person's join requests, emails the person (with the reason, when
// there is one, on each line, and a link to the sign-up page under origin, so
// they can sign up again) and writes an audit entry for the account and one for
// each collective and request that went with it, in the same transaction. The
// reason is optional and is cleaned by CheckReason.
//
// It returns domain.FieldErrors with the key "reason" for a bad reason (checked
// after the actor), and otherwise the errors of ApproveAccount. A rejection that
// races with an approval of the same account gets domain.ErrNotFound (it lost to
// another rejection) or ErrDecided (it lost to an approval).
func RejectAccount(ctx context.Context, store domain.Store, origin string, actor domain.User, userID, reason string, now time.Time) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	reason, err := CheckReason(reason)
	if err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "reject account"
		person, err := accountPending(ctx, r, userID, step)
		if err != nil {
			return err
		}
		views, err := r.Memberships.ListByUser(ctx, person.ID)
		if err != nil {
			return wrap(step+": list memberships", err)
		}
		d := newDecision(person.Email, person.Name)
		d.add(whatAccount, mail.DecisionRejected, reason)
		for _, v := range views {
			if v.Status != domain.MembershipStatusPending {
				// An approved membership goes with the account and is no
				// request: it has nothing to tell the person.
				continue
			}
			c, err := r.Collectives.ByID(ctx, v.CollectiveID)
			if errors.Is(err, domain.ErrNotFound) {
				return errors.New("approvals: " + step + ": the collective of a membership does not exist")
			}
			if err != nil {
				return wrap(step+": read collective", err)
			}
			switch c.Status {
			case domain.CollectiveStatusPending:
				if c.CreatedBy != person.ID {
					return errors.New("approvals: " + step + ": the person has a request to join a collective that is not approved")
				}
				err = rejectCollectiveTx(ctx, r, actor, person, c, reason, d, now)
			case domain.CollectiveStatusApproved:
				err = rejectJoinTx(ctx, r, actor, person, c, v.Membership, reason, d, now)
			}
			if err != nil {
				return err
			}
		}
		if err := r.Users.Delete(ctx, person.ID); err != nil {
			return wrap(step+": delete user", err)
		}
		e := auditBy(domain.AuditAccountRejected, actor, now)
		e.UserID, e.UserName, e.Reason = person.ID, person.Name, reason
		if err := audit(ctx, r, e); err != nil {
			return err
		}
		return d.send(ctx, r, origin+pathSignup, now)
	})
}

// BlockAccount disables the active account of userID for the site admin actor:
// it ends every session of the person, so the block takes effect on their next
// request, and writes an audit entry with the optional reason (cleaned by
// CheckReason), in the same transaction. It queues no email. The person's
// collectives and requests stay as they are.
//
// It returns ErrNotAllowed when actor is not an active site admin or the account
// is still pending (a pending account is approved or rejected, not blocked);
// ErrSelf when actor is userID (checked right after the actor); domain.FieldErrors
// with the key "reason" for a bad reason; domain.ErrNotFound when the account
// does not exist; and ErrDecided when it is already disabled.
func BlockAccount(ctx context.Context, store domain.Store, actor domain.User, userID, reason string, now time.Time) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if userID == actor.ID {
		return ErrSelf
	}
	reason, err := CheckReason(reason)
	if err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "block account"
		person, err := r.Users.ByID(ctx, userID)
		if err != nil {
			return wrap(step+": read user", err)
		}
		switch person.Status {
		case domain.UserStatusActive:
		case domain.UserStatusDisabled:
			return ErrDecided
		default:
			return ErrNotAllowed
		}
		if err := r.Users.SetStatus(ctx, person.ID, domain.UserStatusDisabled, now); err != nil {
			return wrap(step+": set status", err)
		}
		if err := r.Sessions.DeleteForUser(ctx, person.ID, nil); err != nil {
			return wrap(step+": end sessions", err)
		}
		e := auditBy(domain.AuditAccountBlocked, actor, now)
		e.UserID, e.UserName, e.Reason = person.ID, person.Name, reason
		return audit(ctx, r, e)
	})
}

// UnblockAccount makes the disabled account of userID active again for the site
// admin actor and writes an audit entry, in the same transaction. The person
// signs in again; nothing else about them changed while they were blocked. It
// queues no email.
//
// It returns ErrNotAllowed when actor is not an active site admin or the account
// is pending; domain.ErrNotFound when it does not exist; and ErrDecided when it
// is already active.
func UnblockAccount(ctx context.Context, store domain.Store, actor domain.User, userID string, now time.Time) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "unblock account"
		person, err := r.Users.ByID(ctx, userID)
		if err != nil {
			return wrap(step+": read user", err)
		}
		switch person.Status {
		case domain.UserStatusDisabled:
		case domain.UserStatusActive:
			return ErrDecided
		default:
			return ErrNotAllowed
		}
		if err := r.Users.SetStatus(ctx, person.ID, domain.UserStatusActive, now); err != nil {
			return wrap(step+": set status", err)
		}
		e := auditBy(domain.AuditAccountUnblocked, actor, now)
		e.UserID, e.UserName = person.ID, person.Name
		return audit(ctx, r, e)
	})
}

// AdminQueue returns the people the site admin has something to decide about:
// first the accounts that await approval, the oldest first, each with the
// requests of the person that await the admin; then the active accounts that
// have such requests, the one with the oldest request first. Every card has the
// account or at least one request to decide. Only the first domain.ListLimitMax
// accounts and the first domain.ListLimitMax requests are read. It runs no
// transaction: the queue is a read, and a decision that lands between its
// queries shows up on the next one.
//
// It returns ErrNotAllowed when actor is not an active site admin.
func AdminQueue(ctx context.Context, store domain.Store, actor domain.User) ([]PersonCard, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	r := store.Repos()
	pending, err := r.Users.ListByStatus(ctx, domain.UserStatusPendingApproval, domain.ListLimitMax)
	if err != nil {
		return nil, wrap("admin queue: list pending accounts", err)
	}
	awaiting, err := r.Memberships.ListAwaitingAdmin(ctx, domain.ListLimitMax)
	if err != nil {
		return nil, wrap("admin queue: list requests", err)
	}
	byUser := make(map[string][]domain.MembershipView)
	var order []string // the users with requests, the oldest request first
	for _, v := range awaiting {
		if _, ok := byUser[v.UserID]; !ok {
			order = append(order, v.UserID)
		}
		byUser[v.UserID] = append(byUser[v.UserID], v)
	}
	var cards []PersonCard
	for _, u := range pending {
		cards = append(cards, PersonCard{User: u, Items: byUser[u.ID]})
		delete(byUser, u.ID)
	}
	for _, id := range order {
		items, ok := byUser[id]
		if !ok {
			continue
		}
		u, err := r.Users.ByID(ctx, id)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, wrap("admin queue: read user", err)
		}
		if u.Status != domain.UserStatusActive {
			continue
		}
		cards = append(cards, PersonCard{User: u, Items: items})
	}
	return cards, nil
}

// accountPending reads the person a decision is about, in the caller's
// transaction, and returns it only when the account awaits approval. An active
// account is ErrDecided (the decision was made); one that has not confirmed its
// email or is disabled is ErrNotAllowed; an unknown one is domain.ErrNotFound.
func accountPending(ctx context.Context, r domain.Repos, userID, step string) (domain.User, error) {
	u, err := r.Users.ByID(ctx, userID)
	if err != nil {
		return domain.User{}, wrap(step+": read user", err)
	}
	switch u.Status {
	case domain.UserStatusPendingApproval:
		return u, nil
	case domain.UserStatusActive:
		return domain.User{}, ErrDecided
	}
	return domain.User{}, ErrNotAllowed
}

// approveAccountTx approves the pending account of person, whom the caller read
// in the transaction r, adds the account's line to the caller's decision d and
// writes the audit entry. views are the person's memberships: for each pending
// request to join an approved collective that the collective's Administradores
// have not decided, they are emailed the request notice, as the request is
// visible to them from now on (the person's own new collective has no
// Administrador to tell: its creator is the person).
func approveAccountTx(ctx context.Context, r domain.Repos, origin string, actor, person domain.User, views []domain.MembershipView, d *decision, now time.Time) error {
	if err := r.Users.Approve(ctx, person.ID, actor.ID, now); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			return ErrDecided
		}
		return wrap("approve account: approve user", err)
	}
	d.add(whatAccount, mail.DecisionApproved, "")
	e := auditBy(domain.AuditAccountApproved, actor, now)
	e.UserID, e.UserName = person.ID, person.Name
	if err := audit(ctx, r, e); err != nil {
		return err
	}
	for _, v := range views {
		if v.Status != domain.MembershipStatusPending || v.OwnerApprovedAt != nil || v.CollectiveStatus != domain.CollectiveStatusApproved {
			continue
		}
		notice := mail.OwnerJoinRequestData{
			CollectiveName: v.CollectiveName,
			UserName:       person.Name,
			Role:           RoleLabel(v.Role),
			Link:           origin + collectivePath(v.CollectiveID),
		}
		if err := notifyOwners(ctx, r, v.CollectiveID, notice, now); err != nil {
			return err
		}
	}
	return nil
}
