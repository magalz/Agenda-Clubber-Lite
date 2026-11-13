package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/magalz/agenda-clubber-lite/internal/auth"
	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const createAdminUsage = "usage: agenda create-admin --name <name> --email <email> --phone <digits>   (the password is the first line of stdin)"

// maxPasswordLine bounds what is read from stdin. A password has at most
// auth.MaxLength runes, so a line this long breaks the policy whatever the
// characters, and cutting it here cannot make it pass.
const maxPasswordLine = 4096

// ruleLabels joins the pt-BR labels of the rules a password breaks.
func ruleLabels(violated []auth.Rule) string {
	labels := make([]string, len(violated))
	for i, rule := range violated {
		labels[i] = auth.RuleLabels[rule]
	}
	return strings.Join(labels, ", ")
}

// readPassword returns the first line of r without its line ending: only the
// final LF and the CR before it are removed, so spaces stay part of the password.
// Input with no line ending is the password as it is.
func readPassword(r io.Reader) (string, error) {
	line, err := bufio.NewReader(io.LimitReader(r, maxPasswordLine)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

// createAdmin creates an active admin account from --name, --email and --phone,
// with the password read from stdin. It reads only the MongoDB settings. It
// returns 0 on success, 1 when the account cannot be created and 2 when it is
// called wrongly. Neither the password nor the database URI reaches an output.
func createAdmin(ctx context.Context, args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, createAdminUsage) }
	name := fs.String("name", "", "full name")
	email := fs.String("email", "", "e-mail address, the login")
	phone := fs.String("phone", "", "phone number; only its digits are kept")
	if err := fs.Parse(args); err != nil {
		return 2 // the flag package has said why, and -h
	}
	var missing []string
	for _, f := range []struct{ flag, value string }{{"--name", *name}, {"--email", *email}, {"--phone", *phone}} {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, f.flag)
		}
	}
	if len(missing) > 0 || fs.NArg() > 0 {
		if len(missing) > 0 {
			fmt.Fprintf(stderr, "agenda: create-admin: missing %s\n", strings.Join(missing, ", "))
		} else {
			fmt.Fprintf(stderr, "agenda: create-admin: unexpected argument %q\n", fs.Arg(0))
		}
		fs.Usage()
		return 2
	}

	password, err := readPassword(stdin)
	if err != nil {
		fmt.Fprintln(stderr, "agenda: create-admin: cannot read the password from stdin")
		return 1
	}
	u, err := newAdmin(*name, *email, *phone, password, time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		fmt.Fprintf(stderr, "agenda: create-admin: %v\n", err)
		return 1
	}

	cfg, err := config.LoadMongo(getenv)
	if err != nil {
		fmt.Fprintf(stderr, "agenda: create-admin: %v\n", err)
		return 1
	}
	store, err := openStore(ctx, cfg)
	if err != nil {
		fmt.Fprintf(stderr, "agenda: create-admin: %v\n", err)
		return 1
	}
	defer func() {
		if err := closeStore(store); err != nil {
			fmt.Fprintln(stderr, "agenda: create-admin: closing the database failed")
		}
	}()

	var fields domain.FieldErrors
	switch err := store.Repos().Users.Create(ctx, &u); {
	case err == nil:
	case errors.Is(err, domain.ErrEmailTaken):
		fmt.Fprintln(stderr, "agenda: create-admin: this email already has an account")
		return 1
	case errors.As(err, &fields):
		fmt.Fprintf(stderr, "agenda: create-admin: %v\n", fields)
		return 1
	default: // the driver's text may name the server
		fmt.Fprintln(stderr, "agenda: create-admin: cannot create the account")
		return 1
	}
	fmt.Fprintf(stdout, "admin created: %s\n", u.Email)
	return 0
}

// newAdmin checks the input and returns the admin account it describes, active
// and with its e-mail confirmed, as of now. The name and the e-mail are trimmed
// and the phone keeps only its digits, as at sign-up. An error is safe to print:
// it names fields and rules, never the password.
func newAdmin(name, email, phone, password string, now time.Time) (domain.User, error) {
	name = strings.TrimSpace(name)
	switch {
	case !utf8.ValidString(name):
		return domain.User{}, domain.FieldErrors{"name": domain.MsgInvalid}
	case !utf8.ValidString(password):
		return domain.User{}, domain.FieldErrors{"password": domain.MsgInvalid}
	}
	email, err := domain.NormalizeEmail(email)
	if err != nil {
		return domain.User{}, err
	}
	phone = strings.Map(func(r rune) rune {
		if r < '0' || r > '9' {
			return -1
		}
		return r
	}, phone)
	if violated := auth.CheckPolicy(password, email); len(violated) > 0 {
		return domain.User{}, fmt.Errorf("password does not meet the rules: %s", ruleLabels(violated))
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return domain.User{}, errors.New("cannot hash the password")
	}
	return domain.User{
		Name:              name,
		Email:             email,
		Phone:             phone,
		PasswordHash:      hash,
		PasswordChangedAt: now,
		Status:            domain.UserStatusActive,
		IsAdmin:           true,
		EmailVerifiedAt:   &now,
		CreatedAt:         now,
		UpdatedAt:         now,
	}, nil
}
