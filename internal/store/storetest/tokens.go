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
	})
}
