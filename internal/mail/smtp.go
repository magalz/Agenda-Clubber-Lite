package mail

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"net/textproto"
	"regexp"
	"slices"
	"strconv"
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
	// authFailed is how go-mail words an error from the AUTH exchange.
	authFailed = "SMTP AUTH failed"
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
		redactor = newRedactor(cfg.Username, cfg.Password)
	}
	client, err := gomail.NewClient(cfg.Host, append(opts, extra...)...)
	if err != nil {
		return nil, fmt.Errorf("mail: the SMTP client: %w", err)
	}
	return &SMTPSender{client: client, from: from, redactor: redactor}, nil
}

// Send delivers m. A failure comes back as one line with the credentials and the
// recipient taken out, since a server may repeat them in its refusal and the
// error ends up in logs; when ctx ended, the error wraps ctx.Err(). ctx bounds
// the connection; the server then has smtpTimeout for each reply.
func (s *SMTPSender) Send(ctx context.Context, m domain.OutboxMessage) error {
	msg, err := buildMessage(s.from, m)
	if err != nil {
		return err
	}
	if err := s.client.DialAndSendWithContext(ctx, msg); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("mail: send: %w", ctxErr)
		}
		return s.sendError(err, m.To)
	}
	return nil
}

// sendError returns err as one line that carries neither credential nor to, the
// recipient of the message.
func (s *SMTPSender) sendError(err error, to string) error {
	text := err.Error()
	if s.redactor != nil {
		var reply *textproto.Error
		if errors.As(err, &reply) && strings.Contains(text, authFailed) {
			// The server was handed the password in this exchange and may repeat
			// it in any shape, even as the base64 of the AUTH line, so only its
			// status is kept.
			return fmt.Errorf("mail: send: the SMTP server refused the login (%d)", reply.Code)
		}
	}
	var refusal *gomail.SendError
	if errors.As(err, &refusal) {
		// go-mail appends the recipient to a refusal, and the server's own words
		// usually repeat it too, so the text is built from the typed fields.
		text = refusalText(refusal)
	} else if s.redactor != nil {
		text = s.redactor.Replace(text)
	}
	// Redact before the whitespace is collapsed, or a password with a run of
	// spaces would no longer match.
	text = redactAddress(text, to)
	return errors.New("mail: send: " + strings.Join(strings.Fields(text), " "))
}

// refusalText names the step that failed, the reply code of the server and its
// enhanced status code. The server's wording is left out: a refusal usually
// repeats the recipient, in any case or quoting, and may repeat the login.
func refusalText(e *gomail.SendError) string {
	text := e.Reason.String()
	if code := e.ErrorCode(); code != 0 {
		text += ": " + strconv.Itoa(code)
		if status := e.EnhancedStatusCode(); status != "" {
			text += " " + status
		}
	}
	return text
}

// redactAddress hides addr in text whatever its case, as written and as
// strconv.Quote writes it between the quotes, which is how net/textproto shows
// a server's reply. The longer form goes first for the same reason as in
// newRedactor.
func redactAddress(text, addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return text
	}
	quoted := strconv.Quote(addr)
	forms := []string{addr, quoted[1 : len(quoted)-1]}
	slices.SortStableFunc(forms, func(a, b string) int { return len(b) - len(a) })
	for i, form := range forms {
		forms[i] = regexp.QuoteMeta(form)
	}
	return regexp.MustCompile("(?i)"+strings.Join(forms, "|")).ReplaceAllLiteralString(text, "[redacted]")
}

// newRedactor returns a Replacer that hides each secret as written and as
// strconv.Quote writes it between the quotes, which is how net/textproto shows
// a server's reply. A Replacer tries its pairs in order at each position, so
// the longer form goes first: otherwise one that starts another would leave the
// rest of it in the text.
func newRedactor(secrets ...string) *strings.Replacer {
	forms := make([]string, 0, 2*len(secrets))
	for _, secret := range secrets {
		quoted := strconv.Quote(secret)
		forms = append(forms, secret, quoted[1:len(quoted)-1])
	}
	slices.SortStableFunc(forms, func(a, b string) int { return len(b) - len(a) })
	pairs := make([]string, 0, 2*len(forms))
	for _, form := range forms {
		pairs = append(pairs, form, "[redacted]")
	}
	return strings.NewReplacer(pairs...)
}
