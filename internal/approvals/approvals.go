package approvals

import (
	"context"
	"errors"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
	"github.com/magalz/agenda-clubber-lite/internal/mail"
)

// Stubs for the RED commit: they compile and do nothing, so every test fails on
// its own assertions. The next commit replaces this file.

var (
	ErrNotAllowed = errors.New("approvals: not allowed")
	ErrDecided    = errors.New("approvals: already decided")
	ErrLocked     = errors.New("approvals: locked")
	ErrMaster     = errors.New("approvals: master")
	ErrLastOwner  = errors.New("approvals: last owner")
	ErrSelf       = errors.New("approvals: self")
)

func RoleLabel(r domain.Role) string { return "" }

func CheckReason(reason string) (string, error) { return "", nil }

func requireAdmin(actor domain.User) error { return nil }

func wrap(step string, err error) error { return nil }

func auditBy(action domain.AuditAction, actor domain.User, now time.Time) domain.AuditEntry {
	return domain.AuditEntry{}
}

func audit(ctx context.Context, r domain.Repos, e domain.AuditEntry) error { return nil }

func enqueue(ctx context.Context, r domain.Repos, to string, t mail.Template, data any, now time.Time) error {
	return nil
}

type decision struct{}

func newDecision(to, name string) *decision { return &decision{} }

func (d *decision) add(what, outcome, reason string) {}

func (d *decision) send(ctx context.Context, r domain.Repos, link string, now time.Time) error {
	return nil
}

func notifyAdmins(ctx context.Context, r domain.Repos, data mail.AdminRequestData, now time.Time) error {
	return nil
}

func notifyOwners(ctx context.Context, r domain.Repos, collectiveID string, data mail.OwnerJoinRequestData, now time.Time) error {
	return nil
}

const whatAccount = ""

func whatCollective(name string) string { return "" }

func whatJoin(collectiveName string, role domain.Role) string { return "" }
