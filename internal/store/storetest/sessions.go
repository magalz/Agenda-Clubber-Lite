package storetest

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// RunSessions runs the contract of domain.Sessions.
func RunSessions(t *testing.T, newStore NewStore) {
	t.Helper()
	runSubtests(t, newStore, []subtest{
		{"create stores the session and Get returns every field", func(t *testing.T, e env) {
			u := e.user("ana")
			s := e.session(u.ID, "s1", func(s *domain.Session) {
				s.LastSeenAt = at(time.Minute)
				s.UserAgent = "curl/8"
			})
			got, err := e.r.Sessions.Get(e.ctx, s.Hash)
			noErr(t, "Get", err)
			wantEqual(t, "session", normSession(got), normSession(s))
		}},
		{"create keeps a user agent of the maximum length in runes", func(t *testing.T, e env) {
			u := e.user("ana")
			s := e.session(u.ID, "s1", func(s *domain.Session) { s.UserAgent = strings.Repeat("é", domain.UserAgentMax) })
			got, err := e.r.Sessions.Get(e.ctx, s.Hash)
			noErr(t, "Get", err)
			if got.UserAgent != s.UserAgent {
				t.Errorf("UserAgent has %d bytes, want the %d it was stored with", len(got.UserAgent), len(s.UserAgent))
			}
		}},
		{"create validates before it looks for the user", func(t *testing.T, e env) {
			s := newSession(unknownRefs[0], "s1", func(s *domain.Session) {
				s.Hash = []byte("short")
				s.ExpiresAt = s.CreatedAt
				s.UserAgent = strings.Repeat("x", domain.UserAgentMax+1)
			})
			wantFieldErrors(t, "create", e.r.Sessions.Create(e.ctx, s), "hash", "expires_at", "user_agent")
		}},
		{"create without a user is a validation error", func(t *testing.T, e env) {
			wantFieldErrors(t, "create", e.r.Sessions.Create(e.ctx, newSession("", "s1")), "user_id")
		}},
		{"create refuses a user that does not exist", func(t *testing.T, e env) {
			for _, id := range unknownRefs {
				isErr(t, fmt.Sprintf("create for user %q", id), e.r.Sessions.Create(e.ctx, newSession(id, "s1")), domain.ErrNotFound)
			}
			_, err := e.r.Sessions.Get(e.ctx, hashOf("s1"))
			isErr(t, "Get after the failed creates", err, domain.ErrNotFound)
		}},
		{"create refuses a hash that is already in use", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			first := e.session(ana.ID, "s1")
			isErr(t, "create with the same hash", e.r.Sessions.Create(e.ctx, newSession(bia.ID, "s1")), domain.ErrConflict)
			got, err := e.r.Sessions.Get(e.ctx, first.Hash)
			noErr(t, "Get", err)
			if got.UserID != ana.ID {
				t.Errorf("the failed create changed the session: UserID = %q, want %q", got.UserID, ana.ID)
			}
		}},
		{"create looks for the user before it checks the hash", func(t *testing.T, e env) {
			ana := e.user("ana")
			e.session(ana.ID, "s1")
			isErr(t, "create with a used hash, unknown user", e.r.Sessions.Create(e.ctx, newSession(unknownRefs[1], "s1")), domain.ErrNotFound)
		}},
		{"Get returns ErrNotFound for an unknown hash", func(t *testing.T, e env) {
			u := e.user("ana")
			e.session(u.ID, "s1")
			for name, hash := range map[string][]byte{"unknown": hashOf("other"), "nil": nil, "empty": {}, "short": []byte("short")} {
				_, err := e.r.Sessions.Get(e.ctx, hash)
				isErr(t, "Get of a "+name+" hash", err, domain.ErrNotFound)
			}
		}},
		{"Get returns a session that has expired, and the store never expires it", func(t *testing.T, e env) {
			u := e.user("ana")
			long := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			s := e.session(u.ID, "s1", func(s *domain.Session) {
				s.CreatedAt, s.LastSeenAt, s.ExpiresAt = long, long, long.Add(time.Hour)
			})
			got, err := e.r.Sessions.Get(e.ctx, s.Hash)
			noErr(t, "Get of an expired session", err)
			sameTime(t, "ExpiresAt", got.ExpiresAt, s.ExpiresAt)
		}},
		{"Touch sets LastSeenAt and ExpiresAt", func(t *testing.T, e env) {
			u := e.user("ana")
			s := e.session(u.ID, "s1")
			noErr(t, "Touch", e.r.Sessions.Touch(e.ctx, s.Hash, at(5*time.Minute), at(48*time.Hour)))
			want := s
			want.LastSeenAt = at(5 * time.Minute)
			want.ExpiresAt = at(48 * time.Hour)
			got, err := e.r.Sessions.Get(e.ctx, s.Hash)
			noErr(t, "Get", err)
			wantEqual(t, "session", normSession(got), normSession(want))
		}},
		{"Touch returns ErrNotFound for an unknown hash", func(t *testing.T, e env) {
			isErr(t, "Touch", e.r.Sessions.Touch(e.ctx, hashOf("s1"), at(time.Minute), at(time.Hour)), domain.ErrNotFound)
		}},
		{"Delete removes the session and is idempotent", func(t *testing.T, e env) {
			u := e.user("ana")
			s := e.session(u.ID, "s1")
			keep := e.session(u.ID, "s2")
			noErr(t, "Delete", e.r.Sessions.Delete(e.ctx, s.Hash))
			_, err := e.r.Sessions.Get(e.ctx, s.Hash)
			isErr(t, "Get after Delete", err, domain.ErrNotFound)
			noErr(t, "second Delete", e.r.Sessions.Delete(e.ctx, s.Hash))
			noErr(t, "Delete of an unknown hash", e.r.Sessions.Delete(e.ctx, hashOf("unknown")))
			noErr(t, "Delete of a nil hash", e.r.Sessions.Delete(e.ctx, nil))
			if _, err := e.r.Sessions.Get(e.ctx, keep.Hash); err != nil {
				t.Errorf("the other session: %v", err)
			}
		}},
		{"DeleteForUser removes the user's sessions except the one to keep", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			s1, s2, s3 := e.session(ana.ID, "s1"), e.session(ana.ID, "s2"), e.session(ana.ID, "s3")
			other := e.session(bia.ID, "s4")
			noErr(t, "DeleteForUser", e.r.Sessions.DeleteForUser(e.ctx, ana.ID, s2.Hash))
			for _, s := range []domain.Session{s1, s3} {
				_, err := e.r.Sessions.Get(e.ctx, s.Hash)
				isErr(t, "Get of a removed session", err, domain.ErrNotFound)
			}
			for _, s := range []domain.Session{s2, other} {
				if _, err := e.r.Sessions.Get(e.ctx, s.Hash); err != nil {
					t.Errorf("Get of a session that stays: %v", err)
				}
			}
		}},
		{"DeleteForUser removes every session of the user when keep is nil or empty", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			other := e.session(bia.ID, "other")
			for _, keep := range [][]byte{nil, {}} {
				s1, s2 := e.session(ana.ID, fmt.Sprintf("a%d-1", len(keep))), e.session(ana.ID, fmt.Sprintf("a%d-2", len(keep)))
				noErr(t, "DeleteForUser", e.r.Sessions.DeleteForUser(e.ctx, ana.ID, keep))
				for _, s := range []domain.Session{s1, s2} {
					_, err := e.r.Sessions.Get(e.ctx, s.Hash)
					isErr(t, fmt.Sprintf("Get after DeleteForUser with keep %v", keep), err, domain.ErrNotFound)
				}
			}
			if _, err := e.r.Sessions.Get(e.ctx, other.Hash); err != nil {
				t.Errorf("the other user's session: %v", err)
			}
		}},
		{"DeleteForUser keeps nothing of the user when keep is another user's session", func(t *testing.T, e env) {
			ana, bia := e.user("ana"), e.user("bia")
			mine := e.session(ana.ID, "mine")
			other := e.session(bia.ID, "other")
			noErr(t, "DeleteForUser", e.r.Sessions.DeleteForUser(e.ctx, ana.ID, other.Hash))
			_, err := e.r.Sessions.Get(e.ctx, mine.Hash)
			isErr(t, "Get of the user's session", err, domain.ErrNotFound)
			if _, err := e.r.Sessions.Get(e.ctx, other.Hash); err != nil {
				t.Errorf("the other user's session: %v", err)
			}
		}},
		{"DeleteForUser is not an error for a user without sessions or an unknown user", func(t *testing.T, e env) {
			u := e.user("ana")
			noErr(t, "user without sessions", e.r.Sessions.DeleteForUser(e.ctx, u.ID, nil))
			for _, id := range unknownIDs {
				noErr(t, fmt.Sprintf("DeleteForUser(%q)", id), e.r.Sessions.DeleteForUser(e.ctx, id, nil))
			}
		}},
		{"DeleteExpired removes the sessions with ExpiresAt up to now and counts them", func(t *testing.T, e env) {
			u := e.user("ana")
			expiring := func(d time.Duration) func(*domain.Session) {
				return func(s *domain.Session) {
					s.CreatedAt, s.LastSeenAt = at(-2*time.Hour), at(-2*time.Hour)
					s.ExpiresAt = at(d)
				}
			}
			past := e.session(u.ID, "past", expiring(-time.Hour))
			exact := e.session(u.ID, "exact", expiring(0))
			justAfter := e.session(u.ID, "just-after", expiring(time.Millisecond))
			later := e.session(u.ID, "later", expiring(time.Hour))

			n, err := e.r.Sessions.DeleteExpired(e.ctx, base)
			noErr(t, "DeleteExpired", err)
			if n != 2 {
				t.Errorf("DeleteExpired = %d, want 2", n)
			}
			for _, s := range []domain.Session{past, exact} {
				_, err := e.r.Sessions.Get(e.ctx, s.Hash)
				isErr(t, "Get of an expired session", err, domain.ErrNotFound)
			}
			for _, s := range []domain.Session{justAfter, later} {
				if _, err := e.r.Sessions.Get(e.ctx, s.Hash); err != nil {
					t.Errorf("Get of a live session: %v", err)
				}
			}
			n, err = e.r.Sessions.DeleteExpired(e.ctx, base)
			noErr(t, "second DeleteExpired", err)
			if n != 0 {
				t.Errorf("second DeleteExpired = %d, want 0", n)
			}
		}},
	})
}
