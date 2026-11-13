package main

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// seedPassword passes the password policy for every seed account.
const seedPassword = "Seed-Pass-2026!"

// seedLocals are the local parts of the seed accounts, all at agenda.test.
var seedLocals = []string{"admin", "dono", "membro", "pendente-email", "pendente-aprovacao", "senha-vencida", "senha-quase"}

func seedEmail(local string) string { return local + "@agenda.test" }

// seedEnv returns the variables a seed-dev run needs on top of the database ones
// in env: a dev environment and the password. extra pairs come last.
func seedEnv(env []string, extra ...string) []string {
	return slices.Concat([]string{"APP_ENV", "dev", "SEED_PASSWORD", seedPassword}, env, extra)
}

// docCounts counts the documents of every collection of the database env names.
func docCounts(t *testing.T, env []string) map[string]int64 {
	t.Helper()
	get := getenvOf(env...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(get("MONGO_URI")))
	if err != nil {
		t.Fatal("connect to the test database") // the error may echo the URI
	}
	defer func() { _ = client.Disconnect(ctx) }()
	counts := map[string]int64{}
	for _, name := range []string{"collectives", "memberships", "outbox", "sessions", "tokens", "users"} {
		n, err := client.Database(get("MONGO_DB")).Collection(name).CountDocuments(ctx, bson.M{})
		if err != nil {
			t.Fatalf("count the documents of %s: %v", name, err)
		}
		counts[name] = n
	}
	return counts
}

// seedUsers reads the seed accounts, by local part.
func seedUsers(t *testing.T, store domain.Store) map[string]domain.User {
	t.Helper()
	users := map[string]domain.User{}
	for _, local := range seedLocals {
		u, err := store.Repos().Users.ByEmail(t.Context(), seedEmail(local))
		if err != nil {
			t.Fatalf("find %s: %v", seedEmail(local), err)
		}
		users[local] = u
	}
	return users
}

// wantWithin fails unless got lies in [lo, hi].
func wantWithin(t *testing.T, name string, got *time.Time, lo, hi time.Time) {
	t.Helper()
	if got == nil {
		t.Errorf("%s is unset, want a time in [%v, %v]", name, lo, hi)
		return
	}
	if got.Before(lo) || got.After(hi) {
		t.Errorf("%s = %v, want a time in [%v, %v]", name, *got, lo, hi)
	}
}

// seedLines are the lines of a first run, one per record.
func seedLines(state string) []string {
	var lines []string
	for _, local := range seedLocals {
		lines = append(lines, "user "+seedEmail(local)+": "+state)
	}
	return append(lines,
		"collective Coletivo Alfa: "+state,
		"collective Produtora Beta: "+state,
		"membership dono@agenda.test in Coletivo Alfa: "+state,
		"membership dono@agenda.test in Produtora Beta: "+state,
		"membership membro@agenda.test in Coletivo Alfa: "+state,
		"membership pendente-aprovacao@agenda.test in Coletivo Alfa: "+state,
	)
}

func wantLines(t *testing.T, stdout string, lines []string) {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(stdout, line+"\n") {
			t.Errorf("stdout has no line %q:\n%s", line, stdout)
		}
	}
}

// linkIn returns the confirmation link that follows prefix in stdout.
func linkIn(t *testing.T, stdout, prefix string) (raw string) {
	t.Helper()
	if n := strings.Count(stdout, "/confirmar"); n != 1 {
		t.Fatalf("stdout has %d confirmation links, want 1:\n%s", n, stdout)
	}
	i := strings.Index(stdout, prefix)
	if i < 0 {
		t.Fatalf("stdout has no link starting with %q:\n%s", prefix, stdout)
	}
	fields := strings.Fields(stdout[i+len(prefix):])
	if len(fields) == 0 || fields[0] == "" {
		t.Fatalf("the link in stdout has no token:\n%s", stdout)
	}
	return fields[0]
}

// seed-dev writes sample data for a developer, so it refuses before it touches
// anything unless it is told it runs in dev and has a password to give the
// accounts.
func TestSeedDevRefuses(t *testing.T) {
	env := mongoEnv(t)
	for _, tc := range []struct {
		name string
		vars []string // on top of the database variables
		want string
	}{
		{"no environment", []string{"SEED_PASSWORD", seedPassword}, "APP_ENV must be dev"},
		{"production", []string{"APP_ENV", "prod", "SEED_PASSWORD", seedPassword}, "APP_ENV must be dev"},
		{"no password", []string{"APP_ENV", "dev"}, "SEED_PASSWORD is not set"},
		{"a blank password", []string{"APP_ENV", "dev", "SEED_PASSWORD", "   "}, "SEED_PASSWORD does not meet the rules"},
		{"a weak password", []string{"APP_ENV", "dev", "SEED_PASSWORD", "qwerty"}, "SEED_PASSWORD does not meet the rules"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(t, slices.Concat(tc.vars, env), "", "seed-dev")
			if code != 1 || stdout != "" || !strings.Contains(stderr, "agenda: seed-dev: ") || !strings.Contains(stderr, tc.want) {
				t.Errorf("exit code = %d, stdout = %q, stderr = %q; want 1, nothing on stdout and %q on stderr", code, stdout, stderr, tc.want)
			}
			wantNone(t, map[string]string{"the weak password": "qwerty", "the database URI": env[1]}, stdout, stderr)
		})
	}

	// The weak password is told what it lacks.
	_, _, stderr := runCmd(t, seedEnv(env, "SEED_PASSWORD", "qwerty"), "", "seed-dev")
	for _, rule := range []auth.Rule{auth.RuleMinLength, auth.RuleUpper, auth.RuleDigit, auth.RuleSymbol} {
		if !strings.Contains(stderr, auth.RuleLabels[rule]) {
			t.Errorf("stderr = %q, want it to name the rule %q", stderr, auth.RuleLabels[rule])
		}
	}
	if got := collectionNames(t, env); len(got) != 0 {
		t.Errorf("collections = %v, want none: a refused run opens no database", got)
	}
}

func TestSeedDevCreatesTheSampleData(t *testing.T) {
	env := mongoEnv(t)
	before := time.Now().UTC().Truncate(time.Millisecond)
	code, stdout, stderr := runCmd(t, seedEnv(env, "APP_ORIGIN", "https://agenda.example/"), "", "seed-dev")
	after := time.Now()
	if code != 0 || stderr != "" {
		t.Fatalf("exit code = %d, stderr = %q; want 0 and nothing on stderr", code, stderr)
	}
	wantLines(t, stdout, seedLines("created"))
	if !strings.Contains(stdout, "13 created, 0 existing") {
		t.Errorf("stdout has no summary of 13 created records:\n%s", stdout)
	}

	// Nothing the run prints may unlock an account or the database.
	wantNone(t, map[string]string{
		"the seed password": seedPassword, "the previous password": "Anterior-" + seedPassword,
		"a password hash": "$argon2id", "the database URI": env[1],
	}, stdout, stderr)

	store := openTestStore(t, env)
	repos := store.Repos()
	users := seedUsers(t, store)

	// Accounts: status, admin flag, and how far each got in the sign-up.
	for _, tc := range []struct {
		local    string
		status   domain.UserStatus
		verified bool
		admin    bool
	}{
		{"admin", domain.UserStatusActive, true, true},
		{"dono", domain.UserStatusActive, true, false},
		{"membro", domain.UserStatusActive, true, false},
		{"pendente-email", domain.UserStatusPendingEmail, false, false},
		{"pendente-aprovacao", domain.UserStatusPendingApproval, true, false},
		{"senha-vencida", domain.UserStatusActive, true, false},
		{"senha-quase", domain.UserStatusActive, true, false},
	} {
		u := users[tc.local]
		if u.Status != tc.status || u.IsAdmin != tc.admin || (u.EmailVerifiedAt != nil) != tc.verified {
			t.Errorf("%s: status = %q, admin = %v, verified = %v; want %q, %v, %v", tc.local, u.Status, u.IsAdmin, u.EmailVerifiedAt != nil, tc.status, tc.admin, tc.verified)
		}
		if u.Name == "" || u.Phone == "" {
			t.Errorf("%s: name = %q, phone = %q; want both set", tc.local, u.Name, u.Phone)
		}
		if ok, err := auth.VerifyPassword(u.PasswordHash, seedPassword); tc.local != "senha-vencida" && (err != nil || !ok) {
			t.Errorf("%s: VerifyPassword(stored hash, the seed password) = %v, %v; want true", tc.local, ok, err)
		}
		// The active accounts other than the admin were approved by the admin.
		wantApprover := ""
		if tc.status == domain.UserStatusActive && !tc.admin {
			wantApprover = users["admin"].ID
		}
		if u.ApprovedBy != wantApprover || (u.ApprovedAt != nil) != (wantApprover != "") {
			t.Errorf("%s: approved by %q (at %v), want approved by %q", tc.local, u.ApprovedBy, u.ApprovedAt, wantApprover)
		}
	}

	// The two accounts with an old password.
	oneDay := 24 * time.Hour
	if u := users["senha-vencida"]; !auth.PasswordExpired(u.PasswordChangedAt, time.Now()) {
		t.Errorf("senha-vencida: PasswordChangedAt = %v, want the password expired", u.PasswordChangedAt)
	} else {
		wantWithin(t, "senha-vencida PasswordChangedAt", &u.PasswordChangedAt, before.Add(-91*oneDay), after.Add(-91*oneDay))
		hashes, err := repos.Users.RecentPasswordHashes(t.Context(), u.ID)
		if err != nil || len(hashes) != 2 {
			t.Fatalf("senha-vencida: RecentPasswordHashes = %d entries, %v; want 2", len(hashes), err)
		}
		if ok, _ := auth.VerifyPassword(hashes[0], seedPassword); !ok {
			t.Error("senha-vencida: the current password is not the seed password")
		}
		if ok, _ := auth.VerifyPassword(hashes[1], "Anterior-"+seedPassword); !ok {
			t.Error("senha-vencida: the previous password is not Anterior- and the seed password")
		}
	}
	if u := users["senha-quase"]; auth.DaysUntilExpiry(u.PasswordChangedAt, time.Now()) != 3 {
		t.Errorf("senha-quase: %d days until the password expires, want 3 (PasswordChangedAt = %v)", auth.DaysUntilExpiry(u.PasswordChangedAt, time.Now()), u.PasswordChangedAt)
	} else {
		wantWithin(t, "senha-quase PasswordChangedAt", &u.PasswordChangedAt, before.Add(-87*oneDay), after.Add(-87*oneDay))
	}
	for _, local := range []string{"admin", "dono", "membro", "pendente-email", "pendente-aprovacao"} {
		u := users[local]
		wantWithin(t, local+" PasswordChangedAt", &u.PasswordChangedAt, before, after)
		wantWithin(t, local+" CreatedAt", &u.CreatedAt, before, after)
	}

	// Collectives: both approved, made by the owner, approved by the admin.
	for _, tc := range []struct {
		name string
		kind domain.CollectiveKind
	}{{"Coletivo Alfa", domain.CollectiveKindColetivo}, {"Produtora Beta", domain.CollectiveKindProdutora}} {
		c, err := repos.Collectives.ByNameKey(t.Context(), domain.NameKey(tc.name))
		if err != nil {
			t.Fatalf("find the collective %s: %v", tc.name, err)
		}
		if c.Name != tc.name || c.Kind != tc.kind || c.Status != domain.CollectiveStatusApproved || c.CreatedBy != users["dono"].ID || c.ApprovedBy != users["admin"].ID {
			t.Errorf("%s: name = %q, kind = %q, status = %q, created by %q, approved by %q; want %q, %q, approved, the owner, the admin", tc.name, c.Name, c.Kind, c.Status, c.CreatedBy, c.ApprovedBy, tc.name, tc.kind)
		}
		wantWithin(t, tc.name+" ApprovedAt", c.ApprovedAt, before, after)
	}

	// Memberships, by user, in the order of the collective's name.
	type membership struct {
		collective string
		role       domain.Role
		status     domain.MembershipStatus
	}
	for local, want := range map[string][]membership{
		"dono":               {{"Coletivo Alfa", domain.RoleOwner, domain.MembershipStatusApproved}, {"Produtora Beta", domain.RoleOwner, domain.MembershipStatusApproved}},
		"membro":             {{"Coletivo Alfa", domain.RoleMember, domain.MembershipStatusApproved}},
		"pendente-aprovacao": {{"Coletivo Alfa", domain.RoleMember, domain.MembershipStatusPending}},
		"admin":              nil, "pendente-email": nil, "senha-vencida": nil, "senha-quase": nil,
	} {
		list, err := repos.Memberships.ListByUser(t.Context(), users[local].ID)
		if err != nil {
			t.Fatalf("list the memberships of %s: %v", local, err)
		}
		var got []membership
		for _, m := range list {
			got = append(got, membership{m.CollectiveName, m.Role, m.Status})
			approved := m.Status == domain.MembershipStatusApproved
			if approved && (m.AdminApprovedBy != users["admin"].ID || m.OwnerApprovedBy != users["dono"].ID || m.DecidedAt == nil) {
				t.Errorf("%s in %s: approved by admin %q and owner %q, decided = %v; want the admin, the owner, and a decision time", local, m.CollectiveName, m.AdminApprovedBy, m.OwnerApprovedBy, m.DecidedAt != nil)
			}
			if !approved && (m.AdminApprovedBy != "" || m.OwnerApprovedBy != "" || m.DecidedAt != nil) {
				t.Errorf("%s in %s: a pending request already carries a decision: %+v", local, m.CollectiveName, m.Membership)
			}
			wantWithin(t, local+" membership RequestedAt", &m.RequestedAt, before, after)
		}
		if !slices.Equal(got, want) {
			t.Errorf("memberships of %s = %v, want %v", local, got, want)
		}
	}
	for local, want := range map[string]bool{"dono": true, "membro": false, "pendente-aprovacao": false, "admin": false} {
		if got, err := repos.Memberships.UserCanCreateEvents(t.Context(), users[local].ID); err != nil || got != want {
			t.Errorf("UserCanCreateEvents(%s) = %v, %v; want %v", local, got, err, want)
		}
	}

	// The link works for the account that waits for its e-mail, once.
	raw := linkIn(t, stdout, "https://agenda.example/confirmar?token=")
	hash, err := auth.HashToken(raw)
	if err != nil {
		t.Fatalf("the token in the link is not one: %v", err)
	}
	tok, err := repos.Tokens.Consume(t.Context(), domain.TokenPurposeVerifyEmail, hash, time.Now())
	if err != nil || tok.UserID != users["pendente-email"].ID {
		t.Errorf("Consume(the link's token) = user %q, %v; want the account of pendente-email", tok.UserID, err)
	}

	want := map[string]int64{"collectives": 2, "memberships": 4, "outbox": 0, "sessions": 0, "tokens": 1, "users": 7}
	if got := docCounts(t, env); !reflect.DeepEqual(got, want) {
		t.Errorf("documents per collection = %v, want %v", got, want)
	}
}

func TestSeedDevSecondRunChangesNothing(t *testing.T) {
	env := mongoEnv(t)
	senv := seedEnv(env, "APP_ORIGIN", "https://agenda.example")
	if code, _, stderr := runCmd(t, senv, "", "seed-dev"); code != 0 {
		t.Fatalf("first run: exit code = %d, stderr = %q; want 0", code, stderr)
	}
	store := openTestStore(t, env)
	usersBefore := seedUsers(t, store)
	countsBefore := docCounts(t, env)
	historyBefore, err := store.Repos().Users.RecentPasswordHashes(t.Context(), usersBefore["senha-vencida"].ID)
	if err != nil {
		t.Fatalf("read the password history: %v", err)
	}

	code, stdout, stderr := runCmd(t, senv, "", "seed-dev")
	if code != 0 || stderr != "" {
		t.Fatalf("second run: exit code = %d, stderr = %q; want 0 and nothing on stderr", code, stderr)
	}
	wantLines(t, stdout, seedLines("exists"))
	if strings.Contains(stdout, ": created") || strings.Contains(stdout, "/confirmar") {
		t.Errorf("the second run created something or printed a link:\n%s", stdout)
	}
	if !strings.Contains(stdout, "0 created, 13 existing") {
		t.Errorf("stdout has no summary of 13 existing records:\n%s", stdout)
	}
	wantNone(t, map[string]string{
		"the seed password": seedPassword, "the previous password": "Anterior-" + seedPassword,
		"a password hash": "$argon2id", "the database URI": env[1],
	}, stdout, stderr)

	if got := docCounts(t, env); !reflect.DeepEqual(got, countsBefore) {
		t.Errorf("documents per collection = %v after the second run, was %v", got, countsBefore)
	}
	if usersAfter := seedUsers(t, store); !reflect.DeepEqual(usersAfter, usersBefore) {
		t.Errorf("the second run changed an account:\nbefore %+v\nafter  %+v", usersBefore, usersAfter)
	}
	historyAfter, err := store.Repos().Users.RecentPasswordHashes(t.Context(), usersBefore["senha-vencida"].ID)
	if err != nil || !slices.Equal(historyAfter, historyBefore) {
		t.Errorf("the password history of senha-vencida changed in the second run (err = %v)", err)
	}
}

// An account that is already there is left as it is, whatever its password, and
// the rest of the data is made around it.
func TestSeedDevKeepsAnExistingAccount(t *testing.T) {
	env := mongoEnv(t)
	const otherPassword = "Other-Passw0rd!"
	if code, _, stderr := runCmd(t, env, otherPassword+"\n", "create-admin", "--name", "Dev Admin", "--email", "admin@agenda.test", "--phone", "85999990000"); code != 0 {
		t.Fatalf("create-admin: exit code = %d, stderr = %q; want 0", code, stderr)
	}

	code, stdout, stderr := runCmd(t, seedEnv(env), "", "seed-dev") // no APP_ORIGIN: the default
	if code != 0 || stderr != "" {
		t.Fatalf("exit code = %d, stderr = %q; want 0 and nothing on stderr", code, stderr)
	}
	lines := seedLines("created")
	lines[0] = "user admin@agenda.test: exists"
	wantLines(t, stdout, lines)
	if !strings.Contains(stdout, "12 created, 1 existing") {
		t.Errorf("stdout has no summary of 12 created and 1 existing record:\n%s", stdout)
	}
	linkIn(t, stdout, "http://localhost:8120/confirmar?token=")

	store := openTestStore(t, env)
	users := seedUsers(t, store)
	if ok, err := auth.VerifyPassword(users["admin"].PasswordHash, otherPassword); err != nil || !ok {
		t.Errorf("the existing admin lost its password: VerifyPassword = %v, %v", ok, err)
	}
	if ok, _ := auth.VerifyPassword(users["admin"].PasswordHash, seedPassword); ok {
		t.Error("the existing admin now takes the seed password")
	}
	if got := users["dono"].ApprovedBy; got != users["admin"].ID {
		t.Errorf("dono was approved by %q, want the existing admin %q", got, users["admin"].ID)
	}
	wantNone(t, map[string]string{"the seed password": seedPassword, "the other password": otherPassword, "the database URI": env[1]}, stdout, stderr)
}

// A database that is not configured or cannot be reached is reported by what
// failed, and neither the URI nor the password reaches the output.
func TestSeedDevDatabaseErrorsCarryNoSecrets(t *testing.T) {
	old := openTimeout
	openTimeout = 500 * time.Millisecond
	t.Cleanup(func() { openTimeout = old })

	for _, tc := range []struct {
		name string
		env  []string
		want string
	}{
		{"no MONGO_URI", nil, "MONGO_URI"},
		{"an unreachable server", []string{"MONGO_URI", unreachableURI}, "agenda: seed-dev: " + openHint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := runCmd(t, seedEnv(tc.env), "", "seed-dev")
			if code != 1 || stdout != "" || !strings.Contains(stderr, tc.want) {
				t.Errorf("exit code = %d, stdout = %q, stderr = %q; want 1, nothing on stdout and %q on stderr", code, stdout, stderr, tc.want)
			}
			wantNoSecrets(t, stdout, stderr)
			wantNone(t, map[string]string{"the seed password": seedPassword}, stdout, stderr)
		})
	}
}
