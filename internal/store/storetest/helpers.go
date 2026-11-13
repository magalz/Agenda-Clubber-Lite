package storetest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// base is the instant every fixture is built around. Each time in the suite is
// base plus a whole number of milliseconds, so the exact boundary rules of the
// contract (ExpiresAt > now, NextAttemptAt <= now, ...) mean the same on every
// backend.
var base = time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return base.Add(d) }

func ptr[T any](v T) *T { return &v }

// unknownIDs are IDs the store never issued, in the forms a backend may choke
// on: empty, free text, a well-formed MongoDB ObjectID, a path and a long one.
// unknownRefs leaves out the empty ID, which in a reference field means "nobody".
var (
	unknownIDs  = []string{"", "not-an-id", "0123456789abcdef01234567", "../etc/passwd", strings.Repeat("a", 300)}
	unknownRefs = unknownIDs[1:]
)

// Short names for the enumerations the fixtures use.
const (
	uPendingEmail    = domain.UserStatusPendingEmail
	uPendingApproval = domain.UserStatusPendingApproval
	uActive          = domain.UserStatusActive
	uDisabled        = domain.UserStatusDisabled

	coletivo  = domain.CollectiveKindColetivo
	produtora = domain.CollectiveKindProdutora

	cPending  = domain.CollectiveStatusPending
	cApproved = domain.CollectiveStatusApproved
	cRejected = domain.CollectiveStatusRejected

	mPending  = domain.MembershipStatusPending
	mApproved = domain.MembershipStatusApproved
	mRejected = domain.MembershipStatusRejected

	roleOwner  = domain.RoleOwner
	roleMember = domain.RoleMember

	resetPassword = domain.TokenPurposeResetPassword
	verifyEmail   = domain.TokenPurposeVerifyEmail
	changeEmail   = domain.TokenPurposeChangeEmail
)

// env is what one subtest works with: its own store and the context to call it.
type env struct {
	t   *testing.T
	ctx context.Context
	s   domain.Store
	r   domain.Repos
}

// subtest is one named contract test; it gets a store of its own.
type subtest struct {
	name string
	run  func(t *testing.T, e env)
}

func runSubtests(t *testing.T, newStore NewStore, tests []subtest) {
	t.Helper()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			tc.run(t, env{t: t, ctx: t.Context(), s: s, r: s.Repos()})
		})
	}
}

// --- fixtures: values that are valid by domain's rules, not yet stored ---

func newUser(name string, mutate ...func(*domain.User)) domain.User {
	u := domain.User{
		Name:              name,
		Email:             name + "@example.com",
		Phone:             "11987654321",
		PasswordHash:      "hash-" + name,
		PasswordChangedAt: base,
		Status:            uActive,
		CreatedAt:         base,
		UpdatedAt:         base,
	}
	for _, f := range mutate {
		f(&u)
	}
	return u
}

func newCollective(name string, kind domain.CollectiveKind, status domain.CollectiveStatus, mutate ...func(*domain.Collective)) domain.Collective {
	c := domain.Collective{
		Name:      name,
		NameKey:   domain.NameKey(name),
		Kind:      kind,
		Status:    status,
		CreatedAt: base,
		UpdatedAt: base,
	}
	for _, f := range mutate {
		f(&c)
	}
	return c
}

func newMembership(u domain.User, c domain.Collective, role domain.Role, status domain.MembershipStatus, mutate ...func(*domain.Membership)) domain.Membership {
	m := domain.Membership{UserID: u.ID, CollectiveID: c.ID, Role: role, Status: status, RequestedAt: base}
	for _, f := range mutate {
		f(&m)
	}
	return m
}

// hashOf returns a 32-byte hash (the SHA-256 of tag), so a fixture can be told
// apart by its tag.
func hashOf(tag string) []byte {
	h := sha256.Sum256([]byte(tag))
	return h[:]
}

func newToken(userID string, purpose domain.TokenPurpose, tag string, mutate ...func(*domain.Token)) domain.Token {
	t := domain.Token{UserID: userID, Purpose: purpose, Hash: hashOf(tag), CreatedAt: base, ExpiresAt: at(time.Hour)}
	if purpose == changeEmail {
		t.NewEmail = "novo@example.com"
	}
	for _, f := range mutate {
		f(&t)
	}
	return t
}

func newSession(userID, tag string, mutate ...func(*domain.Session)) domain.Session {
	s := domain.Session{
		Hash:       hashOf(tag),
		UserID:     userID,
		CreatedAt:  base,
		LastSeenAt: base,
		ExpiresAt:  at(24 * time.Hour),
		UserAgent:  "Mozilla/5.0 (contract suite)",
	}
	for _, f := range mutate {
		f(&s)
	}
	return s
}

func newMessage(to string, mutate ...func(*domain.OutboxMessage)) domain.OutboxMessage {
	m := domain.OutboxMessage{
		To:            to,
		Subject:       "Assunto",
		Text:          "Corpo da mensagem",
		HTML:          "<p>Corpo da mensagem</p>",
		NextAttemptAt: base,
		CreatedAt:     base,
	}
	for _, f := range mutate {
		f(&m)
	}
	return m
}

// due makes a message due at base+d.
func due(d time.Duration) func(*domain.OutboxMessage) {
	return func(m *domain.OutboxMessage) { m.NextAttemptAt = at(d) }
}

// newAuditEntry is a valid entry made at base by an admin about nobody: the
// tests that need a user or a collective set their fields.
func newAuditEntry(mutate ...func(*domain.AuditEntry)) domain.AuditEntry {
	a := domain.AuditEntry{
		At:        base,
		Action:    domain.AuditAccountApproved,
		ActorID:   "admin-1",
		ActorName: "Admin",
	}
	for _, f := range mutate {
		f(&a)
	}
	return a
}

// --- fixtures stored through the repositories; a failure ends the subtest ---

func (e env) user(name string, mutate ...func(*domain.User)) domain.User {
	e.t.Helper()
	u := newUser(name, mutate...)
	noErr(e.t, "create user "+name, e.r.Users.Create(e.ctx, &u))
	return u
}

func (e env) getUser(id string) domain.User {
	e.t.Helper()
	u, err := e.r.Users.ByID(e.ctx, id)
	noErr(e.t, "get user "+id, err)
	return u
}

func (e env) collective(name string, kind domain.CollectiveKind, status domain.CollectiveStatus, mutate ...func(*domain.Collective)) domain.Collective {
	e.t.Helper()
	c := newCollective(name, kind, status, mutate...)
	noErr(e.t, "create collective "+name, e.r.Collectives.Create(e.ctx, &c))
	return c
}

func (e env) member(u domain.User, c domain.Collective, role domain.Role, status domain.MembershipStatus, mutate ...func(*domain.Membership)) domain.Membership {
	e.t.Helper()
	m := newMembership(u, c, role, status, mutate...)
	noErr(e.t, "create membership of "+u.Name+" in "+c.Name, e.r.Memberships.Create(e.ctx, m))
	return m
}

func (e env) token(userID string, purpose domain.TokenPurpose, tag string, mutate ...func(*domain.Token)) domain.Token {
	e.t.Helper()
	tok := newToken(userID, purpose, tag, mutate...)
	noErr(e.t, "create token "+tag, e.r.Tokens.Create(e.ctx, &tok))
	return tok
}

func (e env) session(userID, tag string, mutate ...func(*domain.Session)) domain.Session {
	e.t.Helper()
	s := newSession(userID, tag, mutate...)
	noErr(e.t, "create session "+tag, e.r.Sessions.Create(e.ctx, s))
	return s
}

func (e env) message(to string, mutate ...func(*domain.OutboxMessage)) domain.OutboxMessage {
	e.t.Helper()
	m := newMessage(to, mutate...)
	noErr(e.t, "enqueue message to "+to, e.r.Outbox.Enqueue(e.ctx, &m))
	return m
}

func (e env) auditEntry(mutate ...func(*domain.AuditEntry)) domain.AuditEntry {
	e.t.Helper()
	a := newAuditEntry(mutate...)
	noErr(e.t, "create audit entry "+string(a.Action), e.r.AuditEntries.Create(e.ctx, &a))
	return a
}

// --- assertions ---

func noErr(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func isErr(t *testing.T, what string, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Errorf("%s: error = %v, want %v", what, err, target)
	}
}

// wantFieldErrors checks that err is a domain.FieldErrors (the entity's own
// Validate error, as the contract says) naming at least the given fields.
func wantFieldErrors(t *testing.T, what string, err error, fields ...string) {
	t.Helper()
	var fe domain.FieldErrors
	if !errors.As(err, &fe) {
		t.Errorf("%s: error = %v, want domain.FieldErrors", what, err)
		return
	}
	for _, f := range fields {
		if _, ok := fe[f]; !ok {
			t.Errorf("%s: FieldErrors %v lack the field %q", what, fe, f)
		}
	}
}

// wantEqual compares normalized values; see the norm functions below.
func wantEqual(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s:\n got  %s\n want %s", what, show(got), show(want))
	}
}

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return err.Error()
	}
	return string(b)
}

func sameTime(t *testing.T, what string, got, want time.Time) {
	t.Helper()
	if !ms(got).Equal(ms(want)) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func sameTimePtr(t *testing.T, what string, got *time.Time, want time.Time) {
	t.Helper()
	if got == nil {
		t.Errorf("%s is unset, want %v", what, want)
		return
	}
	sameTime(t, what, *got, want)
}

// inLocalZone returns the same instant in a zone that is not UTC, for the
// subtests that write times in a zone and read them back.
func inLocalZone(t time.Time) time.Time { return t.In(time.FixedZone("UTC-3", -3*60*60)) }

// wantUTC checks that got is set, is in UTC and is the instant want.
func wantUTC(t *testing.T, what string, got *time.Time, want time.Time) {
	t.Helper()
	switch {
	case got == nil:
		t.Errorf("%s is unset, want %v", what, want)
	case got.Location() != time.UTC:
		t.Errorf("%s is in %v, want UTC", what, got.Location())
	default:
		sameTime(t, what, *got, want)
	}
}

func unsetTime(t *testing.T, what string, got *time.Time) {
	t.Helper()
	if got != nil {
		t.Errorf("%s = %v, want it unset", what, *got)
	}
}

// wantIDs ends the subtest on a mismatch, because the checks that follow read
// the messages it vouched for.
func wantIDs(t *testing.T, what string, got []domain.OutboxMessage, want ...domain.OutboxMessage) {
	t.Helper()
	g, w := make([]string, len(got)), make([]string, len(want))
	for i := range got {
		g[i] = got[i].ID
	}
	for i := range want {
		w[i] = want[i].ID
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("%s: IDs = %v, want %v", what, g, w)
	}
}

func wantStrings(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %q, want %q", what, got, want)
	}
}

// --- normalization: whole milliseconds in UTC, so DeepEqual can compare ---

func ms(t time.Time) time.Time { return t.UTC().Truncate(time.Millisecond) }

func msPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	return ptr(ms(*t))
}

func normUser(u domain.User) domain.User {
	u.PasswordChangedAt = ms(u.PasswordChangedAt)
	u.LockedUntil = msPtr(u.LockedUntil)
	u.EmailVerifiedAt = msPtr(u.EmailVerifiedAt)
	u.ApprovedAt = msPtr(u.ApprovedAt)
	u.CreatedAt = ms(u.CreatedAt)
	u.UpdatedAt = ms(u.UpdatedAt)
	return u
}

func normCollective(c domain.Collective) domain.Collective {
	c.ApprovedAt = msPtr(c.ApprovedAt)
	c.CreatedAt = ms(c.CreatedAt)
	c.UpdatedAt = ms(c.UpdatedAt)
	return c
}

func normMembership(m domain.Membership) domain.Membership {
	m.AdminApprovedAt = msPtr(m.AdminApprovedAt)
	m.OwnerApprovedAt = msPtr(m.OwnerApprovedAt)
	m.RequestedAt = ms(m.RequestedAt)
	m.DecidedAt = msPtr(m.DecidedAt)
	return m
}

func normView(v domain.MembershipView) domain.MembershipView {
	v.Membership = normMembership(v.Membership)
	return v
}

func normToken(t domain.Token) domain.Token {
	t.ExpiresAt = ms(t.ExpiresAt)
	t.UsedAt = msPtr(t.UsedAt)
	t.CreatedAt = ms(t.CreatedAt)
	return t
}

func normSession(s domain.Session) domain.Session {
	s.CreatedAt = ms(s.CreatedAt)
	s.LastSeenAt = ms(s.LastSeenAt)
	s.ExpiresAt = ms(s.ExpiresAt)
	return s
}

func normMessage(m domain.OutboxMessage) domain.OutboxMessage {
	m.NextAttemptAt = ms(m.NextAttemptAt)
	m.ClaimedUntil = msPtr(m.ClaimedUntil)
	m.SentAt = msPtr(m.SentAt)
	m.CreatedAt = ms(m.CreatedAt)
	return m
}

func normAuditEntry(a domain.AuditEntry) domain.AuditEntry {
	a.At = ms(a.At)
	return a
}

// --- concurrency: the goroutines only call the store and return what they got;
// every assertion runs in the test's own goroutine ---

type outcome[T any] struct {
	val T
	err error
}

// parallel runs fn n times at once, started together by a barrier, and returns
// the results in call order.
func parallel[T any](n int, fn func(i int) (T, error)) []outcome[T] {
	out := make([]outcome[T], n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			out[i].val, out[i].err = fn(i)
		}()
	}
	close(start)
	wg.Wait()
	return out
}

func parallelErr(n int, fn func(i int) error) []error {
	res := parallel(n, func(i int) (struct{}, error) { return struct{}{}, fn(i) })
	errs := make([]error, n)
	for i, r := range res {
		errs[i] = r.err
	}
	return errs
}

// wantOneWinner checks that exactly one call succeeded and that every other one
// failed with the loser error.
func wantOneWinner(t *testing.T, what string, errs []error, loser error) {
	t.Helper()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, loser):
			t.Errorf("%s: error = %v, want nil or %v", what, err, loser)
		}
	}
	if wins != 1 {
		t.Errorf("%s: %d of %d calls succeeded, want exactly 1", what, wins, len(errs))
	}
}
