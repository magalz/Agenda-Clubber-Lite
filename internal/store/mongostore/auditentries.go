package mongostore

import (
	"context"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

type auditEntries struct{ s *Store }

var _ domain.AuditEntries = auditEntries{}

// Create is a stub until T3.5 implements it.
func (r auditEntries) Create(ctx context.Context, e *domain.AuditEntry) error {
	return errNotImplemented
}

// List is a stub until T3.5 implements it.
func (r auditEntries) List(ctx context.Context, limit int) ([]domain.AuditEntry, error) {
	return nil, errNotImplemented
}
