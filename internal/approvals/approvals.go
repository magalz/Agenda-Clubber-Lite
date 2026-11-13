// Package approvals holds the approval flows: the site admin's decisions on
// collectives and accounts, and the join, role and rename flows of a
// collective's members. Each flow is a service function that takes the domain
// store, the acting user and the current time, runs its writes in one
// Store.WithTx and returns one of the errors below, or domain.FieldErrors for
// input the person can fix.
//
// The people involved, as the product names them: a site administrator is the
// Admin (User.IsAdmin); a collective has Membros and Administradores (the stored
// role owner), and its Master is the creator (Collective.CreatedBy), who is
// never demoted.
//
// Every decision emails the person (one email for everything decided in one
// call) and writes one audit entry. The email is rendered and queued in the same
// transaction as the writes, so it exists exactly when the decision does.
//
// For data access this package uses only internal/domain, never a database
// driver or a store implementation; it also uses internal/mail to render
// emails. A flow that depends on a collective's state reads the collective in
// its transaction and writes it back there (see the conventions at the top of
// internal/domain/repo.go). Files and test fixtures carry the name of their flow
// (collectives…, join…, requests…, accounts…), so flows written in parallel do
// not collide.
package approvals

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// The errors every flow returns, unwrapped, for the web layer to match with
// errors.Is. An error that is none of these (and not a domain.FieldErrors or a
// domain.ErrNotFound) is a server error.
var (
	// ErrNotAllowed means the actor may not do this: not an active site admin
	// where only the admin may act, not an approved Administrador of the
	// collective, or the target is not in a state that takes the action.
	ErrNotAllowed = errors.New("approvals: not allowed")
	// ErrDecided means the request was already decided (or is no longer open).
	ErrDecided = errors.New("approvals: already decided")
	// ErrLocked means the collective is locked: it takes no join decision,
	// promotion, demotion or rename.
	ErrLocked = errors.New("approvals: collective is locked")
	// ErrMaster means the action would change the Master, who is never demoted.
	ErrMaster = errors.New("approvals: the master cannot be changed")
	// ErrLastOwner means the action would leave the collective without an
	// approved Administrador.
	ErrLastOwner = errors.New("approvals: last administrator")
	// ErrSelf means the actor tried to decide about themself.
	ErrSelf = errors.New("approvals: cannot act on yourself")
)

// Paths, relative to the site origin, the emails link to: the person's
// collectives (where they see the outcome of a request), the login page (for an
// outcome that leaves nothing to show there, such as a rejection), and the admin
// area (the requests, and the collectives, the site admins act on).
const (
	pathCollectives      = "/coletivos"
	pathLogin            = "/entrar"
	pathAdmin            = "/admin"
	pathAdminCollectives = "/admin/coletivos"
)

// collectivePath is the page of one collective, where its Administradores see
// the requests to join it.
func collectivePath(id string) string { return "/coletivo/" + id }

// RoleLabel returns the pt-BR name of a stored role: Administrador for owner and
// Membro for member. A role that is none of them is returned as stored, so a
// caller's bug shows in the text and is never passed off as a role the person
// does not have.
func RoleLabel(r domain.Role) string {
	switch r {
	case domain.RoleOwner:
		return "Administrador"
	case domain.RoleMember:
		return "Membro"
	}
	return string(r)
}

// CheckReason cleans the optional reason of a decision and returns it, or
// domain.FieldErrors with the key "reason" when it cannot be stored. It trims
// the text, turns CRLF and CR into LF and accepts at most domain.AuditTextMax
// runes (counted after trimming) of valid UTF-8 without control characters,
// except LF and tab inside the text. An empty reason is allowed.
func CheckReason(reason string) (string, error) {
	var fe domain.FieldErrors
	if !utf8.ValidString(reason) {
		fe.Add("reason", msgReasonText)
		return "", fe.Err()
	}
	reason = strings.ReplaceAll(reason, "\r\n", "\n")
	reason = strings.ReplaceAll(reason, "\r", "\n")
	reason = strings.TrimSpace(reason)
	if strings.ContainsFunc(reason, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		fe.Add("reason", msgReasonText)
		return "", fe.Err()
	}
	if utf8.RuneCountInString(reason) > domain.AuditTextMax {
		fe.Add("reason", fmt.Sprintf("use no máximo %d caracteres", domain.AuditTextMax))
		return "", fe.Err()
	}
	return reason, nil
}

const msgReasonText = "use só texto, sem caracteres de controle"

// requireAdmin returns ErrNotAllowed unless actor is an active site admin. The
// caller passes the user it loaded for the request, so a user disabled since
// the session started is refused here.
func requireAdmin(actor domain.User) error {
	if actor.ID == "" || !actor.IsAdmin || actor.Status != domain.UserStatusActive {
		return ErrNotAllowed
	}
	return nil
}

// requireOwner returns the actor's membership in the collective when the actor
// has an active account and is an approved Administrador there (role owner,
// status approved), read through r so that, inside the caller's transaction, it
// sees the role as it is now. Anything else, an unknown collective included, is
// ErrNotAllowed: the caller never learns more than "not yours".
func requireOwner(ctx context.Context, r domain.Repos, actor domain.User, collectiveID string) (domain.Membership, error) {
	if actor.ID == "" || actor.Status != domain.UserStatusActive {
		return domain.Membership{}, ErrNotAllowed
	}
	m, err := r.Memberships.Get(ctx, actor.ID, collectiveID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.Membership{}, ErrNotAllowed
	}
	if err != nil {
		return domain.Membership{}, wrap("read the actor's membership", err)
	}
	if m.Role != domain.RoleOwner || m.Status != domain.MembershipStatusApproved {
		return domain.Membership{}, ErrNotAllowed
	}
	return m, nil
}

// wrap names the step of a flow that failed, for example "approve collective:
// read collective". A domain.FieldErrors from the store means the flow built an
// entity the store refuses, which is a bug and not something the person can
// fix, so it is flattened to text and no longer matches errors.As. Any other
// error stays matchable with errors.Is (domain.ErrNotFound included).
func wrap(step string, err error) error {
	var fe domain.FieldErrors
	if errors.As(err, &fe) {
		return fmt.Errorf("approvals: %s: the store rejected a validated entity: %s", step, fe.Error())
	}
	return fmt.Errorf("approvals: %s: %w", step, err)
}

// auditBy starts the audit entry of an action by actor at now; the caller fills
// in the person, the collective, the detail and the reason that apply. The entry
// keeps names and IDs, never an email address.
func auditBy(action domain.AuditAction, actor domain.User, now time.Time) domain.AuditEntry {
	return domain.AuditEntry{At: now, Action: action, ActorID: actor.ID, ActorName: actor.Name}
}

// audit writes e inside the caller's transaction.
func audit(ctx context.Context, r domain.Repos, e domain.AuditEntry) error {
	if err := r.AuditEntries.Create(ctx, &e); err != nil {
		return wrap("write audit entry", err)
	}
	return nil
}

// enqueue renders template t with data and queues the email for to, due at now.
// Call it inside the transaction of the decision it reports.
func enqueue(ctx context.Context, r domain.Repos, to string, t mail.Template, data any, now time.Time) error {
	subject, text, html, err := mail.Render(t, data)
	if err != nil {
		return wrap("render email", err)
	}
	msg := domain.OutboxMessage{To: to, Subject: subject, Text: text, HTML: html, NextAttemptAt: now, CreatedAt: now}
	if err := r.Outbox.Enqueue(ctx, &msg); err != nil {
		return wrap("enqueue email", err)
	}
	return nil
}

// decision collects what one call decided about one person, so the person gets
// one email for all of it. Build it inside the transaction (the transaction
// body may run more than once), add an item for each decision, then send it
// before the transaction ends.
type decision struct {
	to, name string
	items    []mail.DecisionItem
}

// newDecision starts the decision email for the person with this address and
// name. An empty to (the person was deleted) makes send queue nothing.
func newDecision(to, name string) *decision {
	return &decision{to: to, name: name}
}

// add records one thing decided: what is the pt-BR subject (whatAccount,
// whatCollective, whatJoin), outcome one of mail.DecisionApproved,
// mail.DecisionRejected or mail.DecisionWaiting, and reason the optional text of
// whoever decided.
func (d *decision) add(what, outcome, reason string) {
	d.items = append(d.items, mail.DecisionItem{What: what, Outcome: outcome, Reason: reason})
}

// send queues the email, linking to link. It queues nothing when no item was
// added or the person has no address.
func (d *decision) send(ctx context.Context, r domain.Repos, link string, now time.Time) error {
	if d.to == "" || len(d.items) == 0 {
		return nil
	}
	return enqueue(ctx, r, d.to, mail.TemplateDecision, mail.DecisionData{Name: d.name, Items: d.items, Link: link}, now)
}

// notifyAdmins queues the request notice for every active site admin.
func notifyAdmins(ctx context.Context, r domain.Repos, data mail.AdminRequestData, now time.Time) error {
	emails, err := r.Users.ListAdminEmails(ctx)
	if err != nil {
		return wrap("list admin emails", err)
	}
	for _, to := range emails {
		if err := enqueue(ctx, r, to, mail.TemplateAdminRequest, data, now); err != nil {
			return err
		}
	}
	return nil
}

// notifyOwners queues the join request notice for every approved Administrador
// of the collective whose account is active.
func notifyOwners(ctx context.Context, r domain.Repos, collectiveID string, data mail.OwnerJoinRequestData, now time.Time) error {
	emails, err := r.Memberships.OwnerEmails(ctx, collectiveID)
	if err != nil {
		return wrap("list owner emails", err)
	}
	for _, to := range emails {
		if err := enqueue(ctx, r, to, mail.TemplateOwnerJoinRequest, data, now); err != nil {
			return err
		}
	}
	return nil
}

// The subjects of a decision item, in pt-BR, as the email prints them before the
// outcome ("o coletivo Alfa: aprovado").

// whatAccount is the person's own account.
const whatAccount = "sua conta"

// whatCollective is a collective the person asked to create: "a produtora X"
// for a produtora, "o coletivo X" for anything else.
func whatCollective(name string, kind domain.CollectiveKind) string {
	if kind == domain.CollectiveKindProdutora {
		return "a produtora " + name
	}
	return "o coletivo " + name
}

// whatJoin is the person's request to join a collective in a role.
func whatJoin(collectiveName string, role domain.Role) string {
	return "seu pedido para entrar em " + collectiveName + " como " + RoleLabel(role)
}
