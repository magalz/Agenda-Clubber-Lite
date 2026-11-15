package events

import (
	"context"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Create is a stub until the GREEN commit.
func Create(ctx context.Context, s domain.Store, actor domain.User, f Form, now time.Time, loc *time.Location) (string, error) {
	return "", nil
}

// Update is a stub until the GREEN commit.
func Update(ctx context.Context, s domain.Store, actor domain.User, id string, f Form, now time.Time, loc *time.Location) error {
	return nil
}

// Delete is a stub until the GREEN commit.
func Delete(ctx context.Context, s domain.Store, actor domain.User, id string, now time.Time, loc *time.Location) error {
	return nil
}
