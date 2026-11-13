package approvals

import (
	"context"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Stubs: the RED commit compiles the tests; the next commit implements them.

func AdminApproveJoin(ctx context.Context, store domain.Store, origin string, actor domain.User, userID, collectiveID string, now time.Time) error {
	return nil
}

func OwnerApproveJoin(ctx context.Context, store domain.Store, origin string, actor domain.User, userID, collectiveID string, now time.Time) error {
	return nil
}

func RejectJoin(ctx context.Context, store domain.Store, origin string, actor domain.User, userID, collectiveID, reason string, now time.Time) error {
	return nil
}

func Promote(ctx context.Context, store domain.Store, actor domain.User, userID, collectiveID string, now time.Time) error {
	return nil
}

func Demote(ctx context.Context, store domain.Store, actor domain.User, userID, collectiveID string, now time.Time) error {
	return nil
}

type CollectivePage struct {
	Collective domain.Collective
	Members    []domain.MemberView
	Requests   []domain.MemberView
}

func (p CollectivePage) IsMaster(userID string) bool { return false }

func CollectiveView(ctx context.Context, store domain.Store, actor domain.User, collectiveID string) (CollectivePage, error) {
	return CollectivePage{}, nil
}

func adminApproveJoinTx(ctx context.Context, r domain.Repos, actor, requester domain.User, c domain.Collective, m domain.Membership, d *decision, now time.Time) error {
	return nil
}

func rejectJoinTx(ctx context.Context, r domain.Repos, actor, requester domain.User, c domain.Collective, m domain.Membership, reason string, d *decision, now time.Time) error {
	return nil
}
