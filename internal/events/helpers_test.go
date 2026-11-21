package events

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
)

// Fixtures and readers shared by every test in the package. A later task adds
// its own fixtures in its own file, with the prefix of its flow (input…,
// write…, read…, calendar…), so tasks written in parallel do not collide.

// testNow is the fixed clock every events test starts from: Saturday
// 2026-11-14, 13:00 in Fortaleza. A test that needs another instant adds to it.
var testNow = time.Date(2026, 11, 14, 16, 0, 0, 0, time.UTC)

// testLoc is the zone dates are read and shown in (APP_TZ's default).
var testLoc = mustLoadLocation("America/Fortaleza")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

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
		Phone:             "85987654321",
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

// insertCollective stores an approved collective of kind coletivo created by
// createdBy (a user ID, or empty). mutate adjusts it before it is stored, for
// example to set Status or EventsBlocked.
func insertCollective(t *testing.T, s domain.Store, name, createdBy string, mutate ...func(*domain.Collective)) domain.Collective {
	t.Helper()
	approvedAt := testNow
	c := domain.Collective{
		Name:       name,
		NameKey:    domain.NameKey(name),
		Kind:       domain.CollectiveKindColetivo,
		Status:     domain.CollectiveStatusApproved,
		CreatedBy:  createdBy,
		ApprovedAt: &approvedAt,
		CreatedAt:  testNow,
		UpdatedAt:  testNow,
	}
	for _, f := range mutate {
		f(&c)
	}
	if err := s.Repos().Collectives.Create(t.Context(), &c); err != nil {
		t.Fatalf("insert collective %q: %v", name, err)
	}
	return c
}

// insertMember stores an approved membership of the user in the collective with
// the given role, both approval sides set. mutate adjusts it before it is
// stored, for example to leave it pending.
func insertMember(t *testing.T, s domain.Store, userID, collectiveID string, role domain.Role, mutate ...func(*domain.Membership)) domain.Membership {
	t.Helper()
	decided := testNow
	m := domain.Membership{
		UserID:          userID,
		CollectiveID:    collectiveID,
		Role:            role,
		Status:          domain.MembershipStatusApproved,
		AdminApprovedAt: &decided,
		OwnerApprovedAt: &decided,
		RequestedAt:     testNow,
		DecidedAt:       &decided,
	}
	for _, f := range mutate {
		f(&m)
	}
	if err := s.Repos().Memberships.Create(t.Context(), m); err != nil {
		t.Fatalf("insert membership of %s in %s: %v", userID, collectiveID, err)
	}
	return m
}

// insertEvent stores a valid event of the collective created by createdBy and
// returns it with its ID and version. By default it is a Festa in Fortaleza/CE
// at testNow + 1 day, 22:00 in Fortaleza, for six hours, R$ 25,00, style
// techno. mutate adjusts it before it is stored.
func insertEvent(t *testing.T, s domain.Store, collectiveID, createdBy string, mutate ...func(*domain.Event)) domain.Event {
	t.Helper()
	e := domain.Event{
		CollectiveID: collectiveID,
		Name:         "Noite de Teste",
		Type:         domain.EventTypeFesta,
		StartsAt:     time.Date(2026, 11, 16, 1, 0, 0, 0, time.UTC), // Sunday 15/11, 22:00 in Fortaleza
		DurationMin:  360,
		PriceCents:   2500,
		Style:        "techno",
		State:        "CE",
		City:         "Fortaleza",
		CreatedBy:    createdBy,
		UpdatedBy:    createdBy,
		CreatedAt:    testNow,
		UpdatedAt:    testNow,
	}
	for _, f := range mutate {
		f(&e)
	}
	if err := s.Repos().Events.Create(t.Context(), &e); err != nil {
		t.Fatalf("insert event %q: %v", e.Name, err)
	}
	return e
}

// world is the cast most events tests need, stored in a fresh store:
//   - Admin: the site admin, with no membership.
//   - Owner: Administrador (approved) of Alfa and of Delta.
//   - Member: Membro (approved) of Alfa.
//   - Outsider: an active account with no membership.
//   - Alfa: an approved collective created by Owner.
//   - Delta: an approved collective created by Owner, with its events blocked.
type world struct {
	S                              domain.Store
	Admin, Owner, Member, Outsider domain.User
	Alfa, Delta                    domain.Collective
}

// newWorld stores the cast described on world.
func newWorld(t *testing.T) world {
	t.Helper()
	s := newTestStore(t)
	w := world{S: s}
	w.Admin = insertUser(t, s, "admin@agenda.test", func(u *domain.User) { u.IsAdmin = true })
	w.Owner = insertUser(t, s, "dono@agenda.test")
	w.Member = insertUser(t, s, "membro@agenda.test")
	w.Outsider = insertUser(t, s, "fora@agenda.test")
	w.Alfa = insertCollective(t, s, "Coletivo Alfa", w.Owner.ID)
	w.Delta = insertCollective(t, s, "Coletivo Delta", w.Owner.ID, func(c *domain.Collective) { c.EventsBlocked = true })
	insertMember(t, s, w.Owner.ID, w.Alfa.ID, domain.RoleOwner)
	insertMember(t, s, w.Owner.ID, w.Delta.ID, domain.RoleOwner)
	insertMember(t, s, w.Member.ID, w.Alfa.ID, domain.RoleMember)
	return w
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

// requireErrorIs fails the test unless err matches want.
func requireErrorIs(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
}

// requireFieldErrors fails the test unless err is a domain.FieldErrors with
// exactly the given keys.
func requireFieldErrors(t *testing.T, err error, keys ...string) domain.FieldErrors {
	t.Helper()
	var fe domain.FieldErrors
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v, want a domain.FieldErrors", err)
	}
	if len(fe) != len(keys) {
		t.Fatalf("field errors %v, want exactly the keys %v", fe, keys)
	}
	for _, k := range keys {
		if _, ok := fe[k]; !ok {
			t.Fatalf("field errors %v, want the key %q", fe, k)
		}
	}
	return fe
}
