package accounts

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const (
	loginEmail    = "ana@agenda.test"
	loginPassword = "Senha-Certa-1"
	loginWrong    = "Senha-Errada-2"
)

// loginHash is the argon2id hash of loginPassword, built once: hashing is slow
// on purpose.
var loginHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword(loginPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// loginSentinels are every outcome of Login the person sees.
var loginSentinels = []error{ErrLoginFailed, ErrLoginLocked, ErrLoginPendingEmail, ErrLoginPendingApproval, ErrLoginDisabled}

// insertLoginUser stores a user whose password is loginPassword.
func insertLoginUser(t *testing.T, s domain.Store, mutate ...func(*domain.User)) domain.User {
	t.Helper()
	withHash := func(u *domain.User) { u.PasswordHash = loginHash() }
	return insertActiveUser(t, s, loginEmail, append([]func(*domain.User){withHash}, mutate...)...)
}

func loginReload(t *testing.T, s domain.Store, id string) domain.User {
	t.Helper()
	u, err := s.Repos().Users.ByID(context.Background(), id)
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	return u
}

// loginIs fails unless err is want and none of the other sentinels.
func loginIs(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Errorf("%s: err = %v, want %v", what, err, want)
		return
	}
	for _, other := range loginSentinels {
		if other != want && errors.Is(err, other) {
			t.Errorf("%s: err = %v also matches %v", what, err, other)
		}
	}
}

func TestLoginActiveUser(t *testing.T) {
	s := newTestStore(t)
	u := insertLoginUser(t, s, func(u *domain.User) { u.FailedLogins = 3 })

	got, err := Login(context.Background(), s, loginEmail, loginPassword, testNow)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if got.ID != u.ID || got.Status != domain.UserStatusActive {
		t.Errorf("user = %+v, want the active user %s", got, u.ID)
	}
	if n := loginReload(t, s, u.ID).FailedLogins; n != 0 {
		t.Errorf("FailedLogins after a login = %d, want 0", n)
	}
}

func TestLoginNormalizesTheEmail(t *testing.T) {
	s := newTestStore(t)
	u := insertLoginUser(t, s)

	got, err := Login(context.Background(), s, "  ANA@Agenda.Test ", loginPassword, testNow)
	if err != nil || got.ID != u.ID {
		t.Fatalf("Login = %+v, %v; want the user", got, err)
	}
}

func TestLoginUnknownOrMalformedEmail(t *testing.T) {
	s := newTestStore(t)
	insertLoginUser(t, s)

	for _, email := range []string{"nobody@agenda.test", "not-an-email", "", "  ", "ana@agenda.test, x@y.zz"} {
		got, err := Login(context.Background(), s, email, loginPassword, testNow)
		loginIs(t, "email "+email, err, ErrLoginFailed)
		if got.ID != "" {
			t.Errorf("email %q: returned a user %+v", email, got)
		}
	}
}

func TestLoginLocksAfterFiveWrongPasswords(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	u := insertLoginUser(t, s)

	for i := 1; i < LoginMaxFailures; i++ {
		_, err := Login(ctx, s, loginEmail, loginWrong, testNow)
		loginIs(t, "wrong password", err, ErrLoginFailed)
		if n := loginReload(t, s, u.ID).FailedLogins; n != i {
			t.Fatalf("FailedLogins after %d wrong passwords = %d", i, n)
		}
	}
	_, err := Login(ctx, s, loginEmail, loginWrong, testNow)
	loginIs(t, "the fifth wrong password", err, ErrLoginLocked)

	locked := loginReload(t, s, u.ID)
	until := testNow.Add(LoginLockFor)
	if locked.LockedUntil == nil || !locked.LockedUntil.Equal(until) {
		t.Fatalf("LockedUntil = %v, want %v", locked.LockedUntil, until)
	}

	// The right password does not get through a lock, and a wrong one does not
	// extend it.
	_, err = Login(ctx, s, loginEmail, loginPassword, testNow)
	loginIs(t, "right password while locked", err, ErrLoginLocked)
	_, err = Login(ctx, s, loginEmail, loginWrong, testNow.Add(time.Minute))
	loginIs(t, "wrong password while locked", err, ErrLoginLocked)
	if after := loginReload(t, s, u.ID); after.FailedLogins != 0 || !after.LockedUntil.Equal(until) {
		t.Errorf("a refused attempt changed the lock: FailedLogins %d, LockedUntil %v", after.FailedLogins, after.LockedUntil)
	}

	_, err = Login(ctx, s, loginEmail, loginPassword, until.Add(-time.Millisecond))
	loginIs(t, "right password 1ms before the lock ends", err, ErrLoginLocked)
	got, err := Login(ctx, s, loginEmail, loginPassword, until)
	if err != nil || got.ID != u.ID {
		t.Errorf("right password when the lock ends: %+v, %v; want the user", got, err)
	}
}

func TestLoginRevealsStatusOnlyWithTheRightPassword(t *testing.T) {
	for _, tc := range []struct {
		status domain.UserStatus
		want   error
	}{
		{domain.UserStatusPendingEmail, ErrLoginPendingEmail},
		{domain.UserStatusPendingApproval, ErrLoginPendingApproval},
		{domain.UserStatusDisabled, ErrLoginDisabled},
	} {
		t.Run(string(tc.status), func(t *testing.T) {
			s := newTestStore(t)
			u := insertLoginUser(t, s, func(u *domain.User) { u.Status = tc.status })

			_, err := Login(context.Background(), s, loginEmail, loginPassword, testNow)
			loginIs(t, "right password", err, tc.want)

			_, err = Login(context.Background(), s, loginEmail, loginWrong, testNow)
			loginIs(t, "wrong password", err, ErrLoginFailed)
			if n := loginReload(t, s, u.ID).FailedLogins; n != 1 {
				t.Errorf("the wrong password counted %d failures, want 1", n)
			}
		})
	}
}

func TestLoginMalformedStoredHashIsAServerError(t *testing.T) {
	s := newTestStore(t)
	u := insertActiveUser(t, s, loginEmail) // "placeholder-hash" is not a PHC string

	_, err := Login(context.Background(), s, loginEmail, loginPassword, testNow)
	if !errors.Is(err, auth.ErrInvalidHash) {
		t.Fatalf("err = %v, want it to wrap auth.ErrInvalidHash", err)
	}
	for _, sentinel := range loginSentinels {
		if errors.Is(err, sentinel) {
			t.Errorf("err matches the sentinel %v; a broken hash must be a server error", sentinel)
		}
	}
	if strings.Contains(err.Error(), loginEmail) || strings.Contains(err.Error(), loginPassword) {
		t.Errorf("the error repeats the email or the password: %v", err)
	}
	if n := loginReload(t, s, u.ID).FailedLogins; n != 0 {
		t.Errorf("a broken hash counted %d failures against the person, want 0", n)
	}
}

// loginUsersFailing is a Users whose ByEmail fails; any other method panics.
type loginUsersFailing struct {
	domain.Users
	err error
}

func (f loginUsersFailing) ByEmail(context.Context, string) (domain.User, error) {
	return domain.User{}, f.err
}

type loginStoreFailing struct {
	domain.Store
	users domain.Users
}

func (f loginStoreFailing) Repos() domain.Repos { return domain.Repos{Users: f.users} }

func TestLoginStoreFailureIsAServerError(t *testing.T) {
	boom := errors.New("connection reset")
	s := loginStoreFailing{users: loginUsersFailing{err: boom}}

	_, err := Login(context.Background(), s, loginEmail, loginPassword, testNow)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the store error", err)
	}
	for _, sentinel := range loginSentinels {
		if errors.Is(err, sentinel) {
			t.Errorf("a store failure reads as %v", sentinel)
		}
	}
}
