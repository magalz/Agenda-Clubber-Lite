package mail

import (
	"context"

	gomail "github.com/wneessen/go-mail"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// SMTPSender sends through an SMTP server.
type SMTPSender struct{}

// NewSMTPSender returns a sender for cfg.
func NewSMTPSender(cfg config.SMTPConfig) (*SMTPSender, error) {
	return newSMTPSender(cfg)
}

// newSMTPSender is NewSMTPSender with extra go-mail options, which tests use to
// trust a self-signed certificate.
func newSMTPSender(cfg config.SMTPConfig, extra ...gomail.Option) (*SMTPSender, error) {
	return nil, errNotImplemented
}

// Send delivers m.
func (s *SMTPSender) Send(ctx context.Context, m domain.OutboxMessage) error {
	return errNotImplemented
}
