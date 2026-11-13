package approvals

import (
	"context"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Stubs for the RED commit: they compile and do nothing, so every test fails on
// its own assertions. The next commit replaces this file.

func ApproveCollective(ctx context.Context, store domain.Store, origin string, actor domain.User, collectiveID string, now time.Time) error {
	return nil
}

func RejectCollective(ctx context.Context, store domain.Store, origin string, actor domain.User, collectiveID, reason string, now time.Time) error {
	return nil
}

func SetCollectiveBlocks(ctx context.Context, store domain.Store, actor domain.User, collectiveID string, eventsBlocked, locked bool, reason string, now time.Time) error {
	return nil
}

func approveCollectiveTx(ctx context.Context, r domain.Repos, actor, creator domain.User, c domain.Collective, d *decision, now time.Time) error {
	return nil
}

func rejectCollectiveTx(ctx context.Context, r domain.Repos, actor, creator domain.User, c domain.Collective, reason string, d *decision, now time.Time) error {
	return nil
}
