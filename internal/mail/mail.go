// Package mail holds the email templates and the senders that deliver them.
package mail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/mail"
	"strings"

	gomail "github.com/wneessen/go-mail"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// Sender delivers one outbox message. It uses To, Subject, Text and HTML only.
type Sender interface {
	Send(ctx context.Context, m domain.OutboxMessage) error
}

// parseFrom checks a sender address such as `IgnisDev <ignisdev@magalz.space>`.
// The error never echoes the input.
func parseFrom(s string) (*mail.Address, error) {
	a, err := mail.ParseAddress(s)
	if err != nil {
		return nil, errors.New("mail: the sender address is not a valid email address")
	}
	return a, nil
}

// domainOf returns what follows the last @ of an address.
func domainOf(addr string) string { return addr[strings.LastIndexByte(addr, '@')+1:] }

// randomHex returns 2*n random hex digits.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b) // never fails: crypto/rand ends the program if the system has no randomness
	return hex.EncodeToString(b)
}

// buildMessage checks m and turns it into a multipart/alternative message from
// from (text, and HTML when m has any), the one form both senders write. It
// refuses a message whose recipient is not a plain address or whose subject
// could add a header; its errors never echo m.
func buildMessage(from *mail.Address, m domain.OutboxMessage) (*gomail.Msg, error) {
	to, err := domain.NormalizeEmail(m.To)
	if err != nil {
		return nil, errors.New("mail: the recipient is not a valid email address")
	}
	if strings.ContainsAny(m.Subject, "\r\n") {
		return nil, errors.New("mail: the subject spans more than one line")
	}
	if m.Text == "" {
		return nil, errors.New("mail: the message has no text")
	}

	// No default User-Agent, and our own Date and Message-ID: go-mail would put
	// this machine's host name in the Message-ID.
	msg := gomail.NewMsg(gomail.WithNoDefaultUserAgent())
	msg.FromMailAddress(from)
	msg.ReplyToMailAddress(from)
	if err := msg.To(to); err != nil {
		return nil, errors.New("mail: the recipient is not a valid email address")
	}
	msg.Subject(m.Subject)
	msg.SetDate()
	msg.SetMessageIDWithValue(randomHex(16) + "@" + domainOf(from.Address))
	msg.SetBodyString(gomail.TypeTextPlain, m.Text)
	if m.HTML != "" {
		msg.AddAlternativeString(gomail.TypeTextHTML, m.HTML)
	}
	return msg, nil
}
