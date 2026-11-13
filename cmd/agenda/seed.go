package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/accounts"
	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const (
	devOrigin         = "http://localhost:8120" // the link's origin when APP_ORIGIN is unset
	devEmailDomain    = "@agenda.test"
	devPreviousPrefix = "Anterior-" // the previous password of senha-vencida is this and SEED_PASSWORD
	oneDay            = 24 * time.Hour
)

// devAccount is a sample account. age is how long ago it was made, and with it
// approved and its e-mail confirmed; its password dates from then too, unless
// rotated: then it was made with another password and changed it a day later.
type devAccount struct {
	local, name, phone string
	status             domain.UserStatus
	admin, verified    bool
	age                time.Duration
	rotated            bool
}

// The first account is the admin: the others are approved by it.
var devAccounts = []devAccount{
	{local: "admin", name: "Admin Dev", phone: "85999990100", status: domain.UserStatusActive, admin: true, verified: true},
	{local: "dono", name: "Dono Dev", phone: "85999990101", status: domain.UserStatusActive, verified: true},
	{local: "membro", name: "Membro Dev", phone: "85999990102", status: domain.UserStatusActive, verified: true},
	{local: "pendente-email", name: "Pendente Email", phone: "85999990103", status: domain.UserStatusPendingEmail},
	{local: "pendente-aprovacao", name: "Pendente Aprovacao", phone: "85999990104", status: domain.UserStatusPendingApproval, verified: true},
	{local: "senha-vencida", name: "Senha Vencida", phone: "85999990105", status: domain.UserStatusActive, verified: true, age: 92 * oneDay, rotated: true},
	{local: "senha-quase", name: "Senha Quase", phone: "85999990106", status: domain.UserStatusActive, verified: true, age: 87 * oneDay},
}

// devCollectives are made by dono and approved by the admin.
var devCollectives = []struct {
	name string
	kind domain.CollectiveKind
}{
	{"Coletivo Alfa", domain.CollectiveKindColetivo},
	{"Produtora Beta", domain.CollectiveKindProdutora},
}

// devMemberships: the approved ones are decided by the admin and dono.
var devMemberships = []struct {
	local, collective string
	role              domain.Role
	status            domain.MembershipStatus
}{
	{"dono", "Coletivo Alfa", domain.RoleOwner, domain.MembershipStatusApproved},
	{"dono", "Produtora Beta", domain.RoleOwner, domain.MembershipStatusApproved},
	{"membro", "Coletivo Alfa", domain.RoleMember, domain.MembershipStatusApproved},
	{"pendente-aprovacao", "Coletivo Alfa", domain.RoleMember, domain.MembershipStatusPending},
}

func devEmail(local string) string { return local + devEmailDomain }

// seedPlan is what a run writes with. The hashes are made before the transaction
// opens: they are slow, and the transaction may run again.
type seedPlan struct {
	now                time.Time
	origin             string
	hash, previousHash string // of SEED_PASSWORD, and of devPreviousPrefix and SEED_PASSWORD
}

// seedResult says what a pass over the records did.
type seedResult struct {
	lines             []string
	created, existing int
	link              string // the confirmation link of pendente-email, when it was created
}

func (s *seedResult) record(what string, created bool) {
	state := "exists"
	if created {
		state = "created"
		s.created++
	} else {
		s.existing++
	}
	s.lines = append(s.lines, what+": "+state)
}

// seedDev fills a development database with sample accounts and collectives.
// It runs only with APP_ENV=dev and SEED_PASSWORD set, and is safe to run again:
// a record that is already there is left as it is. Every record is written in one
// transaction, so a failed run leaves nothing half done. It reads the MongoDB
// settings, APP_ORIGIN (for the confirmation link), APP_ENV and SEED_PASSWORD.
// Neither the password nor the database URI reaches an output.
func seedDev(ctx context.Context, getenv func(string) string, stdout, stderr io.Writer) int {
	fail := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "agenda: seed-dev: "+format+"\n", args...)
		return 1
	}
	if strings.TrimSpace(getenv("APP_ENV")) != "dev" {
		return fail("APP_ENV must be dev")
	}
	password := getenv("SEED_PASSWORD")
	if password == "" {
		return fail("SEED_PASSWORD is not set")
	}
	for _, a := range devAccounts {
		if violated := auth.CheckPolicy(password, devEmail(a.local)); len(violated) > 0 {
			return fail("SEED_PASSWORD does not meet the rules for %s: %s", devEmail(a.local), ruleLabels(violated))
		}
	}

	cfg, err := config.LoadMongo(getenv)
	if err != nil {
		return fail("%v", err)
	}
	store, err := openStore(ctx, cfg)
	if err != nil {
		return fail("%v", err)
	}
	defer func() {
		if err := closeStore(store); err != nil {
			fmt.Fprintln(stderr, "agenda: seed-dev: closing the database failed")
		}
	}()

	plan := seedPlan{now: time.Now().UTC().Truncate(time.Millisecond), origin: devOrigin}
	if origin := strings.TrimRight(strings.TrimSpace(getenv("APP_ORIGIN")), "/"); origin != "" {
		plan.origin = origin
	}
	if plan.hash, err = auth.HashPassword(password); err != nil {
		return fail("cannot hash the password")
	}
	if plan.previousHash, err = auth.HashPassword(devPreviousPrefix + password); err != nil {
		return fail("cannot hash the password")
	}

	var res seedResult
	err = store.WithTx(ctx, func(ctx context.Context, r domain.Repos) (err error) {
		res, err = seedRecords(ctx, r, plan) // a new result on every run of the function
		return err
	})
	if err != nil { // the driver's text may name the server
		return fail("cannot write the sample data")
	}

	for _, line := range res.lines {
		fmt.Fprintln(stdout, line)
	}
	if res.link != "" {
		fmt.Fprintf(stdout, "confirmation link for %s: %s\n", devEmail("pendente-email"), res.link)
	}
	fmt.Fprintf(stdout, "seed-dev: ready (%d created, %d existing)\n", res.created, res.existing)
	return 0
}

// seedRecords creates the sample records that are not there yet, through r. It
// reads before it writes (a failed write may leave a transaction unusable), keeps
// no state between calls and prints nothing, so the transaction may run it again.
func seedRecords(ctx context.Context, r domain.Repos, p seedPlan) (seedResult, error) {
	var res seedResult
	userIDs := map[string]string{}
	for _, a := range devAccounts {
		email := devEmail(a.local)
		u, err := r.Users.ByEmail(ctx, email)
		switch {
		case err == nil:
			res.record("user "+email, false)
		case errors.Is(err, domain.ErrNotFound):
			if u, err = p.createUser(ctx, r, a, userIDs["admin"]); err != nil {
				return seedResult{}, err
			}
			if a.status == domain.UserStatusPendingEmail {
				if res.link, err = p.createConfirmationLink(ctx, r, u.ID); err != nil {
					return seedResult{}, err
				}
			}
			res.record("user "+email, true)
		default:
			return seedResult{}, err
		}
		userIDs[a.local] = u.ID
	}

	collectiveIDs := map[string]string{}
	for _, want := range devCollectives {
		c, err := r.Collectives.ByNameKey(ctx, domain.NameKey(want.name))
		switch {
		case err == nil:
			res.record("collective "+want.name, false)
		case errors.Is(err, domain.ErrNotFound):
			approvedAt := p.now
			c = domain.Collective{
				Name: want.name, NameKey: domain.NameKey(want.name), Kind: want.kind, Status: domain.CollectiveStatusApproved,
				CreatedBy: userIDs["dono"], ApprovedBy: userIDs["admin"], ApprovedAt: &approvedAt, CreatedAt: p.now, UpdatedAt: p.now,
			}
			if err := r.Collectives.Create(ctx, &c); err != nil {
				return seedResult{}, err
			}
			res.record("collective "+want.name, true)
		default:
			return seedResult{}, err
		}
		collectiveIDs[want.name] = c.ID
	}

	for _, want := range devMemberships {
		what := fmt.Sprintf("membership %s in %s", devEmail(want.local), want.collective)
		have, err := r.Memberships.ListByUser(ctx, userIDs[want.local])
		if err != nil {
			return seedResult{}, err
		}
		if slices.ContainsFunc(have, func(v domain.MembershipView) bool { return v.CollectiveID == collectiveIDs[want.collective] }) {
			res.record(what, false)
			continue
		}
		m := domain.Membership{
			UserID: userIDs[want.local], CollectiveID: collectiveIDs[want.collective],
			Role: want.role, Status: want.status, RequestedAt: p.now,
		}
		if want.status == domain.MembershipStatusApproved {
			adminAt, ownerAt, decidedAt := p.now, p.now, p.now
			m.AdminApprovedBy, m.AdminApprovedAt = userIDs["admin"], &adminAt
			m.OwnerApprovedBy, m.OwnerApprovedAt = userIDs["dono"], &ownerAt
			m.DecidedAt = &decidedAt
		}
		if err := r.Memberships.Create(ctx, m); err != nil {
			return seedResult{}, err
		}
		res.record(what, true)
	}
	return res, nil
}

// createUser stores the account a. Active accounts other than the admin are
// approved by adminID. An account that is rotated is made with the previous
// password and changes to the current one a day later.
func (p seedPlan) createUser(ctx context.Context, r domain.Repos, a devAccount, adminID string) (domain.User, error) {
	at := p.now.Add(-a.age)
	u := domain.User{
		Name: a.name, Email: devEmail(a.local), Phone: a.phone, PasswordHash: p.hash, PasswordChangedAt: at,
		Status: a.status, IsAdmin: a.admin, CreatedAt: at, UpdatedAt: at,
	}
	if a.rotated {
		u.PasswordHash = p.previousHash
	}
	if a.verified {
		verifiedAt := at
		u.EmailVerifiedAt = &verifiedAt
	}
	if a.status == domain.UserStatusActive && !a.admin {
		approvedAt := at
		u.ApprovedBy, u.ApprovedAt = adminID, &approvedAt
	}
	if err := r.Users.Create(ctx, &u); err != nil {
		return domain.User{}, err
	}
	if a.rotated {
		if err := r.Users.ChangePassword(ctx, u.ID, p.hash, at.Add(oneDay)); err != nil {
			return domain.User{}, err
		}
	}
	return u, nil
}

// createConfirmationLink stores an e-mail confirmation token for the user and
// returns the link that carries it.
func (p seedPlan) createConfirmationLink(ctx context.Context, r domain.Repos, userID string) (string, error) {
	raw, hash, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	t := domain.Token{
		UserID: userID, Purpose: domain.TokenPurposeVerifyEmail, Hash: hash,
		ExpiresAt: p.now.Add(accounts.SignupTokenTTL), CreatedAt: p.now,
	}
	if err := r.Tokens.Create(ctx, &t); err != nil {
		return "", err
	}
	return p.origin + "/confirmar?token=" + raw, nil
}
