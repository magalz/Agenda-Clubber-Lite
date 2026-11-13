package accounts

import (
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const (
	// resetEmail has a local part ("ana.souza1") that a password can match.
	resetEmail = "ana.souza1@agenda.test"
	// resetNewPassword and resetOtherPassword pass the policy and are in nobody's
	// history.
	resetNewPassword   = "Nova-Senha-Forte-7"
	resetOtherPassword = "Outra-Senha-Forte-9"
	// resetPrevPassword is the password the account had before loginPassword.
	resetPrevPassword = "Senha-Antiga-2"

	resetMsgHistory  = "use uma senha diferente das 2 últimas"
	resetMsgMismatch = "as senhas não conferem"
)

// resetAt is when the person follows the link: ten minutes after asking for it.
var resetAt = testNow.Add(10 * time.Minute)

// resetPrevHash is the argon2id hash of resetPrevPassword, built once: hashing
// is slow on purpose.
var resetPrevHash = sync.OnceValue(func() string {
	h, err := auth.HashPassword(resetPrevPassword)
	if err != nil {
		panic(err)
	}
	return h
})

// resetUser stores an active account (resetEmail) whose password is
// loginPassword and whose previous one was resetPrevPassword, so both are in its
// history. mutate adjusts the fixture before it is stored.
func resetUser(t *testing.T, s domain.Store, mutate ...func(*domain.User)) domain.User {
	t.Helper()
	withPrev := func(u *domain.User) { u.PasswordHash = resetPrevHash() }
	u := insertActiveUser(t, s, resetEmail, append([]func(*domain.User){withPrev}, mutate...)...)
	if err := s.Repos().Users.ChangePassword(t.Context(), u.ID, loginHash(), testNow); err != nil {
		t.Fatalf("change password: %v", err)
	}
	return u
}

// resetToken stores a reset token for the user, issued at the given time, and
// returns its raw form (what the link carries).
func resetToken(t *testing.T, s domain.Store, userID string, issued time.Time) string {
	t.Helper()
	raw, hash, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	tok := domain.Token{UserID: userID, Purpose: domain.TokenPurposeResetPassword, Hash: hash, ExpiresAt: issued.Add(ResetTokenTTL), CreatedAt: issued}
	if err := s.Repos().Tokens.Create(t.Context(), &tok); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return raw
}

// resetLinkToken returns the token in the reset link of an email text.
func resetLinkToken(t *testing.T, text string) string {
	t.Helper()
	prefix := signupOrigin + "/senha/redefinir?token="
	i := strings.Index(text, prefix)
	if i < 0 {
		t.Fatalf("email text has no %q link:\n%s", prefix, text)
	}
	raw := text[i+len(prefix):]
	if end := strings.IndexAny(raw, " \t\r\n"); end >= 0 {
		raw = raw[:end]
	}
	return raw
}

// resetHashes is the password history the store holds for the user, newest first.
func resetHashes(t *testing.T, s domain.Store, userID string) []string {
	t.Helper()
	got, err := s.Repos().Users.RecentPasswordHashes(t.Context(), userID)
	if err != nil {
		t.Fatalf("RecentPasswordHashes: %v", err)
	}
	return got
}

// resetAssertUnspent asserts that the token still works, which spends it.
func resetAssertUnspent(t *testing.T, s domain.Store, raw string, at time.Time) {
	t.Helper()
	hash, err := auth.HashToken(raw)
	if err != nil {
		t.Fatalf("HashToken: %v", err)
	}
	if _, err := s.Repos().Tokens.Consume(t.Context(), domain.TokenPurposeResetPassword, hash, at); err != nil {
		t.Errorf("the token was spent by the refused reset: %v", err)
	}
}

// resetAssertServerError asserts that err is neither a link refusal nor a field
// error: the caller shows the person nothing but "erro interno" for it.
func resetAssertServerError(t *testing.T, what string, err error) {
	t.Helper()
	var fe domain.FieldErrors
	switch {
	case err == nil:
		t.Errorf("%s: err = nil, want a server error", what)
	case errors.Is(err, ErrResetInvalid) || errors.As(err, &fe):
		t.Errorf("%s: err = %v, want a server error, not a refusal the person sees", what, err)
	}
}

func TestRequestPasswordResetMailsALinkThatResetsThePassword(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	u := insertLoginUser(t, s)

	// The address is read the way login reads it, however it is spelled.
	if err := RequestPasswordReset(ctx, s, signupOrigin, " ANA@Agenda.Test ", testNow); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	msgs := confirmOutbox(t, s, testNow)
	if len(msgs) != 1 {
		t.Fatalf("outbox holds %d messages, want 1: %+v", len(msgs), msgs)
	}
	m := msgs[0]
	if m.To != loginEmail || !strings.Contains(m.Subject, "senha") || !strings.Contains(m.Text, u.Name) {
		t.Errorf("message to %q, subject %q, text %q; want the password reset email for %s", m.To, m.Subject, m.Text, loginEmail)
	}
	raw := resetLinkToken(t, m.Text)

	if err := ResetPassword(ctx, s, raw, resetNewPassword, resetNewPassword, resetAt); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if _, err := Login(ctx, s, loginEmail, resetNewPassword, resetAt); err != nil {
		t.Errorf("login with the new password: %v", err)
	}
	if _, err := Login(ctx, s, loginEmail, loginPassword, resetAt); !errors.Is(err, ErrLoginFailed) {
		t.Errorf("login with the old password: err = %v, want ErrLoginFailed", err)
	}
}

func TestResetLinkLivesForOneHour(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	insertLoginUser(t, s)
	if err := RequestPasswordReset(ctx, s, signupOrigin, loginEmail, testNow); err != nil {
		t.Fatalf("RequestPasswordReset: %v", err)
	}
	msgs := confirmOutbox(t, s, testNow)
	if len(msgs) != 1 {
		t.Fatalf("outbox holds %d messages, want 1", len(msgs))
	}
	raw := resetLinkToken(t, msgs[0].Text)

	if err := ResetPassword(ctx, s, raw, resetNewPassword, resetNewPassword, testNow.Add(ResetTokenTTL)); !errors.Is(err, ErrResetInvalid) {
		t.Errorf("at the expiry instant: err = %v, want ErrResetInvalid", err)
	}
	if err := ResetPassword(ctx, s, raw, resetNewPassword, resetNewPassword, testNow.Add(ResetTokenTTL-time.Millisecond)); err != nil {
		t.Errorf("a millisecond before the expiry: %v", err)
	}
}

func TestRequestPasswordResetIgnoresEveryOtherAddress(t *testing.T) {
	s := newTestStore(t)
	confirmPendingUser(t, s, "pendente@agenda.test")
	insertActiveUser(t, s, "analise@agenda.test", func(u *domain.User) { u.Status = domain.UserStatusPendingApproval })
	insertActiveUser(t, s, "off@agenda.test", func(u *domain.User) { u.Status = domain.UserStatusDisabled })

	for _, email := range []string{"pendente@agenda.test", "analise@agenda.test", "off@agenda.test", "ninguem@agenda.test", "not an address", ""} {
		if err := RequestPasswordReset(t.Context(), s, signupOrigin, email, testNow); err != nil {
			t.Errorf("RequestPasswordReset(%q) = %v, want nil", email, err)
		}
	}
	signupAssertNothingWritten(t, s, "ninguem@agenda.test", "")
}

func TestRequestPasswordResetRetiresTheEarlierLink(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	insertLoginUser(t, s)
	for _, at := range []time.Time{testNow, testNow.Add(time.Minute)} {
		if err := RequestPasswordReset(ctx, s, signupOrigin, loginEmail, at); err != nil {
			t.Fatalf("RequestPasswordReset at %v: %v", at, err)
		}
	}
	msgs := confirmOutbox(t, s, signupFarFuture)
	if len(msgs) != 2 {
		t.Fatalf("outbox holds %d messages, want 2", len(msgs))
	}
	slices.SortFunc(msgs, func(a, b domain.OutboxMessage) int { return a.CreatedAt.Compare(b.CreatedAt) })
	first, second := resetLinkToken(t, msgs[0].Text), resetLinkToken(t, msgs[1].Text)
	if first == second {
		t.Fatal("both emails carry the same token")
	}

	if err := ResetPassword(ctx, s, first, resetNewPassword, resetNewPassword, resetAt); !errors.Is(err, ErrResetInvalid) {
		t.Errorf("the first link: err = %v, want ErrResetInvalid", err)
	}
	if err := ResetPassword(ctx, s, second, resetNewPassword, resetNewPassword, resetAt); err != nil {
		t.Errorf("the second link: %v", err)
	}
}

func TestRequestPasswordResetStoreFailureIsAnError(t *testing.T) {
	ctx := t.Context()
	err := RequestPasswordReset(ctx, confirmFailingStore{}, signupOrigin, loginEmail, testNow)
	if err == nil || !strings.Contains(err.Error(), "the database is down") {
		t.Errorf("err = %v, want the store failure", err)
	}
	// A string that is not an address never reaches the store.
	if err := RequestPasswordReset(ctx, confirmFailingStore{}, signupOrigin, "not an address", testNow); err != nil {
		t.Errorf("malformed address: err = %v, want nil", err)
	}
}

func TestResetPasswordRefusesLinksThatDoNotWork(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	u := insertLoginUser(t, s)
	unknown, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	used := resetToken(t, s, u.ID, testNow)
	if err := ResetPassword(ctx, s, used, resetNewPassword, resetNewPassword, resetAt); err != nil {
		t.Fatalf("the first use of the link: %v", err)
	}
	confirmation := confirmToken(t, s, u.ID, testNow)
	expired := resetToken(t, s, u.ID, testNow.Add(-2*ResetTokenTTL))

	for _, tc := range []struct{ name, token, password string }{
		{"empty", "", resetOtherPassword},
		{"malformed", "garbage", resetOtherPassword},
		{"malformed with a weak password", "garbage", "abc"},
		{"unknown", unknown, resetOtherPassword},
		{"used", used, resetOtherPassword},
		{"expired", expired, resetOtherPassword},
		{"a confirmation link", confirmation, resetOtherPassword},
	} {
		err := ResetPassword(ctx, s, tc.token, tc.password, tc.password, resetAt)
		var fe domain.FieldErrors
		if !errors.Is(err, ErrResetInvalid) || errors.As(err, &fe) {
			t.Errorf("%s: err = %v, want ErrResetInvalid", tc.name, err)
		}
	}
	// Nothing moved: the first use's password still stands and the
	// confirmation link was not spent.
	if _, err := Login(ctx, s, loginEmail, resetNewPassword, resetAt); err != nil {
		t.Errorf("login with the password the first use set: %v", err)
	}
	confirmAssertTokenUnspent(t, s, confirmation, resetAt)
}

func TestResetPasswordFieldErrorsLeaveTheLinkUsable(t *testing.T) {
	notEmail := "requisitos da senha: " + auth.RuleLabels[auth.RuleNotEmail]
	tests := []struct {
		name              string
		password, confirm string
		wantPassword      string // what the password message contains; "" for no message
		wantConfirm       string // what the password_confirm message contains; "" for no message
	}{
		{"no password", "", "", domain.MsgRequired, ""},
		{"not valid UTF-8", "Senha-Forte-1\xff", "Senha-Forte-1\xff", domain.MsgInvalid, ""},
		{"too weak", "abc", "abc", "requisitos da senha: " + auth.RuleLabels[auth.RuleMinLength], ""},
		{"no match", resetNewPassword, resetNewPassword + "x", "", resetMsgMismatch},
		{"weak and no match", "abc", "abd", "requisitos da senha: ", resetMsgMismatch},
		// Both pass the policy until the account's e-mail is known.
		{"the local part of the e-mail", "Ana.Souza1", "Ana.Souza1", notEmail, ""},
		{"the whole e-mail", "Ana.Souza1@Agenda.Test", "Ana.Souza1@Agenda.Test", notEmail, ""},
		{"the current password", loginPassword, loginPassword, resetMsgHistory, ""},
		{"the previous password", resetPrevPassword, resetPrevPassword, resetMsgHistory, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			ctx := t.Context()
			u := resetUser(t, s)
			raw := resetToken(t, s, u.ID, testNow)

			err := ResetPassword(ctx, s, raw, tc.password, tc.confirm, resetAt)
			fe := signupFieldErrors(t, err)
			var keys []string
			if tc.wantPassword != "" {
				keys = append(keys, "password")
				if !strings.Contains(fe["password"], tc.wantPassword) {
					t.Errorf("password message %q, want it to contain %q", fe["password"], tc.wantPassword)
				}
			}
			if tc.wantConfirm != "" {
				keys = append(keys, "password_confirm")
				if !strings.Contains(fe["password_confirm"], tc.wantConfirm) {
					t.Errorf("password_confirm message %q, want it to contain %q", fe["password_confirm"], tc.wantConfirm)
				}
			}
			signupAssertKeys(t, fe, keys...)

			if got, want := resetHashes(t, s, u.ID), []string{loginHash(), resetPrevHash()}; !slices.Equal(got, want) {
				t.Errorf("the refused reset changed the password history")
			}
			// The link was not spent: it still sets a good password.
			if err := ResetPassword(ctx, s, raw, resetNewPassword, resetNewPassword, resetAt); err != nil {
				t.Errorf("the link after the refused reset: %v", err)
			}
		})
	}
}

func TestResetPasswordUnreadableHistoryIsAServerError(t *testing.T) {
	// A hash that cannot be read must never read as "no match".
	for _, tc := range []struct {
		name  string
		build func(t *testing.T, s domain.Store) domain.User
	}{
		{"the current hash", func(t *testing.T, s domain.Store) domain.User {
			return insertActiveUser(t, s, resetEmail)
		}},
		{"the previous hash", func(t *testing.T, s domain.Store) domain.User {
			u := insertActiveUser(t, s, resetEmail)
			if err := s.Repos().Users.ChangePassword(t.Context(), u.ID, loginHash(), testNow); err != nil {
				t.Fatalf("change password: %v", err)
			}
			return u
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			u := tc.build(t, s)
			before := resetHashes(t, s, u.ID)
			raw := resetToken(t, s, u.ID, testNow)

			err := ResetPassword(t.Context(), s, raw, resetNewPassword, resetNewPassword, resetAt)
			resetAssertServerError(t, "ResetPassword", err)
			if got := resetHashes(t, s, u.ID); !slices.Equal(got, before) {
				t.Errorf("history %v after the failed reset, want %v", got, before)
			}
			resetAssertUnspent(t, s, raw, resetAt)
		})
	}
}

func TestResetPasswordRefusesAnAccountThatIsNotActive(t *testing.T) {
	for _, status := range []domain.UserStatus{domain.UserStatusPendingEmail, domain.UserStatusPendingApproval, domain.UserStatusDisabled} {
		t.Run(string(status), func(t *testing.T) {
			s := newTestStore(t)
			u := insertLoginUser(t, s, func(u *domain.User) { u.Status = status })
			raw := resetToken(t, s, u.ID, testNow)

			err := ResetPassword(t.Context(), s, raw, resetNewPassword, resetNewPassword, resetAt)
			if !errors.Is(err, ErrResetInvalid) {
				t.Errorf("err = %v, want ErrResetInvalid", err)
			}
			if got := loginReload(t, s, u.ID).PasswordHash; got != loginHash() {
				t.Error("the password of an account that is not active changed")
			}
			resetAssertUnspent(t, s, raw, resetAt)
		})
	}
}

func TestResetPasswordEndsEverySessionAndUnlocksTheAccount(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	locked := testNow.Add(time.Hour)
	u := insertLoginUser(t, s, func(u *domain.User) { u.FailedLogins = 4; u.LockedUntil = &locked })
	other := insertActiveUser(t, s, "outra@agenda.test")
	if got := loginReload(t, s, u.ID); got.FailedLogins != 4 || got.LockedUntil == nil {
		t.Fatalf("fixture: failed logins %d, locked until %v", got.FailedLogins, got.LockedUntil)
	}
	start := func(userID string) string {
		raw, _, err := auth.StartSession(ctx, s.Repos().Sessions, userID, "test-agent", testNow)
		if err != nil {
			t.Fatalf("StartSession: %v", err)
		}
		return raw
	}
	mine := []string{start(u.ID), start(u.ID)}
	theirs := start(other.ID)
	raw := resetToken(t, s, u.ID, testNow)

	if err := ResetPassword(ctx, s, raw, resetNewPassword, resetNewPassword, resetAt); err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}

	sessionIs := func(cookie string, wantErr error) {
		t.Helper()
		hash, err := auth.HashToken(cookie)
		if err != nil {
			t.Fatalf("HashToken: %v", err)
		}
		if _, err := s.Repos().Sessions.Get(ctx, hash); !errors.Is(err, wantErr) {
			t.Errorf("Sessions.Get: err = %v, want %v", err, wantErr)
		}
	}
	for _, cookie := range mine {
		sessionIs(cookie, domain.ErrNotFound)
	}
	sessionIs(theirs, nil) // someone else's login stays

	got := loginReload(t, s, u.ID)
	if got.FailedLogins != 0 || got.LockedUntil != nil {
		t.Errorf("failed logins %d, locked until %v after the reset, want 0 and none", got.FailedLogins, got.LockedUntil)
	}
	if !got.PasswordChangedAt.Equal(resetAt.Truncate(time.Millisecond)) {
		t.Errorf("PasswordChangedAt = %v, want %v", got.PasswordChangedAt, resetAt)
	}
	hashes := resetHashes(t, s, u.ID)
	if len(hashes) != 2 || hashes[1] != loginHash() {
		t.Errorf("history = %v, want the new hash then the old one", hashes)
	}
	// A locked account would be refused here.
	if _, err := Login(ctx, s, loginEmail, resetNewPassword, resetAt); err != nil {
		t.Errorf("login with the new password: %v", err)
	}
}

func TestResetPasswordStoreFailureIsAServerError(t *testing.T) {
	ctx := t.Context()
	raw, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	resetAssertServerError(t, "store failure", ResetPassword(ctx, confirmFailingStore{}, raw, resetNewPassword, resetNewPassword, resetAt))
	// A malformed link is refused before the store is asked: the failing store
	// would answer with a server error.
	if err := ResetPassword(ctx, confirmFailingStore{}, "garbage", resetNewPassword, resetNewPassword, resetAt); !errors.Is(err, ErrResetInvalid) {
		t.Errorf("malformed token: err = %v, want ErrResetInvalid", err)
	}
	// So are a weak password and a mismatch.
	if err := ResetPassword(ctx, confirmFailingStore{}, raw, "abc", "abc", resetAt); err == nil || errors.Is(err, ErrResetInvalid) {
		t.Errorf("weak password: err = %v, want field errors", err)
	} else {
		signupFieldErrors(t, err)
	}
}

// A link nobody issued must cost no argon2 work: hashing a password takes about
// 19 MiB and the reset form has no rate limit, so the hash runs only once the
// link has been consumed.
func TestResetPasswordHashesOnlyForAWorkingLink(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	u := insertLoginUser(t, s)
	var calls int
	real := resetHashPassword
	resetHashPassword = func(password string) (string, error) {
		calls++
		return real(password)
	}
	t.Cleanup(func() { resetHashPassword = real })

	forged, _, err := auth.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if err := ResetPassword(ctx, s, forged, resetNewPassword, resetNewPassword, resetAt); !errors.Is(err, ErrResetInvalid) {
		t.Fatalf("forged link: err = %v, want ErrResetInvalid", err)
	}
	if calls != 0 {
		t.Errorf("a forged link hashed the password %d time(s), want 0", calls)
	}

	link := resetToken(t, s, u.ID, testNow)
	if err := ResetPassword(ctx, s, link, resetNewPassword, resetNewPassword, resetAt); err != nil {
		t.Fatalf("working link: %v", err)
	}
	if calls == 0 {
		t.Error("a working link never hashed the new password")
	}
}
