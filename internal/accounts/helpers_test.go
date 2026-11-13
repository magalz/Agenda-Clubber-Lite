package accounts

import (
	"context"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/store/mongotest"
)

// testNow is the fixed clock every accounts test starts from.
var testNow = time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)

// newTestStore returns an isolated, empty store on MONGO_TEST_URI. It skips
// locally and fails in CI when the variable is unset (see mongotest).
func newTestStore(t *testing.T) domain.Store {
	t.Helper()
	return mongotest.New(t)
}

// insertActiveUser stores an active user with a verified email and returns it
// with its ID. mutate adjusts the fixture before it is stored, for example to
// set PasswordHash, Status or IsAdmin.
func insertActiveUser(t *testing.T, s domain.Store, email string, mutate ...func(*domain.User)) domain.User {
	t.Helper()
	verified := testNow
	u := domain.User{
		Name:              "Pessoa Teste",
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
	if err := s.Repos().Users.Create(context.Background(), &u); err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
	return u
}
