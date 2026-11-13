package mongostore

import (
	"context"
	"errors"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

type outbox struct{ s *Store }

var _ domain.Outbox = outbox{}

var errOutboxStub = errors.New("mongostore: the outbox is not implemented yet")

// The methods below are stubs until the next commit.

func (outbox) Enqueue(context.Context, *domain.OutboxMessage) error { return errOutboxStub }

func (outbox) Claim(context.Context, time.Time, time.Duration, int) ([]domain.OutboxMessage, error) {
	return nil, errOutboxStub
}

func (outbox) MarkSent(context.Context, string, time.Time) error { return errOutboxStub }

func (outbox) MarkFailed(context.Context, string, string, *time.Time) error { return errOutboxStub }
