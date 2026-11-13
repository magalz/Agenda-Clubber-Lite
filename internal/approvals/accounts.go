package approvals

import (
	"context"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// PersonCard is one person in the site admin's queue: the account and the
// requests of the person that wait for the admin.
type PersonCard struct {
	User  domain.User
	Items []domain.MembershipView
}

// AccountPending reports whether the account still awaits the admin.
func (p PersonCard) AccountPending() bool { return p.User.Status == domain.UserStatusPendingApproval }

func ApproveAccount(ctx context.Context, store domain.Store, origin string, actor domain.User, userID string, now time.Time) error {
	return nil
}

func ApprovePerson(ctx context.Context, store domain.Store, origin string, actor domain.User, userID string, now time.Time) error {
	return nil
}

func RejectAccount(ctx context.Context, store domain.Store, origin string, actor domain.User, userID, reason string, now time.Time) error {
	return nil
}

func BlockAccount(ctx context.Context, store domain.Store, actor domain.User, userID, reason string, now time.Time) error {
	return nil
}

func UnblockAccount(ctx context.Context, store domain.Store, actor domain.User, userID string, now time.Time) error {
	return nil
}

func AdminQueue(ctx context.Context, store domain.Store, actor domain.User) ([]PersonCard, error) {
	return nil, nil
}
