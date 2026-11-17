package main

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/catalog"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// seedPassword passes the password policy for every seed account.
const seedPassword = "Seed-Pass-2026!"

// seedLocals are the local parts of the seed accounts, all at agenda.test.
var seedLocals = []string{
	"admin", "dono", "membro", "pendente-email", "pendente-aprovacao", "senha-vencida", "senha-quase",
	"novo-coletivo", "pedido", "delta",
}

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
	for _, name := range []string{"collectives", "events", "memberships", "outbox", "sessions", "tokens", "users"} {
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

// seedCollectives are the names of the sample collectives.
var seedCollectives = []string{"Coletivo Alfa", "Produtora Beta", "Coletivo Gama", "Coletivo Delta"}

// seedCollectivesByName reads the sample collectives.
func seedCollectivesByName(t *testing.T, store domain.Store) map[string]domain.Collective {
	t.Helper()
	got := map[string]domain.Collective{}
	for _, name := range seedCollectives {
		c, err := store.Repos().Collectives.ByNameKey(t.Context(), domain.NameKey(name))
		if err != nil {
			t.Fatalf("find the collective %s: %v", name, err)
		}
		got[name] = c
	}
	return got
}

// seedEvents are the sample events: the collective each is in, the local part of
// the account that made it (the Administrador of that collective) and its name.
var seedEvents = []struct{ collective, creator, name string }{
	{"Coletivo Alfa", "dono", "Warm Up Techno"},
	{"Coletivo Alfa", "dono", "Rave Litoral"},
	{"Coletivo Alfa", "dono", "Noite House Recife"},
	{"Coletivo Alfa", "dono", "Baile do Brega Funk"},
	{"Coletivo Alfa", "dono", "After Hours Subsolo"},
	{"Coletivo Alfa", "dono", "Baile de Forró Eletrônico"},
	{"Produtora Beta", "dono", "Sarau na Praça"},
	{"Produtora Beta", "dono", "Pista Drum and Bass"},
	{"Produtora Beta", "dono", "Oficina de Discotecagem"},
	{"Produtora Beta", "dono", "Festival Nordeste Eletrônico"},
	{"Coletivo Delta", "delta", "Noite Ambient no Terraço"},
}

// seedRecordCount is how many records a first run creates: the accounts, the
// collectives, the 7 memberships and the events.
var seedRecordCount = len(seedLocals) + len(seedCollectives) + 7 + len(seedEvents)

// seedEventsByName reads the sample events, by name, from the lists of the sample
// collectives.
func seedEventsByName(t *testing.T, store domain.Store) map[string]domain.EventView {
	t.Helper()
	got := map[string]domain.EventView{}
	for _, c := range seedCollectivesByName(t, store) {
		list, err := store.Repos().Events.ListByCollectives(t.Context(), []string{c.ID}, domain.ListLimitMax)
		if err != nil {
			t.Fatalf("list the events of %s: %v", c.Name, err)
		}
		for _, v := range list {
			if _, dup := got[v.Name]; dup {
				t.Errorf("two sample events are named %q", v.Name)
			}
			got[v.Name] = v
		}
	}
	return got
}

// seedAt runs the records pass of seed-dev on store with a clock and a zone of the
// test's choosing, inside one transaction as seed-dev does.
func seedAt(t *testing.T, store domain.Store, now time.Time, loc *time.Location) seedResult {
	t.Helper()
	plan := seedPlan{now: now.UTC(), loc: loc, origin: devOrigin, hash: "hash", previousHash: "previous-hash"}
	var res seedResult
	err := store.WithTx(t.Context(), func(ctx context.Context, r domain.Repos) (err error) {
		res, err = seedRecords(ctx, r, plan)
		return err
	})
	if err != nil {
		t.Fatalf("seed at %v: %v", now, err)
	}
	return res
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
	lines = append(lines,
		"collective Coletivo Alfa: "+state,
		"collective Produtora Beta: "+state,
		"collective Coletivo Gama: "+state,
		"collective Coletivo Delta: "+state,
		"membership dono@agenda.test in Coletivo Alfa: "+state,
		"membership dono@agenda.test in Produtora Beta: "+state,
		"membership membro@agenda.test in Coletivo Alfa: "+state,
		"membership pendente-aprovacao@agenda.test in Coletivo Alfa: "+state,
		"membership novo-coletivo@agenda.test in Coletivo Gama: "+state,
		"membership pedido@agenda.test in Produtora Beta: "+state,
		"membership delta@agenda.test in Coletivo Delta: "+state,
	)
	for _, e := range seedEvents {
		lines = append(lines, "event "+e.name+" in "+e.collective+": "+state)
	}
	return lines
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
		{"an unknown time zone", []string{"APP_ENV", "dev", "SEED_PASSWORD", seedPassword, "APP_TZ", "Mars/Base"}, "APP_TZ"},
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
	if want := fmt.Sprintf("%d created, 0 existing", seedRecordCount); !strings.Contains(stdout, want) {
		t.Errorf("stdout has no summary of %q:\n%s", want, stdout)
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
		{"novo-coletivo", domain.UserStatusPendingApproval, true, false},
		{"pedido", domain.UserStatusActive, true, false},
		{"delta", domain.UserStatusActive, true, false},
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
	for _, local := range []string{"admin", "dono", "membro", "pendente-email", "pendente-aprovacao", "novo-coletivo", "pedido", "delta"} {
		u := users[local]
		wantWithin(t, local+" PasswordChangedAt", &u.PasswordChangedAt, before, after)
		wantWithin(t, local+" CreatedAt", &u.CreatedAt, before, after)
	}
	// The account that was "Dono Dev" is the Administrador of the sample collectives.
	if got := users["dono"].Name; got != "Administrador Dev" {
		t.Errorf("dono: name = %q, want %q", got, "Administrador Dev")
	}

	// Collectives: Alfa and Beta are approved by the admin; Gama is what a sign-up
	// leaves, waiting for the admin; Delta is approved but its events are blocked.
	for _, tc := range []struct {
		name          string
		kind          domain.CollectiveKind
		status        domain.CollectiveStatus
		creator       string // local part of the account that made it
		eventsBlocked bool
	}{
		{"Coletivo Alfa", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, "dono", false},
		{"Produtora Beta", domain.CollectiveKindProdutora, domain.CollectiveStatusApproved, "dono", false},
		{"Coletivo Gama", domain.CollectiveKindColetivo, domain.CollectiveStatusPending, "novo-coletivo", false},
		{"Coletivo Delta", domain.CollectiveKindColetivo, domain.CollectiveStatusApproved, "delta", true},
	} {
		c, err := repos.Collectives.ByNameKey(t.Context(), domain.NameKey(tc.name))
		if err != nil {
			t.Errorf("find the collective %s: %v", tc.name, err)
			continue
		}
		if c.Name != tc.name || c.Kind != tc.kind || c.Status != tc.status || c.CreatedBy != users[tc.creator].ID {
			t.Errorf("%s: name = %q, kind = %q, status = %q, created by %q; want %q, %q, %q, %s", tc.name, c.Name, c.Kind, c.Status, c.CreatedBy, tc.name, tc.kind, tc.status, tc.creator)
		}
		if c.EventsBlocked != tc.eventsBlocked || c.Locked {
			t.Errorf("%s: events blocked = %v, locked = %v; want %v, false", tc.name, c.EventsBlocked, c.Locked, tc.eventsBlocked)
		}
		if tc.status == domain.CollectiveStatusApproved {
			if c.ApprovedBy != users["admin"].ID {
				t.Errorf("%s: approved by %q, want the admin", tc.name, c.ApprovedBy)
			}
			wantWithin(t, tc.name+" ApprovedAt", c.ApprovedAt, before, after)
		} else if c.ApprovedBy != "" || c.ApprovedAt != nil {
			t.Errorf("%s: a pending collective already carries an approval: by %q at %v", tc.name, c.ApprovedBy, c.ApprovedAt)
		}
	}

	// Memberships, by user, in the order of the collective's name. adminBy and
	// ownerBy are the local parts of whoever approved each side, empty while the
	// side is open; the request is decided exactly when it is approved.
	type membership struct {
		collective       string
		role             domain.Role
		status           domain.MembershipStatus
		adminBy, ownerBy string
	}
	for local, want := range map[string][]membership{
		"dono": {
			{"Coletivo Alfa", domain.RoleOwner, domain.MembershipStatusApproved, "admin", "dono"},
			{"Produtora Beta", domain.RoleOwner, domain.MembershipStatusApproved, "admin", "dono"},
		},
		"membro":             {{"Coletivo Alfa", domain.RoleMember, domain.MembershipStatusApproved, "admin", "dono"}},
		"pendente-aprovacao": {{"Coletivo Alfa", domain.RoleMember, domain.MembershipStatusPending, "", ""}},
		// What a sign-up leaves for a new collective: the creator vouched for
		// themself, the admin has not decided.
		"novo-coletivo": {{"Coletivo Gama", domain.RoleOwner, domain.MembershipStatusPending, "", "novo-coletivo"}},
		// A request to join Beta as a Membro: neither side has decided.
		"pedido": {{"Produtora Beta", domain.RoleMember, domain.MembershipStatusPending, "", ""}},
		// The only Administrador of Delta, whose events are blocked.
		"delta": {{"Coletivo Delta", domain.RoleOwner, domain.MembershipStatusApproved, "admin", "delta"}},
		"admin": nil, "pendente-email": nil, "senha-vencida": nil, "senha-quase": nil,
	} {
		list, err := repos.Memberships.ListByUser(t.Context(), users[local].ID)
		if err != nil {
			t.Fatalf("list the memberships of %s: %v", local, err)
		}
		var got []membership
		for _, m := range list {
			localOf := func(id string) string {
				for l, u := range users {
					if u.ID == id {
						return l
					}
				}
				return "(" + id + ")"
			}
			var adminBy, ownerBy string
			if m.AdminApprovedBy != "" {
				adminBy = localOf(m.AdminApprovedBy)
			}
			if m.OwnerApprovedBy != "" {
				ownerBy = localOf(m.OwnerApprovedBy)
			}
			got = append(got, membership{m.CollectiveName, m.Role, m.Status, adminBy, ownerBy})
			if approved := m.Status == domain.MembershipStatusApproved; approved != (m.DecidedAt != nil) {
				t.Errorf("%s in %s: status %q with decided = %v; want a decision time exactly when approved", local, m.CollectiveName, m.Status, m.DecidedAt != nil)
			}
			if (m.AdminApprovedBy != "") != (m.AdminApprovedAt != nil) || (m.OwnerApprovedBy != "") != (m.OwnerApprovedAt != nil) {
				t.Errorf("%s in %s: an approval without its time or the other way round: %+v", local, m.CollectiveName, m.Membership)
			}
			wantWithin(t, local+" membership RequestedAt", &m.RequestedAt, before, after)
		}
		if !slices.Equal(got, want) {
			t.Errorf("memberships of %s = %v, want %v", local, got, want)
		}
	}
	// Only an approved Administrador of an approved collective with its events open
	// can create events; delta's collective is blocked, so delta cannot.
	for local, want := range map[string]bool{
		"dono": true, "membro": false, "pendente-aprovacao": false, "admin": false,
		"novo-coletivo": false, "pedido": false, "delta": false,
	} {
		if got, err := repos.Memberships.UserCanCreateEvents(t.Context(), users[local].ID); err != nil || got != want {
			t.Errorf("UserCanCreateEvents(%s) = %v, %v; want %v", local, got, err, want)
		}
	}

	// The admin's queue holds the three requests that wait for the admin's side.
	awaiting, err := repos.Memberships.ListAwaitingAdmin(t.Context(), domain.ListLimitMax)
	if err != nil {
		t.Fatalf("list the memberships awaiting the admin: %v", err)
	}
	var queue []string
	for _, m := range awaiting {
		queue = append(queue, m.CollectiveName+" <- "+m.UserID)
	}
	wantQueue := []string{
		"Coletivo Alfa <- " + users["pendente-aprovacao"].ID,
		"Coletivo Gama <- " + users["novo-coletivo"].ID,
		"Produtora Beta <- " + users["pedido"].ID,
	}
	slices.Sort(queue)
	slices.Sort(wantQueue)
	if !slices.Equal(queue, wantQueue) {
		t.Errorf("memberships awaiting the admin = %v, want %v", queue, wantQueue)
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

	// One confirmation token: novo-coletivo has already confirmed its e-mail.
	want := map[string]int64{"collectives": 4, "events": int64(len(seedEvents)), "memberships": 7, "outbox": 0, "sessions": 0, "tokens": 1, "users": 10}
	if got := docCounts(t, env); !reflect.DeepEqual(got, want) {
		t.Errorf("documents per collection = %v, want %v", got, want)
	}
}

// localDay is the calendar day of t in loc, as AAAA-MM-DD, which sorts as it reads.
func localDay(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02") }

// wantLateAfterMidnight fails unless e starts at 22:00 or later in loc and ends on
// a later day there.
func wantLateAfterMidnight(t *testing.T, e domain.EventView, loc *time.Location) {
	t.Helper()
	start := e.StartsAt.In(loc)
	end := e.StartsAt.Add(time.Duration(e.DurationMin) * time.Minute)
	if start.Hour() < 22 || localDay(end, loc) <= localDay(start, loc) {
		t.Errorf("%s starts at %v and ends at %v in %v; want a start at 22:00 or later and an end on the next day", e.Name, start, end.In(loc), loc)
	}
}

// Every sample event is a valid event of its collective, made by that collective's
// Administrador, with a style and a city from the catalog; together they show the
// cases the calendar, Analisar Data and the event page have to handle.
func TestSeedDevCreatesTheSampleEvents(t *testing.T) {
	env := mongoEnv(t)
	if code, _, stderr := runCmd(t, seedEnv(env), "", "seed-dev"); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q; want 0", code, stderr)
	}
	store := openTestStore(t, env)
	users := seedUsers(t, store)
	collectives := seedCollectivesByName(t, store)
	got := seedEventsByName(t, store)
	if len(got) != len(seedEvents) { // the collective that waits for the admin has none
		t.Errorf("%d sample events, want %d", len(got), len(seedEvents))
	}
	for _, want := range seedEvents {
		e, ok := got[want.name]
		if !ok {
			t.Errorf("there is no event %q", want.name)
			continue
		}
		if e.CollectiveID != collectives[want.collective].ID || e.CollectiveName != want.collective {
			t.Errorf("%s: in the collective %q (%s), want %s", want.name, e.CollectiveName, e.CollectiveID, want.collective)
		}
		if creator := users[want.creator].ID; e.CreatedBy != creator || e.UpdatedBy != creator {
			t.Errorf("%s: created by %q and updated by %q, want %s (%s)", want.name, e.CreatedBy, e.UpdatedBy, want.creator, creator)
		}
		if err := e.Event.Validate(); err != nil {
			t.Errorf("%s is not a valid event: %v", want.name, err)
		}
		if !catalog.ValidStyle(e.Style, e.Substyle) {
			t.Errorf("%s: the style %q with the substyle %q is not in the catalog", want.name, e.Style, e.Substyle)
		}
		if !catalog.ValidCity(e.State, e.City) {
			t.Errorf("%s: %q is not a city of %q in the catalog", want.name, e.City, e.State)
		}
	}

	// only returns the event called name, and fails unless it is the only one that
	// match selects.
	only := func(what, name string, match func(domain.EventView) bool) domain.EventView {
		t.Helper()
		var names []string
		for n, e := range got {
			if match(e) {
				names = append(names, n)
			}
		}
		slices.Sort(names)
		if !slices.Equal(names, []string{name}) {
			t.Errorf("the events %s are %q, want only %q", what, names, name)
		}
		return got[name]
	}
	only("with no ticket price", "Oficina de Discotecagem", func(e domain.EventView) bool { return e.Free })
	if e := only("of type outros", "Sarau na Praça", func(e domain.EventView) bool { return e.Type == domain.EventTypeOutros }); e.TypeOther != "Sarau" {
		t.Errorf("%s: type other = %q, want Sarau", e.Name, e.TypeOther)
	}
	if e := only("of style outros", "Baile de Forró Eletrônico", func(e domain.EventView) bool { return e.Style == domain.StyleOther }); e.StyleOther != "Forró eletrônico" || e.Substyle != "" {
		t.Errorf("%s: style other = %q, substyle = %q; want Forró eletrônico and none", e.Name, e.StyleOther, e.Substyle)
	}
	festival := got["Festival Nordeste Eletrônico"]
	if !strings.HasPrefix(festival.TicketURL, "https://") || festival.Venue == "" || strings.Count(festival.Artists, "\n") < 2 {
		t.Errorf("%s: ticket link = %q, venue = %q, artists = %q; want an https link, a venue and artists on several lines", festival.Name, festival.TicketURL, festival.Venue, festival.Artists)
	}
	// Coletivo Delta, whose events the admin blocked, has one: its Administrador
	// made it, and it was stored directly.
	if e := got["Noite Ambient no Terraço"]; !collectives["Coletivo Delta"].EventsBlocked || e.CollectiveName != "Coletivo Delta" {
		t.Errorf("%s: in %q, whose events blocked = %v; want Coletivo Delta, blocked", e.Name, e.CollectiveName, collectives["Coletivo Delta"].EventsBlocked)
	}
}

// The times of the sample events are in APP_TZ: the late one starts at 22:00 or
// later there, and ends the next day.
func TestSeedDevSampleEventsUseAppTZ(t *testing.T) {
	env := mongoEnv(t)
	if code, _, stderr := runCmd(t, seedEnv(env, "APP_TZ", "Asia/Tokyo"), "", "seed-dev"); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q; want 0", code, stderr)
	}
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Fatalf("load Asia/Tokyo: %v", err)
	}
	wantLateAfterMidnight(t, seedEventsByName(t, openTestStore(t, env))["After Hours Subsolo"], tokyo)
}

// The dates of the sample events are computed from the clock, in the zone of
// APP_TZ: made at another time they fall on other days, and the calendar has
// events before and after that day, this month and the next. One day has the
// three events of a clash. A later run leaves them where they were.
func TestSeedDevSampleEventDatesFollowNow(t *testing.T) {
	zone := func(name string) *time.Location {
		loc, err := time.LoadLocation(name)
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		return loc
	}
	fortaleza, tokyo := zone("America/Fortaleza"), zone("Asia/Tokyo")
	clocks := []struct {
		name string
		now  time.Time
		loc  *time.Location
	}{
		{"march", time.Date(2026, time.March, 15, 10, 30, 0, 0, fortaleza), fortaleza},
		{"august", time.Date(2026, time.August, 15, 18, 45, 0, 0, fortaleza), fortaleza},
		{"december in Tokyo", time.Date(2026, time.December, 15, 9, 0, 0, 0, tokyo), tokyo},
	}
	starts := make([]map[string]time.Time, len(clocks))
	for i, c := range clocks {
		t.Run(c.name, func(t *testing.T) {
			store := openTestStore(t, mongoEnv(t))
			if res := seedAt(t, store, c.now, c.loc); res.created != seedRecordCount {
				t.Errorf("created %d records, want %d", res.created, seedRecordCount)
			}
			events := seedEventsByName(t, store)
			month := func(day time.Time) string { return day.In(c.loc).Format("2006-01") }
			this, next := month(c.now), month(time.Date(c.now.Year(), c.now.Month()+1, 1, 0, 0, 0, 0, c.loc))

			var earlier, later, ahead int
			byDay := map[string][]domain.EventView{}
			starts[i] = map[string]time.Time{}
			for name, e := range events {
				starts[i][name] = e.StartsAt
				byDay[localDay(e.StartsAt, c.loc)] = append(byDay[localDay(e.StartsAt, c.loc)], e)
				switch {
				case month(e.StartsAt) == this && e.StartsAt.Before(c.now):
					earlier++
				case month(e.StartsAt) == this:
					later++
				case month(e.StartsAt) == next:
					ahead++
				default:
					t.Errorf("%s starts at %v, in neither this month nor the next", name, e.StartsAt.In(c.loc))
				}
			}
			if earlier == 0 || later == 0 || ahead == 0 {
				t.Errorf("%d events earlier this month, %d later this month and %d next month, want some of each", earlier, later, ahead)
			}

			// Analisar Data: one day with two events in Fortaleza and one in Recife.
			var busy []string
			for day, list := range byDay {
				if len(list) > 1 {
					busy = append(busy, day)
				}
			}
			if len(busy) != 1 || len(byDay[busy[0]]) != 3 {
				t.Fatalf("the days with several events are %v, want one day with 3 events", busy)
			}
			clash, err := time.ParseInLocation("2006-01-02", busy[0], c.loc)
			if err != nil {
				t.Fatalf("parse %s: %v", busy[0], err)
			}
			today := time.Date(c.now.Year(), c.now.Month(), c.now.Day(), 0, 0, 0, 0, c.loc)
			if n := int(clash.Sub(today) / (24 * time.Hour)); n < 2 || n > 5 {
				t.Errorf("the clash is %d days from today, want a few (2 to 5)", n)
			}
			var places []string
			for _, e := range byDay[busy[0]] {
				places = append(places, e.City+"/"+e.State)
			}
			slices.Sort(places)
			if want := []string{"Fortaleza/CE", "Fortaleza/CE", "Recife/PE"}; !slices.Equal(places, want) {
				t.Errorf("the events of the clash day are in %v, want %v", places, want)
			}
			day, err := store.Repos().Events.ListBetween(t.Context(), domain.EventFilter{From: clash.UTC(), To: clash.AddDate(0, 0, 1).UTC()}, 10)
			if err != nil || len(day) != 3 {
				t.Errorf("ListBetween(the clash day) = %d events, %v; want 3", len(day), err)
			}

			wantLateAfterMidnight(t, events["After Hours Subsolo"], c.loc)

			// A run on a later day finds every record and moves none.
			again := seedAt(t, store, c.now.Add(10*24*time.Hour), c.loc)
			if again.created != 0 || again.existing != seedRecordCount {
				t.Errorf("a later run: %d created, %d existing; want 0 and %d", again.created, again.existing, seedRecordCount)
			}
			if after := seedEventsByName(t, store); !reflect.DeepEqual(after, events) {
				t.Errorf("a later run changed an event:\nbefore %+v\nafter  %+v", events, after)
			}
		})
	}
	for name, first := range starts[0] {
		for i := 1; i < len(starts); i++ {
			if other, ok := starts[i][name]; ok && other.Sub(first).Abs() < 60*24*time.Hour {
				t.Errorf("%s starts at %v and at %v for clocks months apart: its date does not follow the clock", name, first, other)
			}
		}
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
	collectivesBefore := seedCollectivesByName(t, store)
	eventsBefore := seedEventsByName(t, store)
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
	if want := fmt.Sprintf("0 created, %d existing", seedRecordCount); !strings.Contains(stdout, want) {
		t.Errorf("stdout has no summary of %q:\n%s", want, stdout)
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
	if collectivesAfter := seedCollectivesByName(t, store); !reflect.DeepEqual(collectivesAfter, collectivesBefore) {
		t.Errorf("the second run changed a collective:\nbefore %+v\nafter  %+v", collectivesBefore, collectivesAfter)
	}
	if eventsAfter := seedEventsByName(t, store); !reflect.DeepEqual(eventsAfter, eventsBefore) {
		t.Errorf("the second run changed an event:\nbefore %+v\nafter  %+v", eventsBefore, eventsAfter)
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
	if want := fmt.Sprintf("%d created, 1 existing", seedRecordCount-1); !strings.Contains(stdout, want) {
		t.Errorf("stdout has no summary of %q:\n%s", want, stdout)
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
