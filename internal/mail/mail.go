// Package mail holds the email templates and the senders that deliver them.
package mail

import (
	"context"
	"errors"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

var errNotImplemented = errors.New("mail: not implemented")

// Sender delivers one outbox message. It uses To, Subject, Text and HTML only.
type Sender interface {
	Send(ctx context.Context, m domain.OutboxMessage) error
}
