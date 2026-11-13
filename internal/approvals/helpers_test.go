package approvals

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
)

// Fixtures and readers shared by every test in the package. A later task adds
// its own fixtures in its own file, with the prefix of its flow, so tasks
// written in parallel do not collide.

// testNow is the fixed clock every approvals test starts from; a test that needs
// a later instant adds to it, so a stored time shows which step wrote it.
var testNow = time.Date(2026, 11, 13, 16, 0, 0, 0, time.UTC)

// testOrigin is the site address (APP_ORIGIN) the email links start with.
const testOrigin = "https://agenda.test"

// newTestStore returns an isolated, empty store on MONGO_TEST_URI. It skips
// locally and fails in CI when the variable is unset (see mongotest).
func newTestStore(t *testing.T) domain.Store {
	t.Helper()
	return mongotest.New(t)
}

// insertUser stores an active user with a verified email and returns it with its
// ID. mutate adjusts the fixture before it is stored, for example to set Status
// or IsAdmin.
func insertUser(t *testing.T, s domain.Store, email string, mutate ...func(*domain.User)) domain.User {
	t.Helper()
	verified := testNow
	u := domain.User{
		Name:              "Pessoa " + email[:strings.IndexByte(email, '@')],
		Email:             email,
		Phone:             "81987654321",
		PasswordHash:      "placeholder-hash",
		PasswordChangedAt: testNow,
		Status:            domain.UserStatusActive,
		EmailVerifiedAt:   &verified,
		CreatedAt:         testNow,
		UpdatedAt:         testNow,
	}
	for _, f := range mutate {
		f(&u)
	}
	if err := s.Repos().Users.Create(t.Context(), &u); err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	return u
}

// insertAdmin stores an active site admin.
func insertAdmin(t *testing.T, s domain.Store, email string) domain.User {
	t.Helper()
	return insertUser(t, s, email, func(u *domain.User) { u.IsAdmin = true })
}

// insertCollective stores a pending collective of kind coletivo created by
// createdBy (a user ID, or empty). mutate adjusts it before it is stored, for
// example to set Status, EventsBlocked or Locked.
func insertCollective(t *testing.T, s domain.Store, name, createdBy string, mutate ...func(*domain.Collective)) domain.Collective {
	t.Helper()
	c := domain.Collective{
		Name:      name,
		NameKey:   domain.NameKey(name),
		Kind:      domain.CollectiveKindColetivo,
		Status:    domain.CollectiveStatusPending,
		CreatedBy: createdBy,
		CreatedAt: testNow,
		UpdatedAt: testNow,
	}
	for _, f := range mutate {
		f(&c)
	}
	if err := s.Repos().Collectives.Create(t.Context(), &c); err != nil {
		t.Fatalf("insert collective %q: %v", name, err)
	}
	return c
}

// insertMembership stores m as it is.
func insertMembership(t *testing.T, s domain.Store, m domain.Membership) {
	t.Helper()
	if err := s.Repos().Memberships.Create(t.Context(), m); err != nil {
		t.Fatalf("insert membership of %s in %s: %v", m.UserID, m.CollectiveID, err)
	}
}

// insertNewCollective stores what a signup that creates a collective leaves
// behind for the given creator: a pending collective and the creator's
// membership as an owner, with the owner side already approved and the admin
// side open. mutate adjusts the collective before it is stored.
func insertNewCollective(t *testing.T, s domain.Store, name string, creator domain.User, mutate ...func(*domain.Collective)) (domain.Collective, domain.Membership) {
	t.Helper()
	c := insertCollective(t, s, name, creator.ID, mutate...)
	ownerApprovedAt := testNow
	m := domain.Membership{
		UserID:          creator.ID,
		CollectiveID:    c.ID,
		Role:            domain.RoleOwner,
		Status:          domain.MembershipStatusPending,
		OwnerApprovedBy: creator.ID,
		OwnerApprovedAt: &ownerApprovedAt,
		RequestedAt:     testNow,
	}
	insertMembership(t, s, m)
	return c, m
}

// outboxMessages returns the emails waiting in the outbox. It claims them at
// testNow with a lease, so call it once per test.
func outboxMessages(t *testing.T, s domain.Store) []domain.OutboxMessage {
	t.Helper()
	msgs, err := s.Repos().Outbox.Claim(t.Context(), testNow.Add(24*time.Hour), time.Minute, 50)
	if err != nil {
		t.Fatalf("claim outbox: %v", err)
	}
	return msgs
}

// onlyMessage returns the single email in msgs and fails when there is any
// other number of them.
func onlyMessage(t *testing.T, msgs []domain.OutboxMessage) domain.OutboxMessage {
	t.Helper()
	if len(msgs) != 1 {
		t.Fatalf("%d emails queued, want exactly 1: %+v", len(msgs), msgs)
	}
	return msgs[0]
}

// auditEntries returns the audit log, newest first. It also fails the test when
// an entry holds an "@" in any text field: the log keeps IDs and name snapshots
// and never an email address, so every test that reads it checks that rule.
func auditEntries(t *testing.T, s domain.Store) []domain.AuditEntry {
	t.Helper()
	entries, err := s.Repos().AuditEntries.List(t.Context(), domain.ListLimitMax)
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	for _, e := range entries {
		for _, v := range []string{e.ActorID, e.ActorName, e.UserID, e.UserName, e.CollectiveID, e.CollectiveName, e.Detail, e.Reason} {
			if strings.Contains(v, "@") {
				t.Errorf("audit entry %s holds an email address: %+v", e.Action, e)
			}
		}
	}
	return entries
}

// onlyAudit returns the single audit entry in entries and fails when there is
// any other number of them.
func onlyAudit(t *testing.T, entries []domain.AuditEntry) domain.AuditEntry {
	t.Helper()
	if len(entries) != 1 {
		t.Fatalf("%d audit entries, want exactly 1: %+v", len(entries), entries)
	}
	return entries[0]
}

// collectiveOf reads the collective, failing the test when it is missing.
func collectiveOf(t *testing.T, s domain.Store, id string) domain.Collective {
	t.Helper()
	c, err := s.Repos().Collectives.ByID(t.Context(), id)
	if err != nil {
		t.Fatalf("read collective %s: %v", id, err)
	}
	return c
}

// membershipOf reads the membership, failing the test when it is missing.
func membershipOf(t *testing.T, s domain.Store, userID, collectiveID string) domain.Membership {
	t.Helper()
	m, err := s.Repos().Memberships.Get(t.Context(), userID, collectiveID)
	if err != nil {
		t.Fatalf("read membership of %s in %s: %v", userID, collectiveID, err)
	}
	return m
}

// requireErrorIs fails the test unless err matches want.
func requireErrorIs(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// requireReasonError fails the test unless err is a domain.FieldErrors that
// fails the key "reason" and nothing else.
func requireReasonError(t *testing.T, err error) {
	t.Helper()
	var fe domain.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want a domain.FieldErrors", err)
	}
	if _, ok := fe["reason"]; !ok || len(fe) != 1 {
		t.Fatalf("field errors %v, want only the key reason", fe)
	}
}

// sameTime reports whether got is set and equals want to the millisecond, the
// precision the store keeps.
func sameTime(got *time.Time, want time.Time) bool {
	return got != nil && got.Truncate(time.Millisecond).Equal(want.Truncate(time.Millisecond))
}

// sameAudit reports whether got is the entry want, ignoring the ID the store
// assigned and comparing At to the millisecond.
func sameAudit(got, want domain.AuditEntry) bool {
	if !got.At.Truncate(time.Millisecond).Equal(want.At.Truncate(time.Millisecond)) {
		return false
	}
	got.ID, got.At, want.ID, want.At = "", time.Time{}, "", time.Time{}
	return got == want
}
