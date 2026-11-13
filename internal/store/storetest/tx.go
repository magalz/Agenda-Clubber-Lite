package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// errRollback is what the subtests return from fn to roll the transaction back.
var errRollback = errors.New("storetest: roll back")

// written is what writeEverything stored.
type written struct {
	user       domain.User
	token      domain.Token
	session    domain.Session
	collective domain.Collective
	message    domain.OutboxMessage
	audit      domain.AuditEntry
}

// writeEverything creates one record of every kind through r, with a
// membership that makes the user an approved owner of an approved collective.
func writeEverything(ctx context.Context, r domain.Repos) (written, error) {
	var w written
	w.user = newUser("ana")
	if err := r.Users.Create(ctx, &w.user); err != nil {
		return w, fmt.Errorf("create user: %w", err)
	}
	w.token = newToken(w.user.ID, resetPassword, "tx-token")
	if err := r.Tokens.Create(ctx, &w.token); err != nil {
		return w, fmt.Errorf("create token: %w", err)
	}
	w.session = newSession(w.user.ID, "tx-session")
	if err := r.Sessions.Create(ctx, w.session); err != nil {
		return w, fmt.Errorf("create session: %w", err)
	}
	w.collective = newCollective("Casa Azul", coletivo, cApproved)
	if err := r.Collectives.Create(ctx, &w.collective); err != nil {
		return w, fmt.Errorf("create collective: %w", err)
	}
	if err := r.Memberships.Create(ctx, newMembership(w.user, w.collective, roleOwner, mApproved)); err != nil {
		return w, fmt.Errorf("create membership: %w", err)
	}
	w.message = newMessage(w.user.Email)
	if err := r.Outbox.Enqueue(ctx, &w.message); err != nil {
		return w, fmt.Errorf("enqueue message: %w", err)
	}
	w.audit = newAuditEntry(func(a *domain.AuditEntry) {
		a.UserID, a.UserName = w.user.ID, w.user.Name
		a.CollectiveID, a.CollectiveName = w.collective.ID, w.collective.Name
	})
	if err := r.AuditEntries.Create(ctx, &w.audit); err != nil {
		return w, fmt.Errorf("create audit entry: %w", err)
	}
	return w, nil
}

// readBack checks that r sees what writeEverything wrote. It leaves out Consume
// and Claim, which change what they read.
func readBack(ctx context.Context, r domain.Repos, w written) error {
	if _, err := r.Users.ByID(ctx, w.user.ID); err != nil {
		return fmt.Errorf("read your own user by ID: %w", err)
	}
	if _, err := r.Users.ByEmail(ctx, w.user.Email); err != nil {
		return fmt.Errorf("read your own user by email: %w", err)
	}
	if _, err := r.Sessions.Get(ctx, w.session.Hash); err != nil {
		return fmt.Errorf("read your own session: %w", err)
	}
	if _, err := r.Collectives.ByNameKey(ctx, w.collective.NameKey); err != nil {
		return fmt.Errorf("read your own collective: %w", err)
	}
	views, err := r.Memberships.ListByUser(ctx, w.user.ID)
	if err != nil || len(views) != 1 {
		return fmt.Errorf("read your own membership: %d memberships, error %v", len(views), err)
	}
	emails, err := r.Memberships.OwnerEmails(ctx, w.collective.ID)
	if err != nil || len(emails) != 1 {
		return fmt.Errorf("read your own owner: %q, error %v", emails, err)
	}
	if ok, err := r.Memberships.UserCanCreateEvents(ctx, w.user.ID); err != nil || !ok {
		return fmt.Errorf("UserCanCreateEvents over your own writes = %v, error %v", ok, err)
	}
	entries, err := r.AuditEntries.List(ctx, domain.ListLimitMax)
	if err != nil || len(entries) != 1 || entries[0].ID != w.audit.ID {
		return fmt.Errorf("read your own audit entry: %d entries, error %v", len(entries), err)
	}
	return nil
}

// wantStored checks, outside any transaction, that everything writeEverything
// wrote is there.
func (e env) wantStored(w written) {
	e.t.Helper()
	e.getUser(w.user.ID)
	if _, err := e.r.Sessions.Get(e.ctx, w.session.Hash); err != nil {
		e.t.Errorf("session: %v", err)
	}
	if _, err := e.r.Collectives.ByID(e.ctx, w.collective.ID); err != nil {
		e.t.Errorf("collective: %v", err)
	}
	if got := e.views(w.user.ID); len(got) != 1 {
		e.t.Errorf("ListByUser has %d memberships, want 1", len(got))
	}
	if _, err := e.r.Tokens.Consume(e.ctx, w.token.Purpose, w.token.Hash, at(time.Minute)); err != nil {
		e.t.Errorf("token: %v", err)
	}
	wantIDs(e.t, "Claim", e.claim(base, leaseFor, 10), w.message)
	wantIDsBy(e.t, "audit List", auditID, e.auditList(domain.ListLimitMax), w.audit)
}

// wantNothing checks that nothing writeEverything wrote is there.
func (e env) wantNothing(w written) {
	e.t.Helper()
	_, err := e.r.Users.ByID(e.ctx, w.user.ID)
	isErr(e.t, "user by ID", err, domain.ErrNotFound)
	_, err = e.r.Users.ByEmail(e.ctx, w.user.Email)
	isErr(e.t, "user by email", err, domain.ErrNotFound)
	_, err = e.r.Sessions.Get(e.ctx, w.session.Hash)
	isErr(e.t, "session", err, domain.ErrNotFound)
	_, err = e.r.Collectives.ByID(e.ctx, w.collective.ID)
	isErr(e.t, "collective by ID", err, domain.ErrNotFound)
	_, err = e.r.Collectives.ByNameKey(e.ctx, w.collective.NameKey)
	isErr(e.t, "collective by name key", err, domain.ErrNotFound)
	if got := e.views(w.user.ID); len(got) != 0 {
		e.t.Errorf("ListByUser has %d memberships, want none", len(got))
	}
	_, err = e.r.Tokens.Consume(e.ctx, w.token.Purpose, w.token.Hash, at(time.Minute))
	isErr(e.t, "token", err, domain.ErrNotFound)
	if got := e.claim(base, leaseFor, 10); len(got) != 0 {
		e.t.Errorf("Claim returned %d messages, want none", len(got))
	}
	if got := e.auditList(domain.ListLimitMax); len(got) != 0 {
		e.t.Errorf("audit List returned %d entries, want none", len(got))
	}
}

// txFixture is committed data that the update and delete subtests change in a
// transaction: ana (a user with a session, a membership and a message waiting)
// and victim (a user with a session and a membership), plus two collectives. The
// admin approves newcomer (a user waiting for the approval) and the collective
// under review, where joiner has a pending membership to decide and leaver an
// approved one to remove.
type txFixture struct {
	ana, victim                     domain.User
	anaSession                      domain.Session
	victimSession                   domain.Session
	kept, doomed                    domain.Collective
	message                         domain.OutboxMessage
	anaNewPasswordTag               string
	admin, newcomer, joiner, leaver domain.User
	review                          domain.Collective
	joining, leaving                domain.Membership
}

func (e env) txFixture() txFixture {
	e.t.Helper()
	f := txFixture{anaNewPasswordTag: "other-hash"}
	f.ana, f.victim = e.user("ana"), e.user("victim")
	f.kept = e.collective("Casa Azul", coletivo, cApproved)
	f.doomed = e.collective("Casa Verde", produtora, cApproved)
	e.member(f.victim, f.kept, roleOwner, mApproved)
	e.member(f.ana, f.doomed, roleOwner, mApproved)
	f.anaSession = e.session(f.ana.ID, "ana-session")
	f.victimSession = e.session(f.victim.ID, "victim-session")
	f.message = e.message(f.ana.Email)
	f.admin, f.newcomer = e.user("admin"), e.user("newcomer", userStatus(uPendingApproval))
	f.joiner, f.leaver = e.user("joiner"), e.user("leaver")
	f.review = e.collective("Casa Rosa", produtora, cPending)
	f.joining = e.member(f.joiner, f.review, roleMember, mPending)
	f.leaving = e.member(f.leaver, f.review, roleMember, mApproved, decided(f.admin.ID, f.admin.ID))
	return f
}

// The values mutate writes. Each differs from the stored one in every field the
// update takes, so an update that did not happen, or that did not reach the
// store, shows.

func (f txFixture) approved() domain.User {
	u := f.newcomer
	u.Status, u.ApprovedBy, u.ApprovedAt, u.UpdatedAt = uActive, f.admin.ID, ptr(at(time.Minute)), at(time.Minute)
	return u
}

func (f txFixture) reviewed() domain.Collective {
	c := f.review
	c.Name, c.NameKey = "Casa Rosa Nova", domain.NameKey("Casa Rosa Nova")
	c.Status, c.ApprovedBy, c.ApprovedAt = cApproved, f.admin.ID, ptr(at(time.Minute))
	c.EventsBlocked, c.Locked, c.UpdatedAt = true, true, at(time.Minute)
	return c
}

func (f txFixture) joined() domain.Membership {
	m := f.joining
	m.Role, m.Status = roleOwner, mApproved
	m.AdminApprovedBy, m.AdminApprovedAt = f.admin.ID, ptr(at(time.Minute))
	m.OwnerApprovedBy, m.OwnerApprovedAt = f.admin.ID, ptr(at(2*time.Minute))
	m.DecidedAt = ptr(at(3 * time.Minute))
	return m
}

// mutate updates and deletes through r: one call of every kind that is not a Create.
func (f txFixture) mutate(ctx context.Context, r domain.Repos) error {
	steps := []struct {
		name string
		do   func() error
	}{
		{"Users.Approve", func() error { return r.Users.Approve(ctx, f.newcomer.ID, f.admin.ID, at(time.Minute)) }},
		{"Collectives.Update", func() error { return r.Collectives.Update(ctx, f.reviewed()) }},
		{"Memberships.Update", func() error { return r.Memberships.Update(ctx, f.joined()) }},
		{"Memberships.Delete", func() error { return r.Memberships.Delete(ctx, f.leaver.ID, f.review.ID) }},
		{"SetStatus", func() error { return r.Users.SetStatus(ctx, f.ana.ID, uDisabled, at(time.Minute)) }},
		{"ChangePassword", func() error { return r.Users.ChangePassword(ctx, f.ana.ID, f.anaNewPasswordTag, at(time.Minute)) }},
		{"Sessions.Delete", func() error { return r.Sessions.Delete(ctx, f.anaSession.Hash) }},
		{"Outbox.MarkSent", func() error { return r.Outbox.MarkSent(ctx, f.message.ID, at(time.Minute)) }},
		{"Users.Delete", func() error { return r.Users.Delete(ctx, f.victim.ID) }},
		{"Collectives.Delete", func() error { return r.Collectives.Delete(ctx, f.doomed.ID) }},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
	}
	return nil
}

// wantUntouched checks that mutate had no effect.
func (f txFixture) wantUntouched(e env) {
	e.t.Helper()
	ana := e.getUser(f.ana.ID)
	if ana.Status != uActive || ana.PasswordHash != f.ana.PasswordHash {
		e.t.Errorf("ana = status %s, password hash %q, want %s and %q", ana.Status, ana.PasswordHash, uActive, f.ana.PasswordHash)
	}
	if _, err := e.r.Sessions.Get(e.ctx, f.anaSession.Hash); err != nil {
		e.t.Errorf("ana's session: %v", err)
	}
	wantIDs(e.t, "Claim", e.claim(base, leaseFor, 10), f.message)
	e.getUser(f.victim.ID)
	if _, err := e.r.Sessions.Get(e.ctx, f.victimSession.Hash); err != nil {
		e.t.Errorf("victim's session: %v", err)
	}
	if got := e.views(f.victim.ID); len(got) != 1 {
		e.t.Errorf("victim has %d memberships, want 1", len(got))
	}
	if _, err := e.r.Collectives.ByID(e.ctx, f.doomed.ID); err != nil {
		e.t.Errorf("collective: %v", err)
	}
	if got := e.views(f.ana.ID); len(got) != 1 {
		e.t.Errorf("ana has %d memberships, want 1", len(got))
	}
	wantEqual(e.t, "newcomer", normUser(e.getUser(f.newcomer.ID)), normUser(f.newcomer))
	got, err := e.r.Collectives.ByID(e.ctx, f.review.ID)
	noErr(e.t, "collective under review", err)
	wantEqual(e.t, "collective under review", normCollective(got), normCollective(f.review))
	_, err = e.r.Collectives.ByNameKey(e.ctx, f.reviewed().NameKey)
	isErr(e.t, "collective by the name Update gives it", err, domain.ErrNotFound)
	wantEqual(e.t, "joiner's membership", normMembership(e.membership(f.joiner.ID, f.review.ID)), normMembership(f.joining))
	wantEqual(e.t, "leaver's membership", normMembership(e.membership(f.leaver.ID, f.review.ID)), normMembership(f.leaving))
}

// wantMutated checks that mutate took effect.
func (f txFixture) wantMutated(e env) {
	e.t.Helper()
	ana := e.getUser(f.ana.ID)
	if ana.Status != uDisabled || ana.PasswordHash != f.anaNewPasswordTag {
		e.t.Errorf("ana = status %s, password hash %q, want %s and %q", ana.Status, ana.PasswordHash, uDisabled, f.anaNewPasswordTag)
	}
	_, err := e.r.Sessions.Get(e.ctx, f.anaSession.Hash)
	isErr(e.t, "ana's session", err, domain.ErrNotFound)
	wantIDs(e.t, "Claim of a message that was sent", e.claim(at(1000*time.Hour), leaseFor, 10))
	_, err = e.r.Users.ByID(e.ctx, f.victim.ID)
	isErr(e.t, "victim", err, domain.ErrNotFound)
	_, err = e.r.Sessions.Get(e.ctx, f.victimSession.Hash)
	isErr(e.t, "victim's session", err, domain.ErrNotFound)
	_, err = e.r.Collectives.ByID(e.ctx, f.doomed.ID)
	isErr(e.t, "deleted collective", err, domain.ErrNotFound)
	if got := e.views(f.ana.ID); len(got) != 0 {
		e.t.Errorf("ana has %d memberships, want none", len(got))
	}
	if _, err := e.r.Collectives.ByID(e.ctx, f.kept.ID); err != nil {
		e.t.Errorf("the other collective: %v", err)
	}
	wantEqual(e.t, "newcomer", normUser(e.getUser(f.newcomer.ID)), normUser(f.approved()))
	got, err := e.r.Collectives.ByID(e.ctx, f.review.ID)
	noErr(e.t, "collective under review", err)
	wantEqual(e.t, "collective under review", normCollective(got), normCollective(f.reviewed()))
	_, err = e.r.Collectives.ByNameKey(e.ctx, f.review.NameKey)
	isErr(e.t, "collective by the name Update took from it", err, domain.ErrNotFound)
	wantEqual(e.t, "joiner's membership", normMembership(e.membership(f.joiner.ID, f.review.ID)), normMembership(f.joined()))
	_, err = e.r.Memberships.Get(e.ctx, f.leaver.ID, f.review.ID)
	isErr(e.t, "leaver's membership", err, domain.ErrNotFound)
}

// RunTx runs the contract of Store.WithTx and Store.Ping.
func RunTx(t *testing.T, newStore NewStore) {
	t.Helper()
	runSubtests(t, newStore, []subtest{
		{"Ping succeeds on a working store", func(t *testing.T, e env) {
			noErr(t, "Ping", e.s.Ping(e.ctx))
		}},
		{"Close can be called twice, and the Store is unusable after it", func(t *testing.T, e env) {
			noErr(t, "first Close", e.s.Close(e.ctx))
			noErr(t, "second Close", e.s.Close(e.ctx))
			if err := e.s.Ping(e.ctx); err == nil {
				t.Error("Ping after Close succeeded")
			}
		}},
		{"WithTx commits the writes when fn returns nil, and fn sees its own writes", func(t *testing.T, e env) {
			var w written
			err := e.s.WithTx(e.ctx, func(ctx context.Context, r domain.Repos) error {
				var err error
				if w, err = writeEverything(ctx, r); err != nil {
					return err
				}
				return readBack(ctx, r, w)
			})
			noErr(t, "WithTx", err)
			e.wantStored(w)
		}},
		{"WithTx rolls every write back when fn fails, and returns the error", func(t *testing.T, e env) {
			var w written
			calls := 0
			err := e.s.WithTx(e.ctx, func(ctx context.Context, r domain.Repos) error {
				calls++
				var err error
				if w, err = writeEverything(ctx, r); err != nil {
					return err
				}
				return errRollback
			})
			isErr(t, "WithTx", err, errRollback)
			if calls != 1 {
				t.Errorf("fn ran %d times, want 1: an error from fn is not retried", calls)
			}
			e.wantNothing(w)
			// The unique keys the rolled-back writes took are free again.
			e.user("ana")
			e.collective("Casa Azul", coletivo, cApproved)
		}},
		{"WithTx commits updates and deletes", func(t *testing.T, e env) {
			f := e.txFixture()
			noErr(t, "WithTx", e.s.WithTx(e.ctx, func(ctx context.Context, r domain.Repos) error { return f.mutate(ctx, r) }))
			f.wantMutated(e)
		}},
		{"WithTx rolls updates and deletes back when fn fails", func(t *testing.T, e env) {
			f := e.txFixture()
			err := e.s.WithTx(e.ctx, func(ctx context.Context, r domain.Repos) error {
				if err := f.mutate(ctx, r); err != nil {
					return err
				}
				return errRollback
			})
			isErr(t, "WithTx", err, errRollback)
			f.wantUntouched(e)
		}},
		// A method that is atomic by itself (the cascade of Users.Delete, the
		// retirement of tokens in Tokens.Create) takes part in the transaction of
		// its caller, so the caller's rollback undoes it. A transaction of its own
		// would have committed it.
		{"a call that is atomic by itself joins the transaction of its caller", func(t *testing.T, e env) {
			ana := e.user("ana")
			old := e.token(ana.ID, resetPassword, "old")
			fresh := newToken(ana.ID, resetPassword, "fresh")
			err := e.s.WithTx(e.ctx, func(ctx context.Context, r domain.Repos) error {
				if err := r.Tokens.Create(ctx, &fresh); err != nil { // retires "old"
					return err
				}
				if err := r.Users.Delete(ctx, ana.ID); err != nil { // cascades
					return err
				}
				return errRollback
			})
			isErr(t, "WithTx", err, errRollback)
			e.getUser(ana.ID)
			_, err = e.r.Tokens.Consume(e.ctx, resetPassword, old.Hash, at(time.Minute))
			noErr(t, "Consume of the old token, which the rolled-back Tokens.Create must not retire", err)
			_, err = e.r.Tokens.Consume(e.ctx, resetPassword, fresh.Hash, at(time.Minute))
			isErr(t, "Consume of the token the rolled-back Tokens.Create made", err, domain.ErrNotFound)
		}},
		{"an ErrNotFound leaves the transaction usable", func(t *testing.T, e env) {
			err := e.s.WithTx(e.ctx, func(ctx context.Context, r domain.Repos) error {
				for name, call := range map[string]func() error{
					"Users.ByID":              func() error { _, err := r.Users.ByID(ctx, unknownRefs[1]); return err },
					"Users.ByEmail":           func() error { _, err := r.Users.ByEmail(ctx, "nobody@example.com"); return err },
					"Sessions.Get":            func() error { _, err := r.Sessions.Get(ctx, hashOf("nobody")); return err },
					"Collectives.ByNameKey":   func() error { _, err := r.Collectives.ByNameKey(ctx, "nobody"); return err },
					"Tokens.Consume":          func() error { _, err := r.Tokens.Consume(ctx, resetPassword, hashOf("nobody"), base); return err },
					"Users.Delete":            func() error { return r.Users.Delete(ctx, unknownRefs[1]) },
					"Outbox.MarkSent":         func() error { return r.Outbox.MarkSent(ctx, unknownRefs[1], base) },
					"Collectives.ByID (path)": func() error { _, err := r.Collectives.ByID(ctx, unknownRefs[2]); return err },
				} {
					if err := call(); !errors.Is(err, domain.ErrNotFound) {
						return fmt.Errorf("%s: error = %v, want ErrNotFound", name, err)
					}
				}
				u := newUser("ana")
				return r.Users.Create(ctx, &u)
			})
			noErr(t, "WithTx", err)
			if _, err := e.r.Users.ByEmail(e.ctx, "ana@example.com"); err != nil {
				t.Errorf("the user created after the reads: %v", err)
			}
		}},
		{"WithTx rolls back and returns the error of a write that failed", func(t *testing.T, e env) {
			ana := e.user("ana")
			c := e.collective("Casa Azul", coletivo, cApproved)
			e.member(ana, c, roleOwner, mApproved)
			for i, tc := range []struct {
				name   string
				target error
				write  func(ctx context.Context, r domain.Repos) error
			}{
				{"email taken", domain.ErrEmailTaken, func(ctx context.Context, r domain.Repos) error {
					u := newUser("outro", func(u *domain.User) { u.Email = ana.Email })
					return r.Users.Create(ctx, &u)
				}},
				{"name taken", domain.ErrNameTaken, func(ctx context.Context, r domain.Repos) error {
					other := newCollective("Casa Azul", produtora, cPending)
					return r.Collectives.Create(ctx, &other)
				}},
				{"already member", domain.ErrAlreadyMember, func(ctx context.Context, r domain.Repos) error {
					return r.Memberships.Create(ctx, newMembership(ana, c, roleMember, mPending))
				}},
			} {
				name := fmt.Sprintf("tmp%d", i)
				err := e.s.WithTx(e.ctx, func(ctx context.Context, r domain.Repos) error {
					u := newUser(name)
					if err := r.Users.Create(ctx, &u); err != nil {
						return err
					}
					return tc.write(ctx, r)
				})
				isErr(t, tc.name, err, tc.target)
				_, err = e.r.Users.ByEmail(e.ctx, name+"@example.com")
				isErr(t, tc.name+": the user written before the failure", err, domain.ErrNotFound)
			}
			e.getUser(ana.ID)
			if got := e.views(ana.ID); len(got) != 1 {
				t.Errorf("ana has %d memberships, want 1", len(got))
			}
		}},
		{"WithTx returns an error that fn wrapped, or a FieldErrors, so errors.Is and errors.As match", func(t *testing.T, e env) {
			err := e.s.WithTx(e.ctx, func(ctx context.Context, r domain.Repos) error {
				u := newUser("ana")
				if err := r.Users.Create(ctx, &u); err != nil {
					return err
				}
				return fmt.Errorf("sign-up failed: %w", errRollback)
			})
			isErr(t, "WithTx with a wrapped error", err, errRollback)

			err = e.s.WithTx(e.ctx, func(ctx context.Context, r domain.Repos) error {
				u := newUser("ana")
				if err := r.Users.Create(ctx, &u); err != nil {
					return err
				}
				bad := newUser("bia", func(u *domain.User) { u.Phone = "1" })
				return r.Users.Create(ctx, &bad)
			})
			wantFieldErrors(t, "WithTx with a validation error", err, "phone")
			_, err = e.r.Users.ByEmail(e.ctx, "ana@example.com")
			isErr(t, "the user written before the validation error", err, domain.ErrNotFound)
		}},
		{"concurrent transactions that write the same unique key: one commits", func(t *testing.T, e env) {
			errs := parallelErr(4, func(i int) error {
				return e.s.WithTx(e.ctx, func(ctx context.Context, r domain.Repos) error {
					u := newUser(fmt.Sprintf("racer%d", i), func(u *domain.User) { u.Email = "racer@example.com" })
					return r.Users.Create(ctx, &u)
				})
			})
			wantOneWinner(t, "WithTx", errs, domain.ErrEmailTaken)
			if _, err := e.r.Users.ByEmail(e.ctx, "racer@example.com"); err != nil {
				t.Errorf("ByEmail: %v", err)
			}
		}},
	})
}
