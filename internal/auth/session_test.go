package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
)

// sessionNow is the fixed clock of the session tests. It has a sub-millisecond
// part on purpose: the session times are whole milliseconds, like MongoDB's.
var sessionNow = time.Date(2026, 11, 12, 16, 0, 0, 123_456_789, time.UTC)

var errBoom = errors.New("boom")

// newSessionStore returns the sessions of an isolated test database, and the ID
// of an active user in it.
func newSessionStore(t *testing.T) (repos domain.Repos, userID string) {
	t.Helper()
	repos = mongotest.New(t).Repos()
	return repos, newSessionUser(t, repos, "ana@example.com")
}

func newSessionUser(t *testing.T, repos domain.Repos, email string) string {
	t.Helper()
	verified := sessionNow
	u := domain.User{
		Name:              "Pessoa Teste",
		Email:             email,
		Phone:             "81987654321",
		PasswordHash:      "placeholder-hash",
		PasswordChangedAt: sessionNow,
		Status:            domain.UserStatusActive,
		EmailVerifiedAt:   &verified,
		CreatedAt:         sessionNow,
		UpdatedAt:         sessionNow,
	}
	if err := repos.Users.Create(context.Background(), &u); err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	return u.ID
}

// mustStart starts a session at sessionNow and returns its cookie value and the
// session as StartSession returned it.
func mustStart(t *testing.T, sessions domain.Sessions, userID string) (string, domain.Session) {
	t.Helper()
	raw, s, err := StartSession(context.Background(), sessions, userID, "test-agent", sessionNow)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return raw, s
}

// stored returns the session as the store holds it.
func stored(t *testing.T, sessions domain.Sessions, raw string) domain.Session {
	t.Helper()
	hash, err := HashToken(raw)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}
	s, err := sessions.Get(context.Background(), hash)
	if err != nil {
		t.Fatalf("Get the stored session: %v", err)
	}
	return s
}

func wantGone(t *testing.T, sessions domain.Sessions, raw string) {
	t.Helper()
	hash, err := HashToken(raw)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}
	if _, err := sessions.Get(context.Background(), hash); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("Get of the session = %v, want ErrNotFound", err)
	}
}

func sameSession(t *testing.T, what string, got, want domain.Session) {
	t.Helper()
	if !bytes.Equal(got.Hash, want.Hash) || got.UserID != want.UserID || got.UserAgent != want.UserAgent ||
		!got.CreatedAt.Equal(want.CreatedAt) || !got.LastSeenAt.Equal(want.LastSeenAt) || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("%s:\n got  %+v\n want %+v", what, got, want)
	}
}

func TestSessionConstants(t *testing.T) {
	if SessionCookieName != "__Host-acl_session" {
		t.Errorf("SessionCookieName = %q", SessionCookieName)
	}
	if SessionIdleTimeout != 7*24*time.Hour || SessionMaxLifetime != 30*24*time.Hour || SessionTouchInterval != 5*time.Minute {
		t.Errorf("timings = %v, %v, %v", SessionIdleTimeout, SessionMaxLifetime, SessionTouchInterval)
	}
}

func TestStartSessionStoresOnlyTheHash(t *testing.T) {
	repos, uid := newSessionStore(t)
	zone := time.FixedZone("UTC-3", -3*3600)

	raw, s, err := StartSession(context.Background(), repos.Sessions, uid, "curl/8", sessionNow.In(zone))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != 43 {
		t.Errorf("cookie value has %d characters, want 43", len(raw))
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) != 32 {
		t.Fatalf("cookie value is not 32 bytes of base64url: %d bytes, %v", len(b), err)
	}
	sum := sha256.Sum256(b)

	created := sessionNow.Truncate(time.Millisecond)
	want := domain.Session{
		Hash: sum[:], UserID: uid, UserAgent: "curl/8",
		CreatedAt: created, LastSeenAt: created, ExpiresAt: created.Add(7 * 24 * time.Hour),
	}
	sameSession(t, "returned session", s, want)
	if s.CreatedAt.Location() != time.UTC || s.LastSeenAt.Location() != time.UTC || s.ExpiresAt.Location() != time.UTC {
		t.Errorf("times are not in UTC: %v %v %v", s.CreatedAt.Location(), s.LastSeenAt.Location(), s.ExpiresAt.Location())
	}
	sameSession(t, "stored session", stored(t, repos.Sessions, raw), want)

	// Resolving soon after returns the same session and changes nothing.
	got, err := ResolveSession(context.Background(), repos.Sessions, raw, sessionNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("ResolveSession: %v", err)
	}
	sameSession(t, "resolved session", got, want)
}

func TestStartSessionTokensAreDistinct(t *testing.T) {
	repos, uid := newSessionStore(t)
	a, _ := mustStart(t, repos.Sessions, uid)
	b, _ := mustStart(t, repos.Sessions, uid)
	if a == b {
		t.Fatal("two sessions got the same cookie value")
	}
	_ = stored(t, repos.Sessions, a)
	_ = stored(t, repos.Sessions, b)
}

func TestStartSessionUnknownUser(t *testing.T) {
	repos, _ := newSessionStore(t)
	for _, id := range []string{"000000000000000000000000", "no-such-user"} {
		raw, _, err := StartSession(context.Background(), repos.Sessions, id, "ua", sessionNow)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("StartSession for user %q = %v, want ErrNotFound", id, err)
		}
		if raw != "" {
			t.Errorf("StartSession for user %q returned a cookie value with its error", id)
		}
	}
}

func TestStartSessionCleansTheUserAgent(t *testing.T) {
	repos, uid := newSessionStore(t)
	for _, c := range []struct {
		name, ua, want string
	}{
		{"too long", strings.Repeat("é", 400), strings.Repeat("é", domain.UserAgentMax)},
		{"invalid utf-8", "ab\xff\xfecd", "ab\ufffdcd"},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, s, err := StartSession(context.Background(), repos.Sessions, uid, c.ua, sessionNow)
			if err != nil {
				t.Fatal(err)
			}
			if s.UserAgent != c.want {
				t.Errorf("returned user agent = %q, want %q", s.UserAgent, c.want)
			}
			if got := stored(t, repos.Sessions, raw).UserAgent; got != c.want {
				t.Errorf("stored user agent = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCleanUserAgent(t *testing.T) {
	uaMax := domain.UserAgentMax
	for _, c := range []struct {
		name, ua, want string
	}{
		{"empty", "", ""},
		{"short", "Mozilla/5.0", "Mozilla/5.0"},
		{"exactly the maximum", strings.Repeat("a", uaMax), strings.Repeat("a", uaMax)},
		{"one over", strings.Repeat("a", uaMax+1), strings.Repeat("a", uaMax)},
		{"two-byte runes", strings.Repeat("é", uaMax+50), strings.Repeat("é", uaMax)},
		{"four-byte runes", strings.Repeat("😀", uaMax+1), strings.Repeat("😀", uaMax)},
		{"cut falls between runes", strings.Repeat("a", uaMax-1) + "é😀tail", strings.Repeat("a", uaMax-1) + "é"},
		{"invalid byte", "ab\xffcd", "ab\ufffdcd"},
		{"a run of invalid bytes is one replacement", "ab\xff\xfe\xfdcd", "ab\ufffdcd"},
		{"truncated rune", "ab\xc3", "ab\ufffd"},
		{"invalid bytes and over long", "\xff" + strings.Repeat("é", uaMax+10), "\ufffd" + strings.Repeat("é", uaMax-1)},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := cleanUserAgent(c.ua)
			if got != c.want {
				t.Errorf("cleanUserAgent = %q, want %q", got, c.want)
			}
			if !utf8.ValidString(got) || utf8.RuneCountInString(got) > uaMax {
				t.Errorf("result is not valid UTF-8 of at most %d runes", uaMax)
			}
		})
	}
}

func TestResolveSessionRejectsMalformedAndUnknownValues(t *testing.T) {
	repos, _ := newSessionStore(t)
	unknown, _, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "abc", strings.Repeat("A", 43) + "=", strings.Repeat("A", 44), "not base64 url !!", unknown} {
		got, err := ResolveSession(context.Background(), repos.Sessions, raw, sessionNow)
		if !errors.Is(err, ErrNoSession) {
			t.Errorf("ResolveSession(%q) = %v, want ErrNoSession", raw, err)
		}
		if got.UserID != "" || got.Hash != nil {
			t.Errorf("ResolveSession(%q) returned a session with its error: %+v", raw, got)
		}
	}
}

func TestResolveSessionIdleExpiry(t *testing.T) {
	repos, uid := newSessionStore(t)
	for _, c := range []struct {
		name   string
		offset time.Duration // from ExpiresAt
		valid  bool
	}{
		{"1ms before ExpiresAt", -time.Millisecond, true},
		{"exactly at ExpiresAt", 0, false},
		{"long after ExpiresAt", 90 * 24 * time.Hour, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, s := mustStart(t, repos.Sessions, uid)
			_, err := ResolveSession(context.Background(), repos.Sessions, raw, s.ExpiresAt.Add(c.offset))
			if c.valid {
				if err != nil {
					t.Fatalf("ResolveSession = %v, want the session", err)
				}
				return
			}
			if !errors.Is(err, ErrNoSession) {
				t.Fatalf("ResolveSession = %v, want ErrNoSession", err)
			}
			wantGone(t, repos.Sessions, raw)
		})
	}
}

// The slide never passes CreatedAt+30d, so the 30-day check cannot fire through
// StartSession and ResolveSession alone: a session whose stored ExpiresAt is
// later (written by another version of the code, or by hand) tests it.
func TestResolveSessionMaxLifetimeEvenWhenStoredExpiryIsLater(t *testing.T) {
	repos, uid := newSessionStore(t)
	created := sessionNow.Truncate(time.Millisecond)
	insert := func() string {
		raw, hash, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		err = repos.Sessions.Create(context.Background(), domain.Session{
			Hash: hash, UserID: uid, UserAgent: "ua",
			CreatedAt: created, LastSeenAt: created.Add(29 * 24 * time.Hour), ExpiresAt: created.Add(40 * 24 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	t.Run("at CreatedAt+30d", func(t *testing.T) {
		raw := insert()
		if _, err := ResolveSession(context.Background(), repos.Sessions, raw, created.Add(SessionMaxLifetime)); !errors.Is(err, ErrNoSession) {
			t.Fatalf("ResolveSession = %v, want ErrNoSession", err)
		}
		wantGone(t, repos.Sessions, raw)
	})
	t.Run("1ms before, the slide is cut back to CreatedAt+30d", func(t *testing.T) {
		raw := insert()
		now := created.Add(SessionMaxLifetime - time.Millisecond)
		got, err := ResolveSession(context.Background(), repos.Sessions, raw, now)
		if err != nil {
			t.Fatal(err)
		}
		if want := created.Add(SessionMaxLifetime); !got.ExpiresAt.Equal(want) || !stored(t, repos.Sessions, raw).ExpiresAt.Equal(want) {
			t.Errorf("ExpiresAt returned %v, stored %v, want %v", got.ExpiresAt, stored(t, repos.Sessions, raw).ExpiresAt, want)
		}
	})
}

func TestResolveSessionWritesAtMostEveryFiveMinutes(t *testing.T) {
	repos, uid := newSessionStore(t)
	raw, s := mustStart(t, repos.Sessions, uid)

	// Just under the interval: no write, in the result or in the store.
	got, err := ResolveSession(context.Background(), repos.Sessions, raw, s.LastSeenAt.Add(SessionTouchInterval-time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	sameSession(t, "resolved before the interval", got, s)
	sameSession(t, "stored before the interval", stored(t, repos.Sessions, raw), s)

	// Exactly at the interval: the use is recorded and the expiry slides.
	touched := s
	touched.LastSeenAt = s.LastSeenAt.Add(SessionTouchInterval)
	touched.ExpiresAt = touched.LastSeenAt.Add(SessionIdleTimeout)
	got, err = ResolveSession(context.Background(), repos.Sessions, raw, touched.LastSeenAt)
	if err != nil {
		t.Fatal(err)
	}
	sameSession(t, "resolved at the interval", got, touched)
	sameSession(t, "stored at the interval", stored(t, repos.Sessions, raw), touched)

	// The next interval counts from that write.
	got, err = ResolveSession(context.Background(), repos.Sessions, raw, touched.LastSeenAt.Add(SessionTouchInterval-time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	sameSession(t, "resolved inside the second interval", got, touched)
	sameSession(t, "stored inside the second interval", stored(t, repos.Sessions, raw), touched)

	// A clock with a sub-millisecond part: the times written are whole milliseconds.
	at := touched.LastSeenAt.Add(SessionTouchInterval + 456*time.Microsecond)
	touched.LastSeenAt = at.Truncate(time.Millisecond)
	touched.ExpiresAt = touched.LastSeenAt.Add(SessionIdleTimeout)
	got, err = ResolveSession(context.Background(), repos.Sessions, raw, at)
	if err != nil {
		t.Fatal(err)
	}
	sameSession(t, "resolved with a sub-millisecond clock", got, touched)
	sameSession(t, "stored with a sub-millisecond clock", stored(t, repos.Sessions, raw), touched)
}

func TestResolveSessionSlideNeverPassesThirtyDays(t *testing.T) {
	repos, uid := newSessionStore(t)
	raw, s := mustStart(t, repos.Sessions, uid)
	created := s.CreatedAt
	day := 24 * time.Hour

	// A visit every 6 days keeps a session alive past the 7-day idle limit; while
	// the slide fits within 30 days it is a plain 7 days after the visit.
	for _, d := range []time.Duration{6 * day, 12 * day, 18 * day} {
		got, err := ResolveSession(context.Background(), repos.Sessions, raw, created.Add(d))
		if err != nil {
			t.Fatalf("day %v: %v", d/day, err)
		}
		if want := created.Add(d + SessionIdleTimeout); !got.ExpiresAt.Equal(want) {
			t.Errorf("day %v: ExpiresAt = %v, want visit + 7d = %v", d/day, got.ExpiresAt, want)
		}
	}
	// From day 24 the 7-day slide would pass day 30: it stops there.
	for _, d := range []time.Duration{24 * day, 29*day + 21*time.Hour + 36*time.Minute} { // day 29.9
		got, err := ResolveSession(context.Background(), repos.Sessions, raw, created.Add(d))
		if err != nil {
			t.Fatalf("day %v: %v", d, err)
		}
		if want := created.Add(SessionMaxLifetime); !got.ExpiresAt.Equal(want) {
			t.Errorf("day %v: ExpiresAt = %v, want CreatedAt+30d = %v", d, got.ExpiresAt, want)
		}
		if got := stored(t, repos.Sessions, raw).ExpiresAt; !got.Equal(created.Add(SessionMaxLifetime)) {
			t.Errorf("day %v: stored ExpiresAt = %v, want CreatedAt+30d", d, got)
		}
	}
	// At day 30 the session is over, however recently it was used.
	if _, err := ResolveSession(context.Background(), repos.Sessions, raw, created.Add(SessionMaxLifetime)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("day 30: %v, want ErrNoSession", err)
	}
	wantGone(t, repos.Sessions, raw)
}

func TestEndSession(t *testing.T) {
	repos, uid := newSessionStore(t)
	ctx := context.Background()
	raw, _ := mustStart(t, repos.Sessions, uid)
	other, _ := mustStart(t, repos.Sessions, uid)

	if err := EndSession(ctx, repos.Sessions, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveSession(ctx, repos.Sessions, raw, sessionNow.Add(time.Minute)); !errors.Is(err, ErrNoSession) {
		t.Errorf("ResolveSession after EndSession = %v, want ErrNoSession", err)
	}
	wantGone(t, repos.Sessions, raw)
	if _, err := ResolveSession(ctx, repos.Sessions, other, sessionNow.Add(time.Minute)); err != nil {
		t.Errorf("EndSession ended another session: %v", err)
	}

	unknown, _, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{raw, unknown, "", "garbage", strings.Repeat("A", 44)} {
		if err := EndSession(ctx, repos.Sessions, v); err != nil {
			t.Errorf("EndSession(%q) = %v, want nil", v, err)
		}
	}
}

func TestRevokeOtherSessionsKeepsExactlyOne(t *testing.T) {
	repos, uid := newSessionStore(t)
	ctx := context.Background()
	bob := newSessionUser(t, repos, "bob@example.com")
	later := sessionNow.Add(time.Minute)

	start := func(user string, n int) []string {
		var raws []string
		for range n {
			raw, _ := mustStart(t, repos.Sessions, user)
			raws = append(raws, raw)
		}
		return raws
	}
	alive := func(raw string) bool {
		_, err := ResolveSession(ctx, repos.Sessions, raw, later)
		if err != nil && !errors.Is(err, ErrNoSession) {
			t.Fatalf("ResolveSession: %v", err)
		}
		return err == nil
	}

	ana := start(uid, 3)
	bobs := start(bob, 1)
	if err := RevokeOtherSessions(ctx, repos.Sessions, uid, ana[1]); err != nil {
		t.Fatal(err)
	}
	if alive(ana[0]) || !alive(ana[1]) || alive(ana[2]) {
		t.Errorf("after keeping the second session: alive = %v %v %v, want false true false", alive(ana[0]), alive(ana[1]), alive(ana[2]))
	}
	if !alive(bobs[0]) {
		t.Error("another user's session was revoked")
	}

	// No session to keep: an empty or malformed value revokes them all.
	for _, keep := range []string{"", "garbage"} {
		ana = start(uid, 2)
		if err := RevokeOtherSessions(ctx, repos.Sessions, uid, keep); err != nil {
			t.Fatalf("keep %q: %v", keep, err)
		}
		if alive(ana[0]) || alive(ana[1]) {
			t.Errorf("keep %q: a session of the user survived", keep)
		}
		if !alive(bobs[0]) {
			t.Errorf("keep %q: another user's session was revoked", keep)
		}
	}
}

// fakeSessions is a domain.Sessions that fails the test on a method the test
// did not set up, so a test can show that the store is not touched.
type fakeSessions struct {
	t             *testing.T
	get           func(hash []byte) (domain.Session, error)
	touch         func(hash []byte, lastSeen, expiresAt time.Time) error
	del           func(hash []byte) error
	create        func(s domain.Session) error
	deleteForUser func(userID string, keep []byte) error
}

func (f *fakeSessions) Create(_ context.Context, s domain.Session) error {
	f.t.Helper()
	if f.create == nil {
		f.t.Fatal("unexpected Create")
	}
	return f.create(s)
}

func (f *fakeSessions) Get(_ context.Context, hash []byte) (domain.Session, error) {
	f.t.Helper()
	if f.get == nil {
		f.t.Fatal("unexpected Get")
	}
	return f.get(hash)
}

func (f *fakeSessions) Touch(_ context.Context, hash []byte, lastSeen, expiresAt time.Time) error {
	f.t.Helper()
	if f.touch == nil {
		f.t.Fatal("unexpected Touch")
	}
	return f.touch(hash, lastSeen, expiresAt)
}

func (f *fakeSessions) Delete(_ context.Context, hash []byte) error {
	f.t.Helper()
	if f.del == nil {
		f.t.Fatal("unexpected Delete")
	}
	return f.del(hash)
}

func (f *fakeSessions) DeleteForUser(_ context.Context, userID string, keep []byte) error {
	f.t.Helper()
	if f.deleteForUser == nil {
		f.t.Fatal("unexpected DeleteForUser")
	}
	return f.deleteForUser(userID, keep)
}

func (f *fakeSessions) DeleteExpired(context.Context, time.Time) (int, error) {
	f.t.Helper()
	f.t.Fatal("unexpected DeleteExpired")
	return 0, nil
}

func TestMalformedCookieValueNeverReachesTheStore(t *testing.T) {
	store := &fakeSessions{t: t}
	for _, raw := range []string{"", "abc", strings.Repeat("A", 44), "not base64 url !!"} {
		if _, err := ResolveSession(context.Background(), store, raw, sessionNow); !errors.Is(err, ErrNoSession) {
			t.Errorf("ResolveSession(%q) = %v, want ErrNoSession", raw, err)
		}
		if err := EndSession(context.Background(), store, raw); err != nil {
			t.Errorf("EndSession(%q) = %v, want nil", raw, err)
		}
	}
}

// fakeSession returns a cookie value and a session in a store that is a fake.
func fakeSession(t *testing.T, mutate func(*domain.Session)) (string, domain.Session) {
	t.Helper()
	raw, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	created := sessionNow.Truncate(time.Millisecond)
	s := domain.Session{
		Hash: hash, UserID: "u1", UserAgent: "ua",
		CreatedAt: created, LastSeenAt: created, ExpiresAt: created.Add(SessionIdleTimeout),
	}
	if mutate != nil {
		mutate(&s)
	}
	return raw, s
}

func TestResolveSessionStoreFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("Get fails", func(t *testing.T) {
		raw, _ := fakeSession(t, nil)
		store := &fakeSessions{t: t, get: func([]byte) (domain.Session, error) { return domain.Session{}, errBoom }}
		_, err := ResolveSession(ctx, store, raw, sessionNow)
		if !errors.Is(err, errBoom) || errors.Is(err, ErrNoSession) {
			t.Errorf("ResolveSession = %v, want the store's error and not ErrNoSession", err)
		}
		if strings.Contains(err.Error(), raw) {
			t.Error("the error text repeats the cookie value")
		}
	})

	t.Run("the delete of an expired session fails", func(t *testing.T) {
		raw, s := fakeSession(t, nil)
		store := &fakeSessions{
			t:   t,
			get: func([]byte) (domain.Session, error) { return s, nil },
			del: func([]byte) error { return errBoom },
		}
		_, err := ResolveSession(ctx, store, raw, s.ExpiresAt)
		if !errors.Is(err, errBoom) || errors.Is(err, ErrNoSession) {
			t.Errorf("ResolveSession = %v, want the delete error and not ErrNoSession", err)
		}
	})

	t.Run("an expired session is deleted by its hash", func(t *testing.T) {
		raw, s := fakeSession(t, nil)
		var deleted []byte
		store := &fakeSessions{
			t:   t,
			get: func([]byte) (domain.Session, error) { return s, nil },
			del: func(h []byte) error { deleted = h; return nil },
		}
		if _, err := ResolveSession(ctx, store, raw, s.ExpiresAt); !errors.Is(err, ErrNoSession) {
			t.Fatalf("ResolveSession = %v, want ErrNoSession", err)
		}
		if !bytes.Equal(deleted, s.Hash) {
			t.Error("the session deleted is not the one resolved")
		}
	})

	t.Run("Touch fails", func(t *testing.T) {
		raw, s := fakeSession(t, nil)
		store := &fakeSessions{
			t:     t,
			get:   func([]byte) (domain.Session, error) { return s, nil },
			touch: func([]byte, time.Time, time.Time) error { return errBoom },
		}
		_, err := ResolveSession(ctx, store, raw, s.LastSeenAt.Add(time.Hour))
		if !errors.Is(err, errBoom) || errors.Is(err, ErrNoSession) {
			t.Errorf("ResolveSession = %v, want the store's error and not ErrNoSession", err)
		}
	})

	t.Run("the session is ended while it is touched", func(t *testing.T) {
		raw, s := fakeSession(t, nil)
		store := &fakeSessions{
			t:     t,
			get:   func([]byte) (domain.Session, error) { return s, nil },
			touch: func([]byte, time.Time, time.Time) error { return domain.ErrNotFound },
		}
		if _, err := ResolveSession(ctx, store, raw, s.LastSeenAt.Add(time.Hour)); !errors.Is(err, ErrNoSession) {
			t.Errorf("ResolveSession = %v, want ErrNoSession", err)
		}
	})

	t.Run("the session was already gone", func(t *testing.T) {
		raw, _ := fakeSession(t, nil)
		store := &fakeSessions{t: t, get: func([]byte) (domain.Session, error) { return domain.Session{}, domain.ErrNotFound }}
		if _, err := ResolveSession(ctx, store, raw, sessionNow); !errors.Is(err, ErrNoSession) {
			t.Errorf("ResolveSession = %v, want ErrNoSession", err)
		}
	})
}

func TestStartAndEndSessionStoreFailures(t *testing.T) {
	ctx := context.Background()

	store := &fakeSessions{t: t, create: func(domain.Session) error { return errBoom }}
	raw, s, err := StartSession(ctx, store, "u1", "ua", sessionNow)
	if !errors.Is(err, errBoom) {
		t.Errorf("StartSession = %v, want the store's error", err)
	}
	if raw != "" || s.Hash != nil {
		t.Error("StartSession returned a cookie value or a session with its error")
	}

	raw, _ = fakeSession(t, nil)
	store = &fakeSessions{t: t, del: func([]byte) error { return errBoom }}
	if err := EndSession(ctx, store, raw); !errors.Is(err, errBoom) {
		t.Errorf("EndSession = %v, want the store's error", err)
	} else if strings.Contains(err.Error(), raw) {
		t.Error("the error text repeats the cookie value")
	}
}

func TestRevokeOtherSessionsPassesTheHashOfTheKeptSession(t *testing.T) {
	raw, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, keep string
		want       []byte
	}{
		{"a valid value", raw, hash},
		{"an empty value", "", nil},
		{"a malformed value", "garbage", nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			var gotUser string
			var gotKeep []byte
			called := false
			store := &fakeSessions{t: t, deleteForUser: func(userID string, keep []byte) error {
				called, gotUser, gotKeep = true, userID, keep
				return nil
			}}
			if err := RevokeOtherSessions(context.Background(), store, "u1", c.keep); err != nil {
				t.Fatal(err)
			}
			if !called || gotUser != "u1" {
				t.Fatalf("DeleteForUser called = %v for user %q, want u1", called, gotUser)
			}
			if !bytes.Equal(gotKeep, c.want) || (c.want == nil && len(gotKeep) != 0) {
				t.Errorf("keep = %x, want %x", gotKeep, c.want)
			}
		})
	}

	store := &fakeSessions{t: t, deleteForUser: func(string, []byte) error { return errBoom }}
	if err := RevokeOtherSessions(context.Background(), store, "u1", raw); !errors.Is(err, errBoom) {
		t.Errorf("RevokeOtherSessions = %v, want the store's error", err)
	}
}
