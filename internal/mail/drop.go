package mail

import (
	"context"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// DropSender writes each message as an .eml file in a folder.
type DropSender struct {
	// From is the sender address written to each file.
	From string
}

// NewDropSender returns a sender that writes into dir, creating it.
func NewDropSender(dir string) (*DropSender, error) {
	return nil, errNotImplemented
}

// Send writes m to a new file.
func (d *DropSender) Send(ctx context.Context, m domain.OutboxMessage) error {
	return errNotImplemented
}
