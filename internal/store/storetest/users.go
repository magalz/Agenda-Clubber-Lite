package storetest

import (
	"fmt"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// RunUsers runs the contract of domain.Users.
func RunUsers(t *testing.T, newStore NewStore) {
	t.Helper()
	runSubtests(t, newStore, []subtest{
		{"create assigns an ID and stores every field", func(t *testing.T, e env) {
			approver := e.user("root")
			u := newUser("ana", func(u *domain.User) {
				u.ID = "chosen-by-the-caller"
				u.PhoneIsWhatsApp = true
				u.IsAdmin = true
				u.Status = uPendingApproval
				u.FailedLogins = 2
				u.LockedUntil = ptr(at(time.Hour))
				u.EmailVerifiedAt = ptr(at(time.Minute))
				u.ApprovedAt = ptr(at(2 * time.Minute))
				u.ApprovedBy = approver.ID
			})
			noErr(t, "create", e.r.Users.Create(e.ctx, &u))
			if u.ID == "" || u.ID == "chosen-by-the-caller" {
				t.Fatalf("ID = %q, want one assigned by the store", u.ID)
			}
			wantEqual(t, "stored user", normUser(e.getUser(u.ID)), normUser(u))
			_, err := e.r.Users.ByID(e.ctx, "chosen-by-the-caller")
			isErr(t, "ByID of the ID the caller chose", err, domain.ErrNotFound)
		}},
		{"times come back in UTC, whatever the zone they were written in", func(t *testing.T, e env) {
			local := inLocalZone(base)
			u := newUser("ana", func(u *domain.User) {
				u.PasswordChangedAt, u.CreatedAt, u.UpdatedAt = local, local, local
				u.LockedUntil, u.EmailVerifiedAt, u.ApprovedAt = &local, &local, &local
			})
			noErr(t, "create", e.r.Users.Create(e.ctx, &u))
			got := e.getUser(u.ID)
			wantUTC(t, "PasswordChangedAt", &got.PasswordChangedAt, base)
			wantUTC(t, "CreatedAt", &got.CreatedAt, base)
			wantUTC(t, "UpdatedAt", &got.UpdatedAt, base)
			wantUTC(t, "LockedUntil", got.LockedUntil, base)
			wantUTC(t, "EmailVerifiedAt", got.EmailVerifiedAt, base)
			wantUTC(t, "ApprovedAt", got.ApprovedAt, base)
		}},
		{"create gives each user its own ID", func(t *testing.T, e env) {
			a, b := e.user("ana"), e.user("bia")
			if a.ID == b.ID {
				t.Errorf("two users share the ID %q", a.ID)
			}
		}},
		{"create returns the validation errors and stores nothing", func(t *testing.T, e env) {
			u := newUser("ana", func(u *domain.User) {
				u.Name = ""
				u.Phone = "12"
				u.Email = "Ana@Example.com"
			})
			wantFieldErrors(t, "create", e.r.Users.Create(e.ctx, &u), "name", "phone", "email")
			_, err := e.r.Users.ByEmail(e.ctx, "ana@example.com")
			isErr(t, "ByEmail after the failed create", err, domain.ErrNotFound)
		}},
		{"create validates before it checks references and uniqueness", func(t *testing.T, e env) {
			e.user("ana")
			u := newUser("ana", func(u *domain.User) {
				u.Name = ""
				u.ApprovedBy = unknownRefs[1]
			})
			wantFieldErrors(t, "create taken email, unknown approver, bad name", e.r.Users.Create(e.ctx, &u), "name")
		}},
		{"create refuses a taken email", func(t *testing.T, e env) {
			first := e.user("ana")
			dup := newUser("ana", func(u *domain.User) {
				u.Name = "Outra Ana"
				u.Phone = "21911112222"
			})
			isErr(t, "create", e.r.Users.Create(e.ctx, &dup), domain.ErrEmailTaken)
			got, err := e.r.Users.ByEmail(e.ctx, "ana@example.com")
			noErr(t, "ByEmail", err)
			if got.ID != first.ID || got.Name != first.Name {
				t.Errorf("the failed create changed the stored user: %+v", got)
			}
		}},
		{"create refuses an approver that does not exist", func(t *testing.T, e env) {
			for _, id := range unknownRefs {
				u := newUser("ana", func(u *domain.User) { u.ApprovedBy = id })
				isErr(t, fmt.Sprintf("create with ApprovedBy %q", id), e.r.Users.Create(e.ctx, &u), domain.ErrNotFound)
			}
			_, err := e.r.Users.ByEmail(e.ctx, "ana@example.com")
			isErr(t, "ByEmail after the failed creates", err, domain.ErrNotFound)
		}},
		{"create checks references before uniqueness", func(t *testing.T, e env) {
			e.user("ana")
			u := newUser("ana", func(u *domain.User) { u.ApprovedBy = unknownRefs[1] })
			isErr(t, "create taken email, unknown approver", e.r.Users.Create(e.ctx, &u), domain.ErrNotFound)
		}},
		{"concurrent creates with one email store one user", func(t *testing.T, e env) {
			errs := parallelErr(10, func(i int) error {
				u := newUser("ana", func(u *domain.User) { u.Name = fmt.Sprintf("Ana %d", i) })
				return e.r.Users.Create(e.ctx, &u)
			})
			wantOneWinner(t, "create", errs, domain.ErrEmailTaken)
		}},
		{"ByID returns ErrNotFound for an ID the store never issued", func(t *testing.T, e env) {
			e.user("ana")
			for _, id := range unknownIDs {
				_, err := e.r.Users.ByID(e.ctx, id)
				isErr(t, fmt.Sprintf("ByID(%q)", id), err, domain.ErrNotFound)
			}
		}},
		{"ByEmail normalizes the email it looks for", func(t *testing.T, e env) {
			u := e.user("ana")
			got, err := e.r.Users.ByEmail(e.ctx, "  Ana@Example.COM ")
			noErr(t, "ByEmail with case and space", err)
			if got.ID != u.ID {
				t.Errorf("ByEmail found %q, want %q", got.ID, u.ID)
			}
		}},
		{"ByEmail returns ErrNotFound for an email nobody uses or that is not an email", func(t *testing.T, e env) {
			e.user("ana")
			// "a.a@example.com" would match "ana@example.com" if it were a pattern.
			for _, email := range []string{"nobody@example.com", "a.a@example.com", "not an email", "", "ana@", ".*"} {
				_, err := e.r.Users.ByEmail(e.ctx, email)
				isErr(t, fmt.Sprintf("ByEmail(%q)", email), err, domain.ErrNotFound)
			}
		}},
		{"SetStatus sets the status and UpdatedAt", func(t *testing.T, e env) {
			u := e.user("ana", func(u *domain.User) { u.Status = uPendingApproval })
			noErr(t, "SetStatus", e.r.Users.SetStatus(e.ctx, u.ID, uActive, at(time.Hour)))
			want := u
			want.Status = uActive
			want.UpdatedAt = at(time.Hour)
			wantEqual(t, "user", normUser(e.getUser(u.ID)), normUser(want))
		}},
		{"SetStatus refuses a status that is not defined", func(t *testing.T, e env) {
			u := e.user("ana")
			wantFieldErrors(t, "SetStatus", e.r.Users.SetStatus(e.ctx, u.ID, "bogus", at(time.Hour)), "status")
			wantEqual(t, "user", normUser(e.getUser(u.ID)), normUser(u))
		}},
		{"SetStatus returns ErrNotFound for a missing user", func(t *testing.T, e env) {
			for _, id := range unknownIDs {
				isErr(t, fmt.Sprintf("SetStatus(%q)", id), e.r.Users.SetStatus(e.ctx, id, uActive, at(time.Hour)), domain.ErrNotFound)
			}
		}},
		{"MarkEmailVerified moves pending_email to pending_approval", func(t *testing.T, e env) {
			u := e.user("ana", func(u *domain.User) { u.Status = uPendingEmail })
			noErr(t, "MarkEmailVerified", e.r.Users.MarkEmailVerified(e.ctx, u.ID, at(time.Hour)))
			want := u
			want.Status = uPendingApproval
			want.EmailVerifiedAt = ptr(at(time.Hour))
			want.UpdatedAt = at(time.Hour)
			wantEqual(t, "user", normUser(e.getUser(u.ID)), normUser(want))
		}},
		{"MarkEmailVerified is ErrConflict in any other status and changes nothing", func(t *testing.T, e env) {
			for i, status := range []domain.UserStatus{uPendingApproval, uActive, uDisabled} {
				u := e.user(fmt.Sprintf("user%d", i), func(u *domain.User) { u.Status = status })
				isErr(t, "MarkEmailVerified in status "+string(status), e.r.Users.MarkEmailVerified(e.ctx, u.ID, at(time.Hour)), domain.ErrConflict)
				wantEqual(t, "user in status "+string(status), normUser(e.getUser(u.ID)), normUser(u))
			}
		}},
		{"MarkEmailVerified returns ErrNotFound for a missing user", func(t *testing.T, e env) {
			for _, id := range unknownIDs {
				isErr(t, fmt.Sprintf("MarkEmailVerified(%q)", id), e.r.Users.MarkEmailVerified(e.ctx, id, at(time.Hour)), domain.ErrNotFound)
			}
		}},
		{"ChangePassword sets the hash and the dates and ends the lock", func(t *testing.T, e env) {
			u := e.user("ana", func(u *domain.User) {
				u.FailedLogins = 3
				u.LockedUntil = ptr(at(time.Hour))
			})
			noErr(t, "ChangePassword", e.r.Users.ChangePassword(e.ctx, u.ID, "hash-new", at(time.Minute)))
			want := u
			want.PasswordHash = "hash-new"
			want.PasswordChangedAt = at(time.Minute)
			want.UpdatedAt = at(time.Minute)
			want.FailedLogins = 0
			want.LockedUntil = nil
			wantEqual(t, "user", normUser(e.getUser(u.ID)), normUser(want))
		}},
		{"RecentPasswordHashes lists the current hash first and one previous hash", func(t *testing.T, e env) {
			u := e.user("ana")
			got, err := e.r.Users.RecentPasswordHashes(e.ctx, u.ID)
			noErr(t, "RecentPasswordHashes of a new user", err)
			wantStrings(t, "hashes of a new user", got, "hash-ana")

			noErr(t, "first ChangePassword", e.r.Users.ChangePassword(e.ctx, u.ID, "hash-1", at(time.Minute)))
			got, err = e.r.Users.RecentPasswordHashes(e.ctx, u.ID)
			noErr(t, "RecentPasswordHashes", err)
			wantStrings(t, "hashes after one change", got, "hash-1", "hash-ana")

			noErr(t, "second ChangePassword", e.r.Users.ChangePassword(e.ctx, u.ID, "hash-2", at(2*time.Minute)))
			got, err = e.r.Users.RecentPasswordHashes(e.ctx, u.ID)
			noErr(t, "RecentPasswordHashes", err)
			wantStrings(t, "hashes after two changes", got, "hash-2", "hash-1")
		}},
		{"RecentPasswordHashes returns ErrNotFound for a missing user", func(t *testing.T, e env) {
			for _, id := range unknownIDs {
				_, err := e.r.Users.RecentPasswordHashes(e.ctx, id)
				isErr(t, fmt.Sprintf("RecentPasswordHashes(%q)", id), err, domain.ErrNotFound)
			}
		}},
		// A string that starts with $ is a field path inside a MongoDB aggregation
		// update unless it is quoted, and an operator if it becomes a filter key:
		// every backend must keep it as plain text.
		{"values that start with $ are stored and looked up as plain text", func(t *testing.T, e env) {
			u := e.user("ana", func(u *domain.User) {
				u.Name = "$gt"
				u.PasswordHash = "$password_hash"
			})
			const argon = "$argon2id$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA"
			for i, hash := range []string{"$$ROOT", "$$REMOVE", "$name", argon} {
				noErr(t, fmt.Sprintf("ChangePassword(%q)", hash), e.r.Users.ChangePassword(e.ctx, u.ID, hash, at(time.Duration(i+1)*time.Minute)))
				got, err := e.r.Users.RecentPasswordHashes(e.ctx, u.ID)
				noErr(t, "RecentPasswordHashes", err)
				if len(got) != 2 || got[0] != hash {
					t.Errorf("RecentPasswordHashes after ChangePassword(%q) = %q", hash, got)
				}
			}
			stored := e.getUser(u.ID)
			if stored.Name != "$gt" {
				t.Errorf("Name = %q, want $gt", stored.Name)
			}
			if stored.PasswordHash != argon {
				t.Errorf("PasswordHash = %q, want %q", stored.PasswordHash, argon)
			}

			// The same text as a lookup key finds nothing: it is a string, not an operator.
			_, err := e.r.Users.ByEmail(e.ctx, "$ne")
			isErr(t, "ByEmail($ne)", err, domain.ErrNotFound)
			_, err = e.r.Collectives.ByNameKey(e.ctx, "$ne")
			isErr(t, "Collectives.ByNameKey($ne)", err, domain.ErrNotFound)
		}},
		{"ChangePassword refuses an empty hash and changes nothing", func(t *testing.T, e env) {
			u := e.user("ana")
			wantFieldErrors(t, "ChangePassword", e.r.Users.ChangePassword(e.ctx, u.ID, "", at(time.Minute)), "password_hash")
			wantEqual(t, "user", normUser(e.getUser(u.ID)), normUser(u))
		}},
		{"ChangePassword returns ErrNotFound for a missing user", func(t *testing.T, e env) {
			for _, id := range unknownIDs {
				isErr(t, fmt.Sprintf("ChangePassword(%q)", id), e.r.Users.ChangePassword(e.ctx, id, "hash-new", at(time.Minute)), domain.ErrNotFound)
			}
		}},
		{"RecordLoginFailure counts failures and locks at the limit", func(t *testing.T, e env) {
			u := e.user("ana")
			now, lockFor := at(time.Hour), 15*time.Minute
			for i, wantLocked := range []bool{false, false, true} {
				locked, err := e.r.Users.RecordLoginFailure(e.ctx, u.ID, now, 3, lockFor)
				noErr(t, fmt.Sprintf("failure %d", i+1), err)
				if locked != wantLocked {
					t.Errorf("failure %d: locked = %v, want %v", i+1, locked, wantLocked)
				}
				got := e.getUser(u.ID)
				if wantLocked {
					if got.FailedLogins != 0 {
						t.Errorf("failure %d: FailedLogins = %d after the lock, want 0", i+1, got.FailedLogins)
					}
					sameTimePtr(t, "LockedUntil", got.LockedUntil, now.Add(lockFor))
				} else {
					if got.FailedLogins != i+1 {
						t.Errorf("failure %d: FailedLogins = %d, want %d", i+1, got.FailedLogins, i+1)
					}
					unsetTime(t, "LockedUntil", got.LockedUntil)
				}
			}
		}},
		{"RecordLoginFailure with a limit of 1 locks at the first failure", func(t *testing.T, e env) {
			u := e.user("ana")
			locked, err := e.r.Users.RecordLoginFailure(e.ctx, u.ID, at(time.Hour), 1, time.Minute)
			noErr(t, "RecordLoginFailure", err)
			if !locked {
				t.Error("locked = false, want true")
			}
			got := e.getUser(u.ID)
			sameTimePtr(t, "LockedUntil", got.LockedUntil, at(time.Hour+time.Minute))
			if got.FailedLogins != 0 {
				t.Errorf("FailedLogins = %d, want 0", got.FailedLogins)
			}
		}},
		{"RecordLoginFailure never loses a count under concurrency", func(t *testing.T, e env) {
			u := e.user("ana")
			res := parallel(20, func(int) (bool, error) {
				return e.r.Users.RecordLoginFailure(e.ctx, u.ID, at(time.Hour), 100, time.Minute)
			})
			for _, r := range res {
				if r.err != nil || r.val {
					t.Errorf("RecordLoginFailure = (%v, %v), want (false, nil)", r.val, r.err)
				}
			}
			if got := e.getUser(u.ID).FailedLogins; got != 20 {
				t.Errorf("FailedLogins = %d after 20 concurrent failures, want 20", got)
			}
		}},
		{"RecordLoginFailure locks exactly once per limit under concurrency", func(t *testing.T, e env) {
			u := e.user("ana")
			res := parallel(20, func(int) (bool, error) {
				return e.r.Users.RecordLoginFailure(e.ctx, u.ID, at(time.Hour), 5, time.Minute)
			})
			locks := 0
			for _, r := range res {
				noErr(t, "RecordLoginFailure", r.err)
				if r.val {
					locks++
				}
			}
			if locks != 4 {
				t.Errorf("%d calls reported a lock, want 4 (20 failures, 5 per lock)", locks)
			}
			if got := e.getUser(u.ID).FailedLogins; got != 0 {
				t.Errorf("FailedLogins = %d, want 0 after the last lock", got)
			}
		}},
		{"RecordLoginFailure returns ErrNotFound for a missing user", func(t *testing.T, e env) {
			for _, id := range unknownIDs {
				_, err := e.r.Users.RecordLoginFailure(e.ctx, id, at(time.Hour), 3, time.Minute)
				isErr(t, fmt.Sprintf("RecordLoginFailure(%q)", id), err, domain.ErrNotFound)
			}
		}},
		{"ResetLoginFailures zeroes the count and leaves the lock", func(t *testing.T, e env) {
			u := e.user("ana", func(u *domain.User) {
				u.FailedLogins = 2
				u.LockedUntil = ptr(at(time.Hour))
			})
			noErr(t, "ResetLoginFailures", e.r.Users.ResetLoginFailures(e.ctx, u.ID))
			got := e.getUser(u.ID)
			if got.FailedLogins != 0 {
				t.Errorf("FailedLogins = %d, want 0", got.FailedLogins)
			}
			sameTimePtr(t, "LockedUntil", got.LockedUntil, at(time.Hour))
		}},
		{"ResetLoginFailures returns ErrNotFound for a missing user", func(t *testing.T, e env) {
			for _, id := range unknownIDs {
				isErr(t, fmt.Sprintf("ResetLoginFailures(%q)", id), e.r.Users.ResetLoginFailures(e.ctx, id), domain.ErrNotFound)
			}
		}},
		{"ListAdminEmails returns the active admins in ascending order", func(t *testing.T, e env) {
			admin := func(u *domain.User) { u.IsAdmin = true }
			e.user("zed", admin)
			e.user("ana", admin)
			e.user("bia", func(u *domain.User) { u.IsAdmin = true; u.Status = uDisabled })
			e.user("cris", func(u *domain.User) { u.IsAdmin = true; u.Status = uPendingApproval })
			e.user("dani", func(u *domain.User) { u.IsAdmin = true; u.Status = uPendingEmail })
			e.user("eva")
			got, err := e.r.Users.ListAdminEmails(e.ctx)
			noErr(t, "ListAdminEmails", err)
			wantStrings(t, "admin emails", got, "ana@example.com", "zed@example.com")
		}},
		{"ListAdminEmails is empty without admins", func(t *testing.T, e env) {
			e.user("ana")
			got, err := e.r.Users.ListAdminEmails(e.ctx)
			noErr(t, "ListAdminEmails", err)
			wantStrings(t, "admin emails", got)
		}},
		{"Delete removes the user with its sessions, tokens and memberships", func(t *testing.T, e env) {
			gone, other := e.user("gone"), e.user("other")
			c := e.collective("Casa Azul", coletivo, cApproved)
			e.member(gone, c, roleOwner, mApproved)
			e.member(other, c, roleMember, mApproved)
			s1, s2 := e.session(gone.ID, "gone-1"), e.session(gone.ID, "gone-2")
			tok := e.token(gone.ID, resetPassword, "gone-token")
			otherSession := e.session(other.ID, "other-1")
			otherToken := e.token(other.ID, resetPassword, "other-token")

			noErr(t, "Delete", e.r.Users.Delete(e.ctx, gone.ID))

			_, err := e.r.Users.ByID(e.ctx, gone.ID)
			isErr(t, "ByID of the deleted user", err, domain.ErrNotFound)
			_, err = e.r.Users.ByEmail(e.ctx, gone.Email)
			isErr(t, "ByEmail of the deleted user", err, domain.ErrNotFound)
			for _, s := range []domain.Session{s1, s2} {
				_, err = e.r.Sessions.Get(e.ctx, s.Hash)
				isErr(t, "Get of a session of the deleted user", err, domain.ErrNotFound)
			}
			_, err = e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, at(time.Minute))
			isErr(t, "Consume of a token of the deleted user", err, domain.ErrNotFound)
			views, err := e.r.Memberships.ListByUser(e.ctx, gone.ID)
			noErr(t, "ListByUser of the deleted user", err)
			if len(views) != 0 {
				t.Errorf("the deleted user still has %d memberships", len(views))
			}

			// Nothing of the others went with it.
			if got := e.getUser(other.ID); got.Email != other.Email {
				t.Errorf("the other user changed: %+v", got)
			}
			if _, err := e.r.Sessions.Get(e.ctx, otherSession.Hash); err != nil {
				t.Errorf("the other user's session: %v", err)
			}
			if _, err := e.r.Tokens.Consume(e.ctx, otherToken.Purpose, otherToken.Hash, at(time.Minute)); err != nil {
				t.Errorf("the other user's token: %v", err)
			}
			if views, err = e.r.Memberships.ListByUser(e.ctx, other.ID); err != nil || len(views) != 1 {
				t.Errorf("the other user's memberships = %v, %v, want 1", views, err)
			}
			if _, err := e.r.Collectives.ByID(e.ctx, c.ID); err != nil {
				t.Errorf("the collective went with its owner: %v", err)
			}
		}},
		{"Delete empties every reference to the user", func(t *testing.T, e env) {
			gone := e.user("gone")
			approved := e.user("ana", func(u *domain.User) {
				u.ApprovedBy = gone.ID
				u.ApprovedAt = ptr(at(time.Minute))
			})
			c := e.collective("Casa Azul", coletivo, cApproved, func(c *domain.Collective) {
				c.CreatedBy = gone.ID
				c.ApprovedBy = gone.ID
				c.ApprovedAt = ptr(at(time.Minute))
			})
			e.member(approved, c, roleMember, mApproved, func(m *domain.Membership) {
				m.AdminApprovedBy = gone.ID
				m.AdminApprovedAt = ptr(at(time.Minute))
				m.OwnerApprovedBy = gone.ID
				m.OwnerApprovedAt = ptr(at(time.Minute))
			})

			noErr(t, "Delete", e.r.Users.Delete(e.ctx, gone.ID))

			if got := e.getUser(approved.ID); got.ApprovedBy != "" || got.Email != approved.Email {
				t.Errorf("User.ApprovedBy = %q, want empty (user %+v)", got.ApprovedBy, got)
			}
			gotC, err := e.r.Collectives.ByID(e.ctx, c.ID)
			noErr(t, "Collectives.ByID", err)
			if gotC.CreatedBy != "" || gotC.ApprovedBy != "" || gotC.Name != c.Name {
				t.Errorf("Collective.CreatedBy, ApprovedBy = %q, %q, want both empty (collective %+v)", gotC.CreatedBy, gotC.ApprovedBy, gotC)
			}
			views, err := e.r.Memberships.ListByUser(e.ctx, approved.ID)
			noErr(t, "ListByUser", err)
			if len(views) != 1 {
				t.Fatalf("ListByUser returned %d memberships, want 1", len(views))
			}
			if v := views[0]; v.AdminApprovedBy != "" || v.OwnerApprovedBy != "" || v.Role != roleMember {
				t.Errorf("Membership.AdminApprovedBy, OwnerApprovedBy = %q, %q, want both empty (membership %+v)", v.AdminApprovedBy, v.OwnerApprovedBy, v.Membership)
			}
		}},
		{"Delete frees the email", func(t *testing.T, e env) {
			gone := e.user("ana")
			noErr(t, "Delete", e.r.Users.Delete(e.ctx, gone.ID))
			again := newUser("ana")
			noErr(t, "create with the freed email", e.r.Users.Create(e.ctx, &again))
		}},
		{"Delete returns ErrNotFound for a missing user", func(t *testing.T, e env) {
			for _, id := range unknownIDs {
				isErr(t, fmt.Sprintf("Delete(%q)", id), e.r.Users.Delete(e.ctx, id), domain.ErrNotFound)
			}
			u := e.user("ana")
			noErr(t, "Delete", e.r.Users.Delete(e.ctx, u.ID))
			isErr(t, "second Delete", e.r.Users.Delete(e.ctx, u.ID), domain.ErrNotFound)
		}},
	})
}
