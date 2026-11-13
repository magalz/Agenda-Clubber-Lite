package mail

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/magalz/agenda-clubber-lite/internal/config"
	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

const (
	// smtpTimeout bounds the connection and each exchange with the server.
	smtpTimeout = 15 * time.Second
	// smtpImplicitTLSPort is the port where the TLS handshake comes first.
	smtpImplicitTLSPort = 465
)

// SMTPSender sends through an SMTP server. With a username it logs in with
// AUTH PLAIN and TLS is mandatory: STARTTLS (587) or, on port 465, TLS from the
// first byte, and no credential ever crosses a plain connection. Without a
// username it neither encrypts nor logs in, which is a local relay such as
// Mailpit. Send is safe for concurrent use: each call dials its own connection.
type SMTPSender struct {
	client   *gomail.Client
	from     *mail.Address
	redactor *strings.Replacer // hides the username and the password; nil when there are none
}

// NewSMTPSender returns a sender for cfg. It rejects a configuration that
// cannot work, and its errors never carry the username or the password.
func NewSMTPSender(cfg config.SMTPConfig) (*SMTPSender, error) {
	return newSMTPSender(cfg)
}

// newSMTPSender is NewSMTPSender with extra go-mail options, which tests use to
// trust a self-signed certificate.
func newSMTPSender(cfg config.SMTPConfig, extra ...gomail.Option) (*SMTPSender, error) {
	switch {
	case cfg.Host == "":
		return nil, errors.New("mail: the SMTP host is required")
	case cfg.Port < 1 || cfg.Port > 65535:
		return nil, errors.New("mail: the SMTP port must be from 1 to 65535")
	case cfg.Username != "" && cfg.Password == "":
		return nil, errors.New("mail: the SMTP username needs a password")
	}
	from, err := parseFrom(cfg.From)
	if err != nil {
		return nil, err
	}

	opts := []gomail.Option{
		gomail.WithPort(cfg.Port),
		gomail.WithTimeout(smtpTimeout),
		gomail.WithHELO(domainOf(from.Address)),
	}
	var redactor *strings.Replacer
	if cfg.Username == "" {
		// go-mail defaults to mandatory TLS, so the relay case must say so.
		opts = append(opts, gomail.WithTLSPolicy(gomail.NoTLS))
	} else {
		opts = append(opts,
			gomail.WithTLSPolicy(gomail.TLSMandatory),
			gomail.WithSMTPAuth(gomail.SMTPAuthPlain),
			gomail.WithUsername(cfg.Username),
			gomail.WithPassword(cfg.Password),
		)
		if cfg.Port == smtpImplicitTLSPort {
			opts = append(opts, gomail.WithSSL())
		}
		// A Replacer tries its pairs in order at each position, so the longer
		// credential goes first: otherwise one that starts the other would
		// leave the rest of it in the text.
		secrets := []string{cfg.Username, cfg.Password}
		if len(cfg.Password) > len(cfg.Username) {
			secrets[0], secrets[1] = secrets[1], secrets[0]
		}
		redactor = strings.NewReplacer(secrets[0], "[redacted]", secrets[1], "[redacted]")
	}
	client, err := gomail.NewClient(cfg.Host, append(opts, extra...)...)
	if err != nil {
		return nil, fmt.Errorf("mail: the SMTP client: %w", err)
	}
	return &SMTPSender{client: client, from: from, redactor: redactor}, nil
}

// Send delivers m. A failure comes back as one line with the credentials taken
// out, since a server may repeat them in its refusal; when ctx ended, the error
// wraps ctx.Err(). ctx bounds the connection; the server then has smtpTimeout
// for each reply.
func (s *SMTPSender) Send(ctx context.Context, m domain.OutboxMessage) error {
	msg, err := buildMessage(s.from, m)
	if err != nil {
		return err
	}
	if err := s.client.DialAndSendWithContext(ctx, msg); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("mail: send: %w", ctxErr)
		}
		text := strings.Join(strings.Fields(err.Error()), " ")
		if s.redactor != nil {
			text = s.redactor.Replace(text)
		}
		return errors.New("mail: send: " + text)
	}
	return nil
}
