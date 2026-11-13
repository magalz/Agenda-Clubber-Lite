package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// adminPassword passes every rule of the password policy.
const adminPassword = "Str0ng-Passw0rd!"

// runCmd runs a subcommand through run, with stdin as its standard input.
func runCmd(t *testing.T, env []string, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(context.Background(), args, getenvOf(env...), strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// openTestStore opens the database env names. It is closed when the test ends,
// before mongoEnv's cleanup drops the database (cleanups run last in, first out).
func openTestStore(t *testing.T, env []string) domain.Store {
	t.Helper()
	cfg, err := config.LoadMongo(getenvOf(env...))
	if err != nil {
		t.Fatalf("load the test database settings: %v", err)
	}
	store, err := openStore(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open the test database: %v", err)
	}
	t.Cleanup(func() {
		if err := closeStore(store); err != nil {
			t.Errorf("close the test store: %v", err)
		}
	})
	return store
}

// wantNone fails when an output carries one of the secrets. The failure names the
// secret and never prints it.
func wantNone(t *testing.T, secrets map[string]string, outputs ...string) {
	t.Helper()
	for name, secret := range secrets {
		if strings.TrimSpace(secret) == "" {
			continue
		}
		for _, out := range outputs {
			if strings.Contains(out, secret) {
				t.Errorf("an output leaks %s", name)
			}
		}
	}
}

func TestCreateAdminCreatesAnActiveAdmin(t *testing.T) {
	for _, tc := range []struct{ name, phone, wantPhone string }{
		{"mobile number with punctuation", "(85) 99999-0001", "85999990001"},
		{"landline", "85 3222-1234", "8532221234"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := mongoEnv(t)
			before := time.Now().UTC().Truncate(time.Millisecond)
			code, stdout, stderr := runCmd(t, env, adminPassword+"\n",
				"create-admin", "--name", "  Ana Admin  ", "--email", " Ana@Example.COM ", "--phone", tc.phone)
			after := time.Now()
			if code != 0 || stdout != "admin created: ana@example.com\n" || stderr != "" {
				t.Fatalf("exit code = %d, stdout = %q, stderr = %q; want 0, %q and nothing on stderr", code, stdout, stderr, "admin created: ana@example.com\n")
			}

			u, err := openTestStore(t, env).Repos().Users.ByEmail(t.Context(), "ana@example.com")
			if err != nil {
				t.Fatalf("find the admin: %v", err)
			}
			if u.Name != "Ana Admin" || u.Email != "ana@example.com" || u.Phone != tc.wantPhone {
				t.Errorf("name, email, phone = %q, %q, %q; want %q, %q, %q", u.Name, u.Email, u.Phone, "Ana Admin", "ana@example.com", tc.wantPhone)
			}
			if u.Status != domain.UserStatusActive || !u.IsAdmin || u.ApprovedBy != "" || u.FailedLogins != 0 {
				t.Errorf("status = %q, admin = %v, approved by %q, failed logins = %d; want an active admin nobody approved", u.Status, u.IsAdmin, u.ApprovedBy, u.FailedLogins)
			}
			if u.EmailVerifiedAt == nil {
				t.Fatal("EmailVerifiedAt is unset, want now")
			}
			for name, got := range map[string]time.Time{
				"EmailVerifiedAt": *u.EmailVerifiedAt, "PasswordChangedAt": u.PasswordChangedAt, "CreatedAt": u.CreatedAt, "UpdatedAt": u.UpdatedAt,
			} {
				if got.Before(before) || got.After(after) || !got.Equal(u.CreatedAt) {
					t.Errorf("%s = %v, want the one moment of creation, in [%v, %v]", name, got, before, after)
				}
			}
			if ok, err := auth.VerifyPassword(u.PasswordHash, adminPassword); err != nil || !ok {
				t.Errorf("VerifyPassword(stored hash, the password) = %v, %v; want true", ok, err)
			}
			wantNone(t, map[string]string{"the password": adminPassword, "the password hash": u.PasswordHash, "the database URI": env[1]}, stdout, stderr)
		})
	}
}

// The password is the first line of stdin and nothing else: a CR LF line ending
// loses both characters, a space does not.
func TestCreateAdminReadsTheFirstLineOfStdin(t *testing.T) {
	for _, tc := range []struct{ name, stdin, want string }{
		{"line feed", adminPassword + "\n", adminPassword},
		{"carriage return and line feed", adminPassword + "\r\n", adminPassword},
		{"no line ending", adminPassword, adminPassword},
		{"only the first line", adminPassword + "\nsegunda-linha\n", adminPassword},
		{"spaces belong to the password", adminPassword + " \n", adminPassword + " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := mongoEnv(t)
			code, stdout, stderr := runCmd(t, env, tc.stdin, "create-admin", "--name", "Ana", "--email", "ana@example.com", "--phone", "85999990001")
			if code != 0 {
				t.Fatalf("exit code = %d, stdout = %q, stderr = %q; want 0", code, stdout, stderr)
			}
			u, err := openTestStore(t, env).Repos().Users.ByEmail(t.Context(), "ana@example.com")
			if err != nil {
				t.Fatalf("find the admin: %v", err)
			}
			if ok, err := auth.VerifyPassword(u.PasswordHash, tc.want); err != nil || !ok {
				t.Errorf("the stored hash does not match the %s password; err = %v", tc.name, err)
			}
		})
	}
}

// A password that breaks the policy is refused with the rules it misses, before
// anything reaches the database, and is never printed.
func TestCreateAdminRejectsAPasswordThatBreaksThePolicy(t *testing.T) {
	env := mongoEnv(t)
	for _, tc := range []struct {
		name  string
		stdin string
		want  []auth.Rule // the rules named in the message, and only those
	}{
		{"too short", "Pw-xq7\n", []auth.Rule{auth.RuleMinLength}},
		{"no upper case, digit or symbol", "qwertyuiop\n", []auth.Rule{auth.RuleUpper, auth.RuleDigit, auth.RuleSymbol}},
		{"nothing on stdin", "", []auth.Rule{auth.RuleMinLength, auth.RuleLower, auth.RuleUpper, auth.RuleDigit, auth.RuleSymbol}},
		{"the email itself", "ana@example.com\n", []auth.Rule{auth.RuleUpper, auth.RuleDigit, auth.RuleNotEmail}},
		{"too long", strings.Repeat("Aa1!", 40) + "\n", []auth.Rule{auth.RuleMaxLength}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(t, env, tc.stdin, "create-admin", "--name", "Ana", "--email", "ana@example.com", "--phone", "85999990001")
			if code != 1 || stdout != "" {
				t.Errorf("exit code = %d, stdout = %q; want 1 and nothing on stdout", code, stdout)
			}
			if !strings.HasPrefix(stderr, "agenda: create-admin: requisitos da senha: ") {
				t.Errorf("stderr = %q, want it to start with the sign-up wording of the policy message", stderr)
			}
			for _, rule := range auth.Rules() {
				missing := false
				for _, w := range tc.want {
					missing = missing || w == rule
				}
				if has := strings.Contains(stderr, auth.RuleLabels[rule]); has != missing {
					t.Errorf("stderr = %q; names rule %q = %v, want %v", stderr, rule, has, missing)
				}
			}
			wantNone(t, map[string]string{"the password": strings.TrimRight(tc.stdin, "\r\n")}, stdout, stderr)
		})
	}
	if got := collectionNames(t, env); len(got) != 0 {
		t.Errorf("collections = %v, want none: the policy is checked before the database is opened", got)
	}
}

// A name with a line break or any other control character is refused as at
// sign-up (names end up in emails), before the database settings are read: with
// no MONGO_URI, or one nobody answers, the only message is the name's. The
// password is never printed.
func TestCreateAdminRefusesControlCharactersInTheName(t *testing.T) {
	old := openTimeout
	openTimeout = 500 * time.Millisecond
	t.Cleanup(func() { openTimeout = old })

	const want = "agenda: create-admin: name: não use quebras de linha nem caracteres de controle\n"
	for _, tc := range []struct{ name, fullName string }{
		{"line feed", "Ana\nAdmin"},
		{"carriage return and line feed", "Ana\r\nAdmin"},
		{"tab", "Ana\tAdmin"},
		{"nul", "Ana\x00Admin"},
		{"a line feed at the end", "Ana Admin\n"},
	} {
		for _, db := range []struct {
			name string
			env  []string
		}{
			{"no MONGO_URI", nil},
			{"an unreachable server", []string{"MONGO_URI", unreachableURI}},
		} {
			t.Run(tc.name+", "+db.name, func(t *testing.T) {
				code, stdout, stderr := runCmd(t, db.env, adminPassword+"\n", "create-admin", "--name", tc.fullName, "--email", "ana@example.com", "--phone", "85999990001")
				if code != 1 || stdout != "" || stderr != want {
					t.Errorf("exit code = %d, stdout = %q, stderr = %q; want 1, nothing and %q", code, stdout, stderr, want)
				}
				wantNone(t, map[string]string{"the password": adminPassword}, stdout, stderr)
			})
		}
	}
}

func TestCreateAdminRefusesAnEmailThatHasAnAccount(t *testing.T) {
	env := mongoEnv(t)
	if code, _, stderr := runCmd(t, env, adminPassword+"\n", "create-admin", "--name", "Ana", "--email", "ana@example.com", "--phone", "85999990001"); code != 0 {
		t.Fatalf("first create-admin: exit code = %d, stderr = %q; want 0", code, stderr)
	}

	code, stdout, stderr := runCmd(t, env, adminPassword+"\n", "create-admin", "--name", "Outra Ana", "--email", "ANA@Example.com", "--phone", "85999990002")
	if code != 1 || stdout != "" || stderr != "agenda: create-admin: this email already has an account\n" {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q; want 1, nothing and the duplicate message", code, stdout, stderr)
	}
	admins, err := openTestStore(t, env).Repos().Users.ListAdminEmails(t.Context())
	if err != nil || len(admins) != 1 || admins[0] != "ana@example.com" {
		t.Errorf("admins = %v, %v; want only ana@example.com", admins, err)
	}
}

// A call that is not made right is a usage error (exit 2) and touches nothing.
func TestCreateAdminUsageErrors(t *testing.T) {
	env := mongoEnv(t)
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no flags", nil, "--name"},
		{"no name", []string{"--email", "ana@example.com", "--phone", "85999990001"}, "--name"},
		{"no email", []string{"--name", "Ana", "--phone", "85999990001"}, "--email"},
		{"no phone", []string{"--name", "Ana", "--email", "ana@example.com"}, "--phone"},
		{"a blank name", []string{"--name", "  ", "--email", "ana@example.com", "--phone", "85999990001"}, "--name"},
		{"an unknown flag", []string{"--name", "Ana", "--email", "ana@example.com", "--phone", "85999990001", "--admin"}, "usage: agenda create-admin"},
		{"a stray argument", []string{"--name", "Ana", "--email", "ana@example.com", "--phone", "85999990001", "extra"}, "usage: agenda create-admin"},
		{"help", []string{"-h"}, "usage: agenda create-admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(t, env, adminPassword+"\n", append([]string{"create-admin"}, tc.args...)...)
			if code != 2 || stdout != "" {
				t.Errorf("exit code = %d, stdout = %q; want 2 and nothing on stdout", code, stdout)
			}
			if !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "usage: agenda create-admin") {
				t.Errorf("stderr = %q, want it to mention %q and show the usage", stderr, tc.want)
			}
		})
	}
	if got := collectionNames(t, env); len(got) != 0 {
		t.Errorf("collections = %v, want none: a usage error opens no database", got)
	}
}

func TestCreateAdminShowsTheFieldErrors(t *testing.T) {
	for _, tc := range []struct {
		name                string
		stdin               string
		fullName, email, ph string
		want                string
	}{
		{"an email that is not one", adminPassword, "Ana", "ana@", "85999990001", "email: informe um e-mail válido"},
		{"too few digits in the phone", adminPassword, "Ana", "ana@example.com", "123", "phone: informe de 10 a 11 dígitos"},
		{"too many digits in the phone", adminPassword, "Ana", "ana@example.com", "859999900011", "phone: informe de 10 a 11 dígitos"},
		{"a name that is too long", adminPassword, strings.Repeat("a", 121), "ana@example.com", "85999990001", "name: use no máximo 120 caracteres"},
		{"a name that is not text", adminPassword, "Ana\xff", "ana@example.com", "85999990001", "name: valor inválido"},
		{"a password that is not text", "Str0ng-\xffPass!", "Ana", "ana@example.com", "85999990001", "password: valor inválido"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := mongoEnv(t)
			code, stdout, stderr := runCmd(t, env, tc.stdin+"\n", "create-admin", "--name", tc.fullName, "--email", tc.email, "--phone", tc.ph)
			if code != 1 || stdout != "" || stderr != "agenda: create-admin: "+tc.want+"\n" {
				t.Errorf("exit code = %d, stdout = %q, stderr = %q; want 1, nothing and %q", code, stdout, stderr, "agenda: create-admin: "+tc.want)
			}
			if admins, err := openTestStore(t, env).Repos().Users.ListAdminEmails(t.Context()); err != nil || len(admins) != 0 {
				t.Errorf("admins = %v, %v; want none", admins, err)
			}
		})
	}
}

// A database that is not configured or cannot be reached is reported by what
// failed, and neither the URI nor the password reaches the output.
func TestCreateAdminDatabaseErrorsCarryNoSecrets(t *testing.T) {
	old := openTimeout
	openTimeout = 500 * time.Millisecond
	t.Cleanup(func() { openTimeout = old })

	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"no MONGO_URI", nil, "MONGO_URI"},
		{"an unreachable server", []string{"MONGO_URI", unreachableURI}, "agenda: create-admin: " + openHint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(t, tc.env, adminPassword+"\n", "create-admin", "--name", "Ana", "--email", "ana@example.com", "--phone", "85999990001")
			if code != 1 || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Errorf("exit code = %d, stdout = %q, stderr = %q; want 1, nothing on stdout and %q on stderr", code, stdout, stderr, tc.want)
			}
			wantNoSecrets(t, stdout, stderr)
			wantNone(t, map[string]string{"the password": adminPassword}, stdout, stderr)
		})
	}
}
