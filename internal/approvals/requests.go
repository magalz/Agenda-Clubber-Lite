package approvals

import (
	"context"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// RequestJoin is a stub, replaced by the implementation.
func RequestJoin(ctx context.Context, store domain.Store, origin string, actor domain.User, collectiveID string, role domain.Role, now time.Time) error {
	return nil
}

// RequestCollective is a stub, replaced by the implementation.
func RequestCollective(ctx context.Context, store domain.Store, origin string, actor domain.User, name string, kind domain.CollectiveKind, now time.Time) error {
	return nil
}

// RenameCollective is a stub, replaced by the implementation.
func RenameCollective(ctx context.Context, store domain.Store, origin string, actor domain.User, collectiveID, name string, now time.Time) error {
	return nil
}
