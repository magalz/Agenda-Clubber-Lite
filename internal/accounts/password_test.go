package accounts

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const passwordMsgWrongCurrent = "senha atual incorreta"

// passwordAt is when the person submits the form: a day after the fixtures were
// made, so a PasswordChangedAt taken from now is not the fixture's.
var passwordAt = testNow.Add(24 * time.Hour)

// passwordCountHashes counts the calls to the function that hashes the new
// password until the test ends.
func passwordCountHashes(t *testing.T) *int {
	t.Helper()
	var calls int
	orig := passwordHashPassword
	passwordHashPassword = func(password string) (string, error) {
		calls++
		return orig(password)
	}
	t.Cleanup(func() { passwordHashPassword = orig })
	return &calls
}

// passwordStart opens a session for the user and returns its raw cookie value.
func passwordStart(t *testing.T, s domain.Store, userID string) string {
	t.Helper()
	raw, _, err := auth.StartSession(t.Context(), s.Repos().Sessions, userID, "test-agent", testNow)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return raw
}

// passwordAlive reports whether the session behind the cookie value still exists.
func passwordAlive(t *testing.T, s domain.Store, cookie string) bool {
	t.Helper()
	hash, err := auth.HashToken(cookie)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}
	_, err = s.Repos().Sessions.Get(t.Context(), hash)
	switch {
	case err == nil:
		return true
	case errors.Is(err, domain.ErrNotFound):
		return false
	}
	t.Fatalf("Sessions.Get: %v", err)
	return false
}

// passwordAssertUntouched asserts that a refused change left the password
// history of a resetUser and the given sessions as they were.
func passwordAssertUntouched(t *testing.T, s domain.Store, userID string, sessions ...string) {
	t.Helper()
	if got, want := resetHashes(t, s, userID), []string{loginHash(), resetPrevHash()}; !slices.Equal(got, want) {
		t.Error("the refused change altered the password history")
	}
	for i, cookie := range sessions {
		if !passwordAlive(t, s, cookie) {
			t.Errorf("the refused change ended session %d", i)
		}
	}
}

// passwordTxFailsStore is a store whose transactions fail, as a database that
// went down after the account was read.
type passwordTxFailsStore struct{ domain.Store }

func (passwordTxFailsStore) WithTx(context.Context, func(context.Context, domain.Repos) error) error {
	return errors.New("the database is down")
}

func TestChangePasswordSetsThePasswordAndEndsTheOtherSessions(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	u := resetUser(t, s)
	other := insertActiveUser(t, s, "outra@agenda.test")
	keep := passwordStart(t, s, u.ID)
	extra := passwordStart(t, s, u.ID)
	theirs := passwordStart(t, s, other.ID)
	calls := passwordCountHashes(t)

	// The account was locked by wrong passwords and has one more failure on the
	// books: the change must clear both. Without this seed the assertions after
	// the change would pass on a fixture that starts clean.
	for range LoginMaxFailures + 1 {
		if _, err := s.Repos().Users.RecordLoginFailure(ctx, u.ID, passwordAt, LoginMaxFailures, LoginLockFor); err != nil {
			t.Fatalf("RecordLoginFailure: %v", err)
		}
	}
	if before := loginReload(t, s, u.ID); before.FailedLogins != 1 || before.LockedUntil == nil {
		t.Fatalf("seed: FailedLogins %d, LockedUntil %v, want 1 and a lock", before.FailedLogins, before.LockedUntil)
	}

	if err := ChangePassword(ctx, s, u.ID, keep, loginPassword, resetNewPassword, resetNewPassword, passwordAt); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if *calls != 1 {
		t.Errorf("the new password was hashed %d times, want 1", *calls)
	}

	got := loginReload(t, s, u.ID)
	if ok, err := auth.VerifyPassword(got.PasswordHash, resetNewPassword); err != nil || !ok {
		t.Errorf("the stored hash does not verify the new password: ok %v, err %v", ok, err)
	}
	if got.FailedLogins != 0 || got.LockedUntil != nil {
		t.Errorf("FailedLogins %d, LockedUntil %v after the change, want 0 and no lock", got.FailedLogins, got.LockedUntil)
	}
	if !got.PasswordChangedAt.Equal(passwordAt.Truncate(time.Millisecond)) {
		t.Errorf("PasswordChangedAt = %v, want %v", got.PasswordChangedAt, passwordAt)
	}
	if hashes := resetHashes(t, s, u.ID); len(hashes) != 2 || hashes[0] != got.PasswordHash || hashes[1] != loginHash() {
		t.Errorf("history = %v, want the new hash then the old one", hashes)
	}

	if !passwordAlive(t, s, keep) {
		t.Error("the session that made the change ended")
	}
	if passwordAlive(t, s, extra) {
		t.Error("another session of the same person survived the change")
	}
	if !passwordAlive(t, s, theirs) {
		t.Error("someone else's session ended")
	}
	if _, err := Login(ctx, s, resetEmail, resetNewPassword, passwordAt); err != nil {
		t.Errorf("login with the new password: %v", err)
	}
	// Last, because a failed login counts against the account.
	_, err := Login(ctx, s, resetEmail, loginPassword, passwordAt)
	loginIs(t, "login with the old password", err, ErrLoginFailed)
}

func TestChangePasswordFieldErrors(t *testing.T) {
	notEmail := "requisitos da senha: " + auth.RuleLabels[auth.RuleNotEmail]
	tests := []struct {
		name                       string
		current, password, confirm string
		// What each message contains; "" for no message on that field.
		wantCurrent, wantPassword, wantConfirm string
	}{
		{"nothing typed", "", "", "", domain.MsgRequired, domain.MsgRequired, ""},
		{"no current password", "", resetNewPassword, resetNewPassword, domain.MsgRequired, "", ""},
		{"no new password", loginPassword, "", "", "", domain.MsgRequired, ""},
		{"not valid UTF-8", loginPassword, "Senha-Forte-1\xff", "Senha-Forte-1\xff", "", domain.MsgInvalid, ""},
		{"too weak", loginPassword, "abc", "abc", "", "requisitos da senha: " + auth.RuleLabels[auth.RuleMinLength], ""},
		{"no match", loginPassword, resetNewPassword, resetNewPassword + "x", "", "", resetMsgMismatch},
		{"every field wrong", "", "abc", "abd", domain.MsgRequired, "requisitos da senha: ", resetMsgMismatch},
		{"the local part of the e-mail", loginPassword, "Ana.Souza1", "Ana.Souza1", "", notEmail, ""},
		{"the whole e-mail", loginPassword, "Ana.Souza1@Agenda.Test", "Ana.Souza1@Agenda.Test", "", notEmail, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			u := resetUser(t, s)
			session := passwordStart(t, s, u.ID)
			calls := passwordCountHashes(t)

			err := ChangePassword(t.Context(), s, u.ID, session, tc.current, tc.password, tc.confirm, passwordAt)
			fe := signupFieldErrors(t, err)
			var keys []string
			for key, want := range map[string]string{"current_password": tc.wantCurrent, "password": tc.wantPassword, "password_confirm": tc.wantConfirm} {
				if want == "" {
					continue
				}
				keys = append(keys, key)
				if !strings.Contains(fe[key], want) {
					t.Errorf("%s message %q, want it to contain %q", key, fe[key], want)
				}
			}
			signupAssertKeys(t, fe, keys...)
			if *calls != 0 {
				t.Errorf("the new password was hashed %d times for a form with field errors", *calls)
			}
			passwordAssertUntouched(t, s, u.ID, session)
		})
	}
}

// The field checks come before any argon2 work: a stored hash that cannot be
// verified (it would be a server error) never gets that far.
func TestChangePasswordChecksTheFieldsBeforeTouchingAnyHash(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	u := insertActiveUser(t, s, resetEmail) // PasswordHash is a placeholder no verify can read
	calls := passwordCountHashes(t)

	err := ChangePassword(ctx, s, u.ID, "", loginPassword, "abc", "abd", passwordAt)
	signupAssertKeys(t, signupFieldErrors(t, err), "password", "password_confirm")

	// With every field right, the unreadable stored hash is the server's problem.
	err = ChangePassword(ctx, s, u.ID, "", loginPassword, resetNewPassword, resetNewPassword, passwordAt)
	resetAssertServerError(t, "unreadable stored hash", err)
	if *calls != 0 {
		t.Errorf("the new password was hashed %d times, want 0", *calls)
	}
}

func TestChangePasswordWrongCurrentPassword(t *testing.T) {
	// The history is not looked at while the current password is wrong, so a
	// reused new password adds no second message.
	for _, password := range []string{resetNewPassword, loginPassword, resetPrevPassword} {
		t.Run(password, func(t *testing.T) {
			s := newTestStore(t)
			u := resetUser(t, s)
			session := passwordStart(t, s, u.ID)
			calls := passwordCountHashes(t)

			err := ChangePassword(t.Context(), s, u.ID, session, loginWrong, password, password, passwordAt)
			fe := signupFieldErrors(t, err)
			signupAssertKeys(t, fe, "current_password")
			if fe["current_password"] != passwordMsgWrongCurrent {
				t.Errorf("current_password message %q, want %q", fe["current_password"], passwordMsgWrongCurrent)
			}
			if *calls != 0 {
				t.Errorf("the new password was hashed %d times for a wrong current password", *calls)
			}
			passwordAssertUntouched(t, s, u.ID, session)
		})
	}
}

func TestChangePasswordRefusesAPasswordUsedLately(t *testing.T) {
	for name, password := range map[string]string{"the current password": loginPassword, "the previous password": resetPrevPassword} {
		t.Run(name, func(t *testing.T) {
			s := newTestStore(t)
			u := resetUser(t, s)
			session := passwordStart(t, s, u.ID)

			err := ChangePassword(t.Context(), s, u.ID, session, loginPassword, password, password, passwordAt)
			fe := signupFieldErrors(t, err)
			signupAssertKeys(t, fe, "password")
			if fe["password"] != resetMsgHistory {
				t.Errorf("password message %q, want %q", fe["password"], resetMsgHistory)
			}
			passwordAssertUntouched(t, s, u.ID, session)
		})
	}
}

func TestChangePasswordUnreadableHistoryIsAServerError(t *testing.T) {
	// A previous hash that cannot be read must never read as "no match". (An
	// unreadable current hash fails the check of the current password instead.)
	s := newTestStore(t)
	ctx := t.Context()
	u := insertActiveUser(t, s, resetEmail) // its placeholder hash becomes the previous one
	if err := s.Repos().Users.ChangePassword(ctx, u.ID, loginHash(), testNow); err != nil {
		t.Fatalf("change password: %v", err)
	}
	before := resetHashes(t, s, u.ID)
	session := passwordStart(t, s, u.ID)

	err := ChangePassword(ctx, s, u.ID, session, loginPassword, resetNewPassword, resetNewPassword, passwordAt)
	resetAssertServerError(t, "ChangePassword", err)
	if got := resetHashes(t, s, u.ID); !slices.Equal(got, before) {
		t.Errorf("history %v after the failed change, want %v", got, before)
	}
	if ok, err := auth.VerifyPassword(loginReload(t, s, u.ID).PasswordHash, loginPassword); err != nil || !ok {
		t.Errorf("the old password no longer works: ok %v, err %v", ok, err)
	}
	if !passwordAlive(t, s, session) {
		t.Error("the failed change ended the session")
	}
}

func TestChangePasswordMalformedKeepSessionKeepsNone(t *testing.T) {
	s := newTestStore(t)
	u := resetUser(t, s)
	other := insertActiveUser(t, s, "outra@agenda.test")
	mine := []string{passwordStart(t, s, u.ID), passwordStart(t, s, u.ID)}
	theirs := passwordStart(t, s, other.ID)

	if err := ChangePassword(t.Context(), s, u.ID, "garbage", loginPassword, resetNewPassword, resetNewPassword, passwordAt); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	for i, cookie := range mine {
		if passwordAlive(t, s, cookie) {
			t.Errorf("session %d survived a change with no session to keep", i)
		}
	}
	if !passwordAlive(t, s, theirs) {
		t.Error("someone else's session ended")
	}
}

func TestChangePasswordHashFailureChangesNothing(t *testing.T) {
	s := newTestStore(t)
	u := resetUser(t, s)
	session := passwordStart(t, s, u.ID)
	orig := passwordHashPassword
	passwordHashPassword = func(string) (string, error) { return "", errors.New("no entropy") }
	t.Cleanup(func() { passwordHashPassword = orig })

	err := ChangePassword(t.Context(), s, u.ID, session, loginPassword, resetNewPassword, resetNewPassword, passwordAt)
	resetAssertServerError(t, "hash failure", err)
	passwordAssertUntouched(t, s, u.ID, session)
}

func TestChangePasswordRefusesAnAccountThatCannotChange(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()

	resetAssertServerError(t, "unknown account", ChangePassword(ctx, s, "000000000000000000000000", "", loginPassword, resetNewPassword, resetNewPassword, passwordAt))

	for _, status := range []domain.UserStatus{domain.UserStatusPendingEmail, domain.UserStatusPendingApproval, domain.UserStatusDisabled} {
		t.Run(string(status), func(t *testing.T) {
			s := newTestStore(t)
			u := insertLoginUser(t, s, func(u *domain.User) { u.Status = status })

			err := ChangePassword(t.Context(), s, u.ID, "", loginPassword, resetNewPassword, resetNewPassword, passwordAt)
			resetAssertServerError(t, "account that is not active", err)
			if got := loginReload(t, s, u.ID).PasswordHash; got != loginHash() {
				t.Error("the password of an account that is not active changed")
			}
		})
	}
}

func TestChangePasswordStoreFailureIsAServerError(t *testing.T) {
	s := newTestStore(t)
	u := resetUser(t, s)

	err := ChangePassword(t.Context(), passwordTxFailsStore{s}, u.ID, "", loginPassword, resetNewPassword, resetNewPassword, passwordAt)
	resetAssertServerError(t, "failing transaction", err)
	passwordAssertUntouched(t, s, u.ID)
}

func TestPasswordReused(t *testing.T) {
	s := newTestStore(t)
	u := resetUser(t, s)
	for _, tc := range []struct {
		name, password string
		want           bool
	}{
		{"the current password", loginPassword, true},
		{"the previous password", resetPrevPassword, true},
		{"a password nobody used", resetNewPassword, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := PasswordReused(t.Context(), s, u.ID, tc.password)
			if err != nil || got != tc.want {
				t.Errorf("PasswordReused = %v, %v; want %v, nil", got, err, tc.want)
			}
		})
	}
}

func TestPasswordReusedUnreadableHistoryIsAnError(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	broken := insertActiveUser(t, s, resetEmail) // a placeholder hash no verify can read

	if used, err := PasswordReused(ctx, s, broken.ID, resetNewPassword); err == nil || used {
		t.Errorf("unreadable hash: PasswordReused = %v, %v; want false and an error", used, err)
	}
	if used, err := PasswordReused(ctx, s, "000000000000000000000000", resetNewPassword); err == nil || used {
		t.Errorf("unknown account: PasswordReused = %v, %v; want false and an error", used, err)
	}
}

func TestPasswordPolicyMessage(t *testing.T) {
	for _, tc := range []struct {
		name     string
		violated []auth.Rule
		want     string
	}{
		{"one rule", []auth.Rule{auth.RuleMinLength}, "requisitos da senha: 8 caracteres ou mais"},
		{"the email rule", []auth.Rule{auth.RuleNotEmail}, "requisitos da senha: diferente do seu e-mail"},
		{"several rules keep their order", []auth.Rule{auth.RuleMinLength, auth.RuleUpper, auth.RuleDigit, auth.RuleSymbol},
			"requisitos da senha: 8 caracteres ou mais, uma letra maiúscula, um número, um símbolo"},
		{"the rules an empty password breaks", auth.CheckPolicy("", "a@b.co"),
			"requisitos da senha: 8 caracteres ou mais, uma letra minúscula, uma letra maiúscula, um número, um símbolo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := PasswordPolicyMessage(tc.violated); got != tc.want {
				t.Errorf("PasswordPolicyMessage(%v) = %q, want %q", tc.violated, got, tc.want)
			}
		})
	}
}

// passwordFieldErrors is the one check signup, reset and change share before
// they touch the store or a hash.
func TestPasswordFieldErrors(t *testing.T) {
	const good = "Senha#Forte1"
	for _, tc := range []struct {
		name                      string
		password, confirm, email  string
		wantPassword, wantConfirm string // "" means no error under that key
	}{
		{"a good password", good, good, "ana@example.com", "", ""},
		{"empty", "", "", "ana@example.com", domain.MsgRequired, ""},
		{"empty with a confirmation", "", good, "", domain.MsgRequired, signupMsgPasswordMismatch},
		{"not UTF-8", good + "\xff", good + "\xff", "", domain.MsgInvalid, ""},
		{"weak", "abc", "abc", "", PasswordPolicyMessage(auth.CheckPolicy("abc", "")), ""},
		{"equal to the email", "Ana1@Example.com", "Ana1@Example.com", "ana1@example.com", PasswordPolicyMessage([]auth.Rule{auth.RuleNotEmail}), ""},
		{"an empty email skips that rule", "Ana1@Example.com", "Ana1@Example.com", "", "", ""},
		{"confirmation differs", good, good + "2", "", "", signupMsgPasswordMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := domain.FieldErrors{"current_password": "kept"} // an earlier error stays
			passwordFieldErrors(&got, tc.password, tc.confirm, tc.email)
			want := domain.FieldErrors{"current_password": "kept"}
			if tc.wantPassword != "" {
				want["password"] = tc.wantPassword
			}
			if tc.wantConfirm != "" {
				want["password_confirm"] = tc.wantConfirm
			}
			if !maps.Equal(got, want) {
				t.Errorf("field errors %v, want %v", got, want)
			}
		})
	}
}
