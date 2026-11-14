package approvals

import (
	"cmp"
	"context"
	"errors"
	"slices"
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
// to create, or a request to join one), the oldest request first (Items follow
// the order of Memberships.ListAwaitingAdmin).
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
// The email links to the sign-in page under origin: the person can sign in now.
//
// It returns ErrNotAllowed when actor is not an active site admin;
// domain.ErrNotFound when the account does not exist; and ErrDecided when it is
// not awaiting approval (it is active already, has not confirmed its email, or
// is disabled). An approval that races with another decision on the same account
// gets ErrDecided (it lost to an approval) or domain.ErrNotFound (it lost to a
// rejection).
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
		return d.send(ctx, r, origin+pathLogin, now)
	})
}

// ApprovePerson is "Aprovar tudo": it decides everything about userID that waits
// for the site admin actor in one call. For an account that awaits approval that
// is the account itself, then every collective the person created that still
// awaits the admin and the admin side of every join request of the person that
// awaits it. For an active account there is no account step: it decides the open
// items only. The person gets one email for all of it and each decision writes
// its own audit entry, all in the same transaction. A join request becomes
// approved only when the Administradores of the collective have decided it too;
// otherwise the email says it waits for them. The Administradores of each approved
// collective with a request that they have not decided get the request notice
// (never the person's own new collectives: the person is their Master). A pending
// collective the person did not create is not theirs to decide: it is skipped, and
// so is a request that is not open.
//
// It returns ErrNotAllowed when actor is not an active site admin;
// domain.ErrNotFound when the account does not exist; and ErrDecided when the
// account is neither awaiting approval nor active (has not confirmed its email, or
// is disabled), or when there was nothing to decide: the account was active and no
// item of the person awaited the admin. It returns an error that is none of the
// errors of this package when a request of the person points at a collective that
// does not exist (a broken invariant: the transaction is rolled back).
func ApprovePerson(ctx context.Context, store domain.Store, origin string, actor domain.User, userID string, now time.Time) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	return store.WithTx(ctx, func(ctx context.Context, r domain.Repos) error {
		const step = "approve person"
		person, err := r.Users.ByID(ctx, userID)
		if err != nil {
			return wrap(step+": read user", err)
		}
		if person.Status != domain.UserStatusPendingApproval && person.Status != domain.UserStatusActive {
			return ErrDecided
		}
		views, err := r.Memberships.ListByUser(ctx, person.ID)
		if err != nil {
			return wrap(step+": list memberships", err)
		}
		d := newDecision(person.Email, person.Name)
		if person.Status == domain.UserStatusPendingApproval {
			if err := approveAccountTx(ctx, r, origin, actor, person, views, d, now); err != nil {
				return err
			}
		}
		for _, v := range views {
			if v.Status != domain.MembershipStatusPending || v.AdminApprovedAt != nil {
				continue
			}
			c, err := accountsCollective(ctx, r, v, step)
			if err != nil {
				return err
			}
			switch {
			case c.Status == domain.CollectiveStatusPending && c.CreatedBy == person.ID:
				err = approveCollectiveTx(ctx, r, actor, person, c, d, now)
			case c.Status == domain.CollectiveStatusApproved:
				err = adminApproveJoinTx(ctx, r, actor, person, c, v.Membership, d, now)
			}
			if err != nil {
				return err
			}
		}
		if len(d.items) == 0 {
			return ErrDecided
		}
		return d.send(ctx, r, origin+pathCollectives, now)
	})
}

// RejectAccount rejects the account of userID for the site admin actor: it
// deletes the account (with its sessions, tokens and memberships) and the
// collectives the person created that still await the admin, emails the person
// (with the reason, when there is one, on each line, and a link to the sign-up
// page under origin, so they can sign up again) and writes an audit entry for the
// account and one for each collective that went with it, in the same transaction.
// The person's requests to join other collectives go with the account without a
// line or an audit entry of their own, and a pending collective somebody else
// created is left as it is. The reason is optional and is cleaned by CheckReason.
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
				continue
			}
			c, err := accountsCollective(ctx, r, v, step)
			if err != nil {
				return err
			}
			if c.Status != domain.CollectiveStatusPending || c.CreatedBy != person.ID {
				// Not the person's own new collective: the membership goes with
				// the account (Users.Delete) and the collective is not theirs.
				continue
			}
			if err := rejectCollectiveTx(ctx, r, actor, person, c, reason, d, now); err != nil {
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
// CheckReason), in the same transaction. It queues no email: the person is shut
// out, not told a decision about a request. The person's collectives and requests
// stay as they are.
//
// It returns ErrNotAllowed when actor is not an active site admin; ErrSelf when
// actor is userID (checked right after the actor); domain.FieldErrors with the key
// "reason" for a bad reason; domain.ErrNotFound when the account does not exist;
// and ErrDecided when it is not active (it is disabled already, or still awaits
// approval or confirmation: a pending account is approved or rejected, not
// blocked).
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
		if person.Status != domain.UserStatusActive {
			return ErrDecided
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
// It returns ErrNotAllowed when actor is not an active site admin;
// domain.ErrNotFound when the account does not exist; and ErrDecided when it is
// not disabled.
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
		if person.Status != domain.UserStatusDisabled {
			return ErrDecided
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
// the accounts that await approval and the active accounts with requests that
// await the admin. Every card has the account or at least one request to decide.
// The cards are in one order: by the time of the oldest request of the card (the
// card's first item), a card without requests by the creation of its account, the
// oldest first, and the items of a card by the time of the request. Only the first
// domain.ListLimitMax accounts and the first domain.ListLimitMax requests are
// read. It runs no transaction: the queue is a read, and a decision that lands
// between its queries shows up on the next one.
//
// It returns ErrNotAllowed when actor is not an active site admin.
func AdminQueue(ctx context.Context, store domain.Store, actor domain.User) ([]PersonCard, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	r := store.Repos()
	awaiting, err := r.Memberships.ListAwaitingAdmin(ctx, domain.ListLimitMax)
	if err != nil {
		return nil, wrap("admin queue: list requests", err)
	}
	pending, err := r.Users.ListByStatus(ctx, domain.UserStatusPendingApproval, domain.ListLimitMax)
	if err != nil {
		return nil, wrap("admin queue: list pending accounts", err)
	}
	var cards []PersonCard
	at := make(map[string]int) // user ID -> index in cards, or -1 for a user left out
	for _, v := range awaiting {
		i, seen := at[v.UserID]
		if !seen {
			u, err := r.Users.ByID(ctx, v.UserID)
			switch {
			case errors.Is(err, domain.ErrNotFound):
				i = -1
			case err != nil:
				return nil, wrap("admin queue: read user", err)
			case u.Status == domain.UserStatusActive || u.Status == domain.UserStatusPendingApproval:
				i = len(cards)
				cards = append(cards, PersonCard{User: u})
			default:
				i = -1
			}
			at[v.UserID] = i
		}
		if i >= 0 {
			cards[i].Items = append(cards[i].Items, v)
		}
	}
	for _, u := range pending {
		if _, seen := at[u.ID]; !seen {
			at[u.ID] = len(cards)
			cards = append(cards, PersonCard{User: u})
		}
	}
	queuedAt := func(p PersonCard) time.Time {
		if len(p.Items) > 0 {
			return p.Items[0].RequestedAt
		}
		return p.User.CreatedAt
	}
	slices.SortStableFunc(cards, func(a, b PersonCard) int {
		if c := queuedAt(a).Compare(queuedAt(b)); c != 0 {
			return c
		}
		return cmp.Compare(a.User.ID, b.User.ID)
	})
	return cards, nil
}

// accountPending reads the person a decision is about, in the caller's
// transaction, and returns it only when the account awaits approval. Any other
// status is ErrDecided (the account is not awaiting the admin: it was approved
// already, or has not confirmed its email, or is disabled); an unknown account is
// domain.ErrNotFound.
func accountPending(ctx context.Context, r domain.Repos, userID, step string) (domain.User, error) {
	u, err := r.Users.ByID(ctx, userID)
	if err != nil {
		return domain.User{}, wrap(step+": read user", err)
	}
	if u.Status != domain.UserStatusPendingApproval {
		return domain.User{}, ErrDecided
	}
	return u, nil
}

// accountsCollective reads, in the caller's transaction, the collective of the
// membership view v, right before the caller decides it (see the conventions at
// the top of internal/domain/repo.go). A membership always has its collective, so
// a missing one is a broken invariant: the error matches none of the errors of
// this package and not domain.ErrNotFound, and the caller rolls the transaction
// back.
func accountsCollective(ctx context.Context, r domain.Repos, v domain.MembershipView, step string) (domain.Collective, error) {
	c, err := r.Collectives.ByID(ctx, v.CollectiveID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Collective{}, errors.New("approvals: " + step + ": the collective of a membership does not exist")
	}
	if err != nil {
		return domain.Collective{}, wrap(step+": read collective", err)
	}
	return c, nil
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
