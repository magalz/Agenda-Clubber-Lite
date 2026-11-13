package storetest

import (
	"fmt"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// RunTokens runs the contract of domain.Tokens.
func RunTokens(t *testing.T, newStore NewStore) {
	t.Helper()
	runSubtests(t, newStore, []subtest{
		{"create assigns an ID and Consume returns every stored field", func(t *testing.T, e env) {
			u := e.user("ana")
			tok := newToken(u.ID, changeEmail, "t1", func(tok *domain.Token) { tok.ID = "chosen-by-the-caller" })
			noErr(t, "create", e.r.Tokens.Create(e.ctx, &tok))
			if tok.ID == "" || tok.ID == "chosen-by-the-caller" {
				t.Fatalf("ID = %q, want one assigned by the store", tok.ID)
			}
			got, err := e.r.Tokens.Consume(e.ctx, changeEmail, tok.Hash, at(time.Minute))
			noErr(t, "Consume", err)
			want := tok
			want.UsedAt = ptr(at(time.Minute))
			wantEqual(t, "consumed token", normToken(got), normToken(want))
		}},
		{"Consume returns the times in UTC, whatever the zone they were written in", func(t *testing.T, e env) {
			u := e.user("ana")
			tok := e.token(u.ID, resetPassword, "t1", func(tok *domain.Token) {
				tok.CreatedAt, tok.ExpiresAt = inLocalZone(base), inLocalZone(at(time.Hour))
			})
			got, err := e.r.Tokens.Consume(e.ctx, resetPassword, tok.Hash, inLocalZone(at(time.Minute)))
			noErr(t, "Consume", err)
			wantUTC(t, "CreatedAt", &got.CreatedAt, base)
			wantUTC(t, "ExpiresAt", &got.ExpiresAt, at(time.Hour))
			wantUTC(t, "UsedAt", got.UsedAt, at(time.Minute))
		}},
		{"create validates before it looks for the user", func(t *testing.T, e env) {
			tok := newToken(unknownRefs[0], "bogus", "t1", func(tok *domain.Token) {
				tok.Hash = []byte("short")
				tok.ExpiresAt = tok.CreatedAt
			})
			wantFieldErrors(t, "create", e.r.Tokens.Create(e.ctx, &tok), "purpose", "hash", "expires_at")
		}},
		{"create without a user is a validation error", func(t *testing.T, e env) {
			tok := newToken("", resetPassword, "t1")
			wantFieldErrors(t, "create", e.r.Tokens.Create(e.ctx, &tok), "user_id")
		}},
		{"create refuses a user that does not exist", func(t *testing.T, e env) {
			for _, id := range unknownRefs {
				tok := newToken(id, resetPassword, "t1")
				isErr(t, fmt.Sprintf("create for user %q", id), e.r.Tokens.Create(e.ctx, &tok), domain.ErrNotFound)
			}
			_, err := e.r.Tokens.Consume(e.ctx, resetPassword, hashOf("t1"), at(time.Minute))
			isErr(t, "Consume after the failed creates", err, domain.ErrNotFound)
		}},
		{"create retires the unused tokens of the same user and purpose", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			old := e.token(ana.ID, resetPassword, "old")
			otherPurpose := e.token(ana.ID, verifyEmail, "verify")
			otherUser := e.token(bia.ID, resetPassword, "bia")
			newer := e.token(ana.ID, resetPassword, "new", func(tok *domain.Token) {
				tok.CreatedAt = at(time.Minute)
				tok.ExpiresAt = at(2 * time.Hour)
			})

			now := at(2 * time.Minute)
			_, err := e.r.Tokens.Consume(e.ctx, old.Purpose, old.Hash, now)
			isErr(t, "Consume of the older token", err, domain.ErrNotFound)
			for _, tok := range []domain.Token{newer, otherPurpose, otherUser} {
				if _, err := e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, now); err != nil {
					t.Errorf("Consume of the %s token of %s: %v", tok.Purpose, tok.UserID, err)
				}
			}
		}},
		{"a failed create leaves the older tokens usable", func(t *testing.T, e env) {
			u := e.user("ana")
			old := e.token(u.ID, resetPassword, "old")
			bad := newToken(u.ID, resetPassword, "bad", func(tok *domain.Token) { tok.Hash = nil })
			wantFieldErrors(t, "create", e.r.Tokens.Create(e.ctx, &bad), "hash")
			if _, err := e.r.Tokens.Consume(e.ctx, old.Purpose, old.Hash, at(time.Minute)); err != nil {
				t.Errorf("Consume of the older token after a failed create: %v", err)
			}
		}},
		{"create with a hash that is already stored is ErrConflict and changes nothing", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			first := e.token(ana.ID, resetPassword, "dup")
			// Same user and purpose: retiring the older token must not survive the failed insert.
			again := newToken(ana.ID, resetPassword, "dup", func(tok *domain.Token) {
				tok.CreatedAt = at(time.Minute)
				tok.ExpiresAt = at(2 * time.Hour)
			})
			isErr(t, "create with the same hash, user and purpose", e.r.Tokens.Create(e.ctx, &again), domain.ErrConflict)
			// Another user and purpose: the hash is unique across all tokens.
			other := newToken(bia.ID, verifyEmail, "dup")
			isErr(t, "create with the same hash for another user and purpose", e.r.Tokens.Create(e.ctx, &other), domain.ErrConflict)
			_, err := e.r.Tokens.Consume(e.ctx, verifyEmail, other.Hash, at(2*time.Minute))
			isErr(t, "Consume of the token that was refused", err, domain.ErrNotFound)
			// The user is checked before the hash.
			ghost := newToken(unknownRefs[1], resetPassword, "dup")
			isErr(t, "create for an unknown user with a stored hash", e.r.Tokens.Create(e.ctx, &ghost), domain.ErrNotFound)

			got, err := e.r.Tokens.Consume(e.ctx, first.Purpose, first.Hash, at(2*time.Minute))
			noErr(t, "Consume of the first token", err)
			want := first
			want.UsedAt = ptr(at(2 * time.Minute))
			wantEqual(t, "consumed token", normToken(got), normToken(want))
		}},
		{"Consume marks the token used, and a second Consume finds nothing", func(t *testing.T, e env) {
			u := e.user("ana")
			tok := e.token(u.ID, resetPassword, "t1")
			got, err := e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, at(time.Minute))
			noErr(t, "Consume", err)
			sameTimePtr(t, "UsedAt", got.UsedAt, at(time.Minute))
			_, err = e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, at(2*time.Minute))
			isErr(t, "second Consume", err, domain.ErrNotFound)
		}},
		{"Consume finds nothing for another purpose or another hash, and consumes nothing", func(t *testing.T, e env) {
			u := e.user("ana")
			tok := e.token(u.ID, resetPassword, "t1")
			now := at(time.Minute)
			for name, call := range map[string]func() error{
				"other purpose": func() error { _, err := e.r.Tokens.Consume(e.ctx, verifyEmail, tok.Hash, now); return err },
				"unknown hash":  func() error { _, err := e.r.Tokens.Consume(e.ctx, resetPassword, hashOf("other"), now); return err },
				"nil hash":      func() error { _, err := e.r.Tokens.Consume(e.ctx, resetPassword, nil, now); return err },
				"short hash":    func() error { _, err := e.r.Tokens.Consume(e.ctx, resetPassword, tok.Hash[:5], now); return err },
				"bogus purpose": func() error { _, err := e.r.Tokens.Consume(e.ctx, "bogus", tok.Hash, now); return err },
			} {
				isErr(t, name, call(), domain.ErrNotFound)
			}
			if _, err := e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, now); err != nil {
				t.Errorf("Consume after the failed attempts: %v", err)
			}
		}},
		{"Consume needs ExpiresAt strictly after now", func(t *testing.T, e env) {
			u := e.user("ana")
			tok := e.token(u.ID, resetPassword, "t1") // ExpiresAt is base+1h
			_, err := e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, tok.ExpiresAt)
			isErr(t, "Consume at ExpiresAt", err, domain.ErrNotFound)
			_, err = e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, tok.ExpiresAt.Add(time.Hour))
			isErr(t, "Consume after ExpiresAt", err, domain.ErrNotFound)
			// The failed calls consumed nothing.
			if _, err := e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, tok.ExpiresAt.Add(-time.Millisecond)); err != nil {
				t.Errorf("Consume 1ms before ExpiresAt: %v", err)
			}
		}},
		{"Consume succeeds for exactly one of concurrent calls", func(t *testing.T, e env) {
			u := e.user("ana")
			tok := e.token(u.ID, resetPassword, "t1")
			errs := parallelErr(20, func(int) error {
				_, err := e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, at(time.Minute))
				return err
			})
			wantOneWinner(t, "Consume", errs, domain.ErrNotFound)
		}},
		{"DeleteExpired removes the tokens with ExpiresAt up to now and counts them", func(t *testing.T, e env) {
			expiring := func(d time.Duration) func(*domain.Token) {
				return func(tok *domain.Token) {
					tok.CreatedAt = at(-2 * time.Hour)
					tok.ExpiresAt = at(d)
				}
			}
			// One user per token: creating a token retires the user's earlier unused ones.
			past := e.token(e.user("past").ID, resetPassword, "past", expiring(-time.Hour))
			exact := e.token(e.user("exact").ID, resetPassword, "exact", expiring(0))
			justAfter := e.token(e.user("just-after").ID, resetPassword, "just-after", expiring(time.Millisecond))
			later := e.token(e.user("later").ID, resetPassword, "later", expiring(time.Hour))

			n, err := e.r.Tokens.DeleteExpired(e.ctx, base)
			noErr(t, "DeleteExpired", err)
			if n != 2 {
				t.Errorf("DeleteExpired = %d, want 2", n)
			}
			n, err = e.r.Tokens.DeleteExpired(e.ctx, base)
			noErr(t, "second DeleteExpired", err)
			if n != 0 {
				t.Errorf("second DeleteExpired = %d, want 0", n)
			}
			// Both would still be consumable at this time had they not been deleted.
			for _, tok := range []domain.Token{past, exact} {
				_, err := e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, at(-3*time.Hour))
				isErr(t, "Consume of a token that expired, at a time before its expiry", err, domain.ErrNotFound)
			}
			for _, tok := range []domain.Token{justAfter, later} {
				if _, err := e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, base); err != nil {
					t.Errorf("Consume of a live token: %v", err)
				}
			}
		}},
		{"DeleteExpired removes the tokens that are used, expired or not", func(t *testing.T, e env) {
			consumed := e.token(e.user("consumed").ID, resetPassword, "consumed")
			_, err := e.r.Tokens.Consume(e.ctx, consumed.Purpose, consumed.Hash, base)
			noErr(t, "Consume", err)
			ana := e.user("ana")
			retired := e.token(ana.ID, verifyEmail, "retired")
			newer := e.token(ana.ID, verifyEmail, "newer", func(tok *domain.Token) {
				tok.CreatedAt = at(time.Minute) // retires the token above
				tok.ExpiresAt = at(2 * time.Hour)
			})
			unused := e.token(e.user("unused").ID, changeEmail, "unused")

			n, err := e.r.Tokens.DeleteExpired(e.ctx, base)
			noErr(t, "DeleteExpired", err)
			if n != 2 {
				t.Errorf("DeleteExpired = %d, want 2 (the consumed and the retired token)", n)
			}
			// Both are used and unexpired, so Consume cannot tell a deleted token from
			// a kept one: the count above is the evidence. The unused ones survive.
			_, err = e.r.Tokens.Consume(e.ctx, retired.Purpose, retired.Hash, base)
			isErr(t, "Consume of the retired token", err, domain.ErrNotFound)
			for _, tok := range []domain.Token{newer, unused} {
				if _, err := e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, at(2*time.Minute)); err != nil {
					t.Errorf("Consume of the %s token that was unused: %v", tok.Purpose, err)
				}
			}
		}},
		{"DeleteExpired keeps the unused tokens that have not expired and returns 0", func(t *testing.T, e env) {
			n, err := e.r.Tokens.DeleteExpired(e.ctx, base)
			noErr(t, "DeleteExpired on an empty store", err)
			if n != 0 {
				t.Errorf("DeleteExpired on an empty store = %d, want 0", n)
			}
			live := []domain.Token{
				e.token(e.user("ana").ID, resetPassword, "t1"),
				e.token(e.user("bia").ID, verifyEmail, "t2"),
				e.token(e.user("cai").ID, changeEmail, "t3"),
			}
			n, err = e.r.Tokens.DeleteExpired(e.ctx, at(time.Hour-time.Millisecond))
			noErr(t, "DeleteExpired", err)
			if n != 0 {
				t.Errorf("DeleteExpired 1ms before ExpiresAt = %d, want 0", n)
			}
			for _, tok := range live {
				if _, err := e.r.Tokens.Consume(e.ctx, tok.Purpose, tok.Hash, base); err != nil {
					t.Errorf("Consume of the %s token after DeleteExpired: %v", tok.Purpose, err)
				}
			}
		}},
	})
}
