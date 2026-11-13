package mail

import (
	"bytes"
	"context"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"

	"github.com/magalz/agenda-clubber-lite/internal/domain"
)

// sampleMessage is a valid outbox message with non-ASCII text in every part.
func sampleMessage() domain.OutboxMessage {
	return domain.OutboxMessage{
		ID:      "msg-1",
		To:      "ana@example.com",
		Subject: "Confirme seu e-mail · Agenda Clubber",
		Text:    "Olá, Ana!\n\n[→] https://agenda.example/confirmar/abc123\n",
		HTML:    "<p>Olá, Ana! ◢◤</p>\n<p><a href=\"https://agenda.example/confirmar/abc123\">confirmar</a></p>\n",
	}
}

// parsedEML is a message split into the parts the tests look at.
type parsedEML struct {
	Header      mail.Header
	ContentType string // "multipart/alternative" or "text/plain"
	Text        string // the text/plain part, decoded, LF line endings
	HTML        string // the text/html part, decoded, LF line endings; empty when there is none
}

func parseEML(t *testing.T, raw []byte) parsedEML {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("mail.ReadMessage: %v\n%s", err, raw)
	}
	mediaType, params, err := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("Content-Type %q: %v", msg.Header.Get("Content-Type"), err)
	}
	p := parsedEML{Header: msg.Header, ContentType: mediaType}
	lf := func(b []byte) string { return strings.ReplaceAll(string(b), "\r\n", "\n") }
	switch mediaType {
	case "text/plain":
		var body io.Reader = msg.Body
		if strings.EqualFold(msg.Header.Get("Content-Transfer-Encoding"), "quoted-printable") {
			body = quotedprintable.NewReader(body)
		}
		b, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("reading the text body: %v", err)
		}
		p.Text = lf(b)
	case "multipart/alternative":
		mr := multipart.NewReader(msg.Body, params["boundary"])
		for {
			part, err := mr.NextPart() // decodes quoted-printable
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("NextPart: %v", err)
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("reading a part: %v", err)
			}
			partType, _, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
			switch partType {
			case "text/plain":
				p.Text = lf(b)
			case "text/html":
				p.HTML = lf(b)
			default:
				t.Errorf("unexpected part type %q", partType)
			}
		}
	default:
		t.Fatalf("unexpected message type %q", mediaType)
	}
	return p
}

// checkMessage fails the test unless p is want as sent from from.
func checkMessage(t *testing.T, p parsedEML, want domain.OutboxMessage, from string) {
	t.Helper()
	wantFrom, err := mail.ParseAddress(from)
	if err != nil {
		t.Fatalf("test From %q: %v", from, err)
	}
	for _, name := range []string{"From", "Reply-To"} {
		got, err := mail.ParseAddress(p.Header.Get(name))
		if err != nil {
			t.Errorf("%s = %q: %v", name, p.Header.Get(name), err)
			continue
		}
		if got.Name != wantFrom.Name || got.Address != wantFrom.Address {
			t.Errorf("%s = %v, want %v", name, got, wantFrom)
		}
	}
	if to, err := p.Header.AddressList("To"); err != nil || len(to) != 1 || to[0].Address != want.To {
		t.Errorf("To = %q (%v), want just %q", p.Header.Get("To"), err, want.To)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(p.Header.Get("Subject"))
	if err != nil {
		t.Errorf("Subject %q: %v", p.Header.Get("Subject"), err)
	}
	if subject != want.Subject {
		t.Errorf("Subject = %q, want %q", subject, want.Subject)
	}
	if _, err := p.Header.Date(); err != nil {
		t.Errorf("Date = %q: %v", p.Header.Get("Date"), err)
	}
	// The Message-ID uses the sender's domain, not the machine's host name.
	if id := p.Header.Get("Message-ID"); !strings.HasSuffix(id, "@"+wantFrom.Address[strings.LastIndexByte(wantFrom.Address, '@')+1:]+">") {
		t.Errorf("Message-ID = %q, want one ending in the From domain", id)
	}
	if got := strings.TrimRight(p.Text, "\n"); got != strings.TrimRight(want.Text, "\n") {
		t.Errorf("text part = %q, want %q", got, want.Text)
	}
	if want.HTML == "" {
		if p.ContentType != "text/plain" || p.HTML != "" {
			t.Errorf("message without HTML is %s with html %q, want a plain text message", p.ContentType, p.HTML)
		}
		return
	}
	if p.ContentType != "multipart/alternative" {
		t.Errorf("Content-Type = %q, want multipart/alternative", p.ContentType)
	}
	if got := strings.TrimRight(p.HTML, "\n"); got != strings.TrimRight(want.HTML, "\n") {
		t.Errorf("html part = %q, want %q", got, want.HTML)
	}
}

// unsafeMessages are messages a sender must refuse before it writes anything.
var unsafeMessages = []struct {
	name string
	edit func(*domain.OutboxMessage)
}{
	{"empty recipient", func(m *domain.OutboxMessage) { m.To = "" }},
	{"recipient without a domain", func(m *domain.OutboxMessage) { m.To = "ana" }},
	{"recipient with a display name", func(m *domain.OutboxMessage) { m.To = "Ana <ana@example.com>" }},
	{"two recipients", func(m *domain.OutboxMessage) { m.To = "ana@example.com, bia@example.com" }},
	{"recipient with a header injection", func(m *domain.OutboxMessage) { m.To = "ana@example.com\r\nBcc: evil@example.net" }},
	{"subject with LF", func(m *domain.OutboxMessage) { m.Subject = "Oi\nBcc: evil@example.net" }},
	{"subject with CRLF", func(m *domain.OutboxMessage) { m.Subject = "Oi\r\nBcc: evil@example.net" }},
	{"subject with CR", func(m *domain.OutboxMessage) { m.Subject = "Oi\rBcc: evil@example.net" }},
	{"empty text", func(m *domain.OutboxMessage) { m.Text = "" }},
}

// checkRefused fails the test unless err is an error that does not echo the
// rejected input: a CR, an LF or the injected address would reach a log line.
func checkRefused(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("Send succeeded, want an error")
	}
	if strings.ContainsAny(err.Error(), "\r\n") || strings.Contains(err.Error(), "evil@example.net") {
		t.Errorf("error echoes the rejected input: %q", err)
	}
}

func TestSendersRefuseUnsafeMessages(t *testing.T) {
	for _, tc := range unsafeMessages {
		t.Run("smtp "+tc.name, func(t *testing.T) {
			srv := startFakeSMTP(t, fakeSMTPOptions{})
			s, err := NewSMTPSender(srv.config())
			if err != nil {
				t.Fatalf("NewSMTPSender: %v", err)
			}
			m := sampleMessage()
			tc.edit(&m)
			checkRefused(t, s.Send(context.Background(), m))
			if cmds := srv.commands(); len(cmds) != 0 {
				t.Errorf("the server heard %v, want no connection", cmds)
			}
		})
		t.Run("drop "+tc.name, func(t *testing.T) {
			dir := t.TempDir()
			d, err := NewDropSender(dir)
			if err != nil {
				t.Fatalf("NewDropSender: %v", err)
			}
			m := sampleMessage()
			tc.edit(&m)
			checkRefused(t, d.Send(context.Background(), m))
			if names := dirNames(t, dir); len(names) != 0 {
				t.Errorf("the folder holds %v, want it empty", names)
			}
		})
	}
}

func TestSendersAreSenders(t *testing.T) {
	var _ Sender = (*SMTPSender)(nil)
	var _ Sender = (*DropSender)(nil)
}

// within reports whether got is within d of want.
func within(got, want time.Time, d time.Duration) bool {
	diff := got.Sub(want)
	return diff >= -d && diff <= d
}
