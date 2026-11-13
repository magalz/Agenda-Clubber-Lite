package mail

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"math/big"
	"net"
	"net/textproto"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gomail "github.com/wneessen/go-mail"

	"github.com/magalz/agenda-clubber-lite/internal/config"
)

const (
	testFrom     = "IgnisDev <ignisdev@magalz.space>"
	testUsername = "mailer-user-9f3a"
	testPassword = "p@ss-secret-7c21"
)

// fakeSMTPOptions shapes what the fake server offers.
type fakeSMTPOptions struct {
	tls      *tls.Config // non-nil: offer STARTTLS with this certificate
	auth     bool        // advertise AUTH PLAIN (after STARTTLS when that is offered)
	authEcho bool        // refuse AUTH with a 535 that repeats the credentials and the AUTH line
	mailEcho bool        // accept AUTH, then refuse MAIL with a 550 that repeats the credentials

	// Each of these refuses the message with a reply that repeats the recipient's
	// address in upper case and as the client wrote it, and the credentials of the
	// last AUTH PLAIN, the way a real server words "user unknown".
	rcptRefuse    bool // 550 to RCPT
	dataRefuse    bool // 554 to DATA, before the server asks for the content
	contentRefuse bool // 554 to the end of the content
}

// deliveredMail is what the fake server got through MAIL, RCPT and DATA.
type deliveredMail struct {
	from string
	rcpt []string
	data []byte // LF line endings
}

// fakeSMTP is an in-process SMTP server on 127.0.0.1. It records the commands
// it hears and the messages it accepts.
type fakeSMTP struct {
	opts      fakeSMTPOptions
	ln        net.Listener
	delivered chan deliveredMail

	mu    sync.Mutex
	cmds  []string // verbs in order of arrival
	creds []string // "user:password" of every AUTH PLAIN
}

func startFakeSMTP(t *testing.T, opts fakeSMTPOptions) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{opts: opts, ln: ln, delivered: make(chan deliveredMail, 8)}
	var (
		wg    sync.WaitGroup
		mu    sync.Mutex
		conns []net.Conn
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				f.serve(conn)
			}()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		// A client that gave up without a QUIT (go-mail does not close the
		// connection when STARTTLS or AUTH fails) must not hold the test for
		// the server's read deadline.
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	return f
}

// config is the SMTP settings that point a sender at the server.
func (f *fakeSMTP) config() config.SMTPConfig {
	host, port, err := net.SplitHostPort(f.ln.Addr().String())
	if err != nil {
		panic(err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		panic(err)
	}
	return config.SMTPConfig{Host: host, Port: p, From: testFrom}
}

func (f *fakeSMTP) record(verb string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, verb)
}

// commands returns the verbs heard so far, in order.
func (f *fakeSMTP) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cmds)
}

func (f *fakeSMTP) credentials() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.creds)
}

// next waits for the next accepted message.
func (f *fakeSMTP) next(t *testing.T) deliveredMail {
	t.Helper()
	select {
	case d := <-f.delivered:
		return d
	case <-time.After(5 * time.Second):
		t.Fatal("the server accepted no message")
		return deliveredMail{}
	}
}

// noDelivery fails the test if the server accepted a message.
func (f *fakeSMTP) noDelivery(t *testing.T) {
	t.Helper()
	select {
	case d := <-f.delivered:
		t.Errorf("the server accepted a message for %v", d.rcpt)
	default:
	}
}

// addrIn returns the address between the angle brackets of a MAIL or RCPT
// argument such as "TO:<ana@example.com>".
func addrIn(arg string) string {
	_, rest, _ := strings.Cut(arg, "<")
	addr, _, _ := strings.Cut(rest, ">")
	return addr
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	tp := textproto.NewConn(conn)
	_ = tp.PrintfLine("220 fake.test ESMTP ready")
	var (
		secured    bool
		cur        deliveredMail
		user, pass string // from the last AUTH PLAIN
	)
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		verb, arg, _ := strings.Cut(line, " ")
		verb = strings.ToUpper(verb)
		f.record(verb)
		switch verb {
		case "EHLO":
			lines := []string{"fake.test greets you", "8BITMIME", "SMTPUTF8", "ENHANCEDSTATUSCODES"}
			if f.opts.tls != nil && !secured {
				lines = append(lines, "STARTTLS")
			}
			if f.opts.auth && (f.opts.tls == nil || secured) {
				lines = append(lines, "AUTH PLAIN")
			}
			for i, l := range lines {
				sep := "-"
				if i == len(lines)-1 {
					sep = " "
				}
				_ = tp.PrintfLine("250%s%s", sep, l)
			}
		case "STARTTLS":
			if f.opts.tls == nil || secured {
				_ = tp.PrintfLine("502 5.5.1 STARTTLS not available")
				continue
			}
			_ = tp.PrintfLine("220 2.0.0 ready to start TLS")
			tlsConn := tls.Server(conn, f.opts.tls)
			if err := tlsConn.Handshake(); err != nil {
				return
			}
			conn, tp, secured = tlsConn, textproto.NewConn(tlsConn), true
			cur = deliveredMail{}
		case "AUTH":
			fields := strings.Fields(arg)
			if len(fields) == 0 || !strings.EqualFold(fields[0], "PLAIN") {
				_ = tp.PrintfLine("504 5.5.4 mechanism not supported")
				continue
			}
			var encoded string
			if len(fields) > 1 {
				encoded = fields[1]
			} else {
				_ = tp.PrintfLine("334 ")
				if encoded, err = tp.ReadLine(); err != nil {
					return
				}
			}
			raw, err := base64.StdEncoding.DecodeString(encoded)
			parts := strings.Split(string(raw), "\x00")
			if err != nil || len(parts) != 3 {
				_ = tp.PrintfLine("501 5.5.2 cannot decode the response")
				continue
			}
			user, pass = parts[1], parts[2]
			f.mu.Lock()
			f.creds = append(f.creds, user+":"+pass)
			f.mu.Unlock()
			if f.opts.authEcho {
				_ = tp.PrintfLine("535 5.7.8 authentication failed for %s with password %s (AUTH PLAIN %s)", user, pass, encoded)
				continue
			}
			_ = tp.PrintfLine("235 2.7.0 authenticated")
		case "MAIL":
			if f.opts.mailEcho {
				_ = tp.PrintfLine("550 5.7.1 sender refused for %s with password %s", user, pass)
				continue
			}
			cur = deliveredMail{from: arg}
			_ = tp.PrintfLine("250 2.1.0 ok")
		case "RCPT":
			if f.opts.rcptRefuse {
				addr := addrIn(arg)
				_ = tp.PrintfLine("550 5.1.1 <%s>: Recipient address rejected: user unknown for %s (login %s:%s)", strings.ToUpper(addr), addr, user, pass)
				continue
			}
			cur.rcpt = append(cur.rcpt, arg)
			_ = tp.PrintfLine("250 2.1.5 ok")
		case "DATA":
			if f.opts.dataRefuse {
				addr := addrIn(cur.rcpt[0])
				_ = tp.PrintfLine("554 5.6.0 <%s>: DATA refused for %s (login %s:%s)", strings.ToUpper(addr), addr, user, pass)
				continue
			}
			_ = tp.PrintfLine("354 go ahead")
			data, err := tp.ReadDotBytes()
			if err != nil {
				return
			}
			if f.opts.contentRefuse {
				addr := addrIn(cur.rcpt[0])
				_ = tp.PrintfLine("554 5.6.0 <%s>: content refused for %s (login %s:%s)", strings.ToUpper(addr), addr, user, pass)
				cur = deliveredMail{}
				continue
			}
			cur.data = data
			f.delivered <- cur // before the reply, so a returned Send means it is queued
			cur = deliveredMail{}
			_ = tp.PrintfLine("250 2.0.0 queued")
		case "NOOP", "RSET":
			_ = tp.PrintfLine("250 2.0.0 ok")
		case "QUIT":
			_ = tp.PrintfLine("221 2.0.0 bye")
			return
		default:
			_ = tp.PrintfLine("502 5.5.2 command not recognized")
		}
	}
}

// selfSigned returns a TLS server config for 127.0.0.1 and a client config that
// trusts it.
func selfSigned(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake.test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	server = &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}
	client = &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
	return server, client
}

// checkDelivered fails the test unless the server got the sample message
// addressed to ana@example.com from ignisdev@magalz.space.
func checkDelivered(t *testing.T, d deliveredMail) {
	t.Helper()
	if !strings.Contains(d.from, "<ignisdev@magalz.space>") {
		t.Errorf("MAIL %s, want the envelope sender ignisdev@magalz.space", d.from)
	}
	if len(d.rcpt) != 1 || !strings.Contains(d.rcpt[0], "<ana@example.com>") {
		t.Errorf("RCPT %v, want just ana@example.com", d.rcpt)
	}
	checkMessage(t, parseEML(t, d.data), sampleMessage(), testFrom)
}

func TestSMTPSenderDeliversWithoutAuthOrTLS(t *testing.T) {
	// The server offers STARTTLS and AUTH too: a sender with no username is the
	// local Mailpit setup and must use neither.
	serverTLS, _ := selfSigned(t)
	srv := startFakeSMTP(t, fakeSMTPOptions{tls: serverTLS, auth: true})
	s, err := NewSMTPSender(srv.config())
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	if err := s.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	checkDelivered(t, srv.next(t))
	cmds := srv.commands()
	for _, banned := range []string{"STARTTLS", "AUTH"} {
		if slices.Contains(cmds, banned) {
			t.Errorf("the client sent %s, commands: %v", banned, cmds)
		}
	}
	for _, want := range []string{"EHLO", "MAIL", "RCPT", "DATA"} {
		if !slices.Contains(cmds, want) {
			t.Errorf("the client never sent %s, commands: %v", want, cmds)
		}
	}
}

func TestSMTPSenderIsSafeForConcurrentSends(t *testing.T) {
	// The outbox worker may send from several goroutines; run with -race.
	srv := startFakeSMTP(t, fakeSMTPOptions{})
	s, err := NewSMTPSender(srv.config())
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	const n = 4
	errs := make(chan error, n)
	for range n {
		go func() { errs <- s.Send(context.Background(), sampleMessage()) }()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Errorf("Send: %v", err)
		}
	}
	for range n {
		checkDelivered(t, srv.next(t))
	}
}

func TestSMTPSenderAuthenticatesOverSTARTTLS(t *testing.T) {
	serverTLS, clientTLS := selfSigned(t)
	srv := startFakeSMTP(t, fakeSMTPOptions{tls: serverTLS, auth: true})
	cfg := srv.config()
	cfg.Username, cfg.Password = testUsername, testPassword
	s, err := newSMTPSender(cfg, gomail.WithTLSConfig(clientTLS))
	if err != nil {
		t.Fatalf("newSMTPSender: %v", err)
	}
	if err := s.Send(context.Background(), sampleMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	checkDelivered(t, srv.next(t))
	if got := srv.credentials(); len(got) != 1 || got[0] != testUsername+":"+testPassword {
		t.Errorf("the server got credentials %v, want the configured pair once", got)
	}
	cmds := srv.commands()
	tlsAt, authAt, mailAt := slices.Index(cmds, "STARTTLS"), slices.Index(cmds, "AUTH"), slices.Index(cmds, "MAIL")
	if tlsAt < 0 || authAt < tlsAt || mailAt < authAt {
		t.Errorf("want STARTTLS, then AUTH, then MAIL, got %v", cmds)
	}
}

func TestSMTPSenderWithUsernameNeedsSTARTTLS(t *testing.T) {
	// A server that offers AUTH PLAIN but not STARTTLS must never see the password.
	srv := startFakeSMTP(t, fakeSMTPOptions{auth: true})
	cfg := srv.config()
	cfg.Username, cfg.Password = testUsername, testPassword
	s, err := NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	err = s.Send(context.Background(), sampleMessage())
	if err == nil {
		t.Fatal("Send succeeded without TLS, want an error")
	}
	checkNoSecrets(t, err)
	if cmds := srv.commands(); slices.Contains(cmds, "AUTH") || slices.Contains(cmds, "MAIL") {
		t.Errorf("the client went on without TLS: %v", cmds)
	}
	if got := srv.credentials(); len(got) != 0 {
		t.Errorf("the server got credentials %v", got)
	}
	srv.noDelivery(t)
}

func TestSMTPSenderRefusesAnUntrustedCertificate(t *testing.T) {
	// The server's certificate is self-signed and the sender trusts only the
	// system roots, so the handshake must fail before any credential is sent.
	serverTLS, _ := selfSigned(t)
	srv := startFakeSMTP(t, fakeSMTPOptions{tls: serverTLS, auth: true})
	cfg := srv.config()
	cfg.Username, cfg.Password = testUsername, testPassword
	s, err := NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	err = s.Send(context.Background(), sampleMessage())
	if err == nil {
		t.Fatal("Send succeeded against an untrusted certificate, want an error")
	}
	checkNoSecrets(t, err)
	if got := srv.credentials(); len(got) != 0 {
		t.Errorf("the server got credentials %v", got)
	}
	srv.noDelivery(t)
}

func TestSMTPSenderRedactsCredentialsFromErrors(t *testing.T) {
	// A server that echoes the credentials back in its refusal must not get
	// them into the error the outbox worker stores and logs.
	serverTLS, clientTLS := selfSigned(t)
	srv := startFakeSMTP(t, fakeSMTPOptions{tls: serverTLS, auth: true, authEcho: true})
	cfg := srv.config()
	cfg.Username, cfg.Password = testUsername, testPassword
	s, err := newSMTPSender(cfg, gomail.WithTLSConfig(clientTLS))
	if err != nil {
		t.Fatalf("newSMTPSender: %v", err)
	}
	err = s.Send(context.Background(), sampleMessage())
	if err == nil {
		t.Fatal("Send succeeded after a 535, want an error")
	}
	checkNoSecrets(t, err)
	if !strings.Contains(err.Error(), "535") {
		t.Errorf("error = %q, want the server's status kept", err)
	}
	srv.noDelivery(t)
}

func TestSMTPSenderRedactsOverlappingCredentials(t *testing.T) {
	// When one credential starts with the other, redacting the shorter first
	// would leave the rest of the longer one in the text.
	cases := []struct{ name, username, password string }{
		{"username is a prefix of the password", "ana", "ana-secret"},
		{"password is a prefix of the username", "ana-secret", "ana"},
		{"same value", "ana-secret", "ana-secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.SMTPConfig{Host: "smtp.example.com", Port: 587, From: testFrom, Username: tc.username, Password: tc.password}
			s, err := newSMTPSender(cfg)
			if err != nil {
				t.Fatalf("newSMTPSender: %v", err)
			}
			got := s.redactor.Replace("535 bad login " + tc.password + " for " + tc.username + ", then " + tc.password)
			if want := "535 bad login [redacted] for [redacted], then [redacted]"; got != want {
				t.Errorf("redacted = %q, want %q", got, want)
			}
		})
	}
}

func checkNoSecrets(t *testing.T, err error) {
	t.Helper()
	checkNoCredentials(t, err, testUsername, testPassword)
}

// checkNoCredentials fails the test if err carries the username or the password
// in any shape a server reply could bring it back: as written, as net/textproto
// quotes a reply (%q), with its whitespace collapsed, or inside the base64 line
// of AUTH PLAIN.
func checkNoCredentials(t *testing.T, err error, username, password string) {
	t.Helper()
	var forms []string
	for _, secret := range []string{username, password} {
		quoted := strconv.Quote(secret)
		forms = append(forms, secret, quoted[1:len(quoted)-1], strings.Join(strings.Fields(secret), " "))
	}
	forms = append(forms, base64.StdEncoding.EncodeToString([]byte("\x00"+username+"\x00"+password)))
	for _, form := range forms {
		if strings.Contains(err.Error(), form) {
			t.Errorf("error leaks %q: %q", form, err)
		}
	}
}

func TestSMTPSenderRedactsAwkwardCredentialsFromErrors(t *testing.T) {
	// A server may repeat the credentials in a refusal. net/textproto quotes the
	// reply with %q, and the error is flattened onto one line, so a password with
	// whitespace, a quote or a backslash does not come back as it was written.
	serverTLS, clientTLS := selfSigned(t)
	passwords := []struct{ name, password string }{
		{"double space", "p@ss  word-7c21"},
		{"tab", "p@ss\tword-7c21"},
		{"double quote", `p@ss"word-7c21`},
		{"backslash", `p@ss\word-7c21`},
	}
	refusals := []struct {
		name     string
		opts     fakeSMTPOptions
		wantCode string
	}{
		{"login refused", fakeSMTPOptions{authEcho: true}, "535"},
		{"sender refused after the login", fakeSMTPOptions{mailEcho: true}, "550"},
	}
	for _, refusal := range refusals {
		for _, pw := range passwords {
			t.Run(refusal.name+"/"+pw.name, func(t *testing.T) {
				opts := refusal.opts
				opts.tls, opts.auth = serverTLS, true
				srv := startFakeSMTP(t, opts)
				cfg := srv.config()
				cfg.Username, cfg.Password = testUsername, pw.password
				s, err := newSMTPSender(cfg, gomail.WithTLSConfig(clientTLS))
				if err != nil {
					t.Fatalf("newSMTPSender: %v", err)
				}
				err = s.Send(context.Background(), sampleMessage())
				if err == nil {
					t.Fatal("Send succeeded after a refusal, want an error")
				}
				checkNoCredentials(t, err, testUsername, pw.password)
				if !strings.Contains(err.Error(), refusal.wantCode) {
					t.Errorf("error = %q, want the server's status %s kept", err, refusal.wantCode)
				}
				srv.noDelivery(t)
			})
		}
	}
}

// checkNoAddress fails the test if text names the address in any letter case.
func checkNoAddress(t *testing.T, text, addr string) {
	t.Helper()
	if strings.Contains(strings.ToLower(text), strings.ToLower(addr)) {
		t.Errorf("text leaks the address %s: %q", addr, text)
	}
}

func TestSMTPSenderKeepsTheRecipientOutOfErrors(t *testing.T) {
	// go-mail's SendError appends "affected recipient(s): <address>" to its text,
	// and a server's refusal usually names the address too. The error that Send
	// returns is logged and stored, so it keeps the status and the step that
	// failed, and nothing that came from the recipient. The fields of a SendError
	// are unexported, so the only way to get one with a recipient is a server that
	// refuses the message.
	const to = "Ana.Silva+agenda@Example.com" // mixed case: the server sees it lower-cased
	serverTLS, clientTLS := selfSigned(t)
	refusals := []struct {
		name       string
		opts       fakeSMTPOptions
		wantStep   string // how go-mail words the step that failed
		wantCode   string
		wantStatus string
	}{
		{"recipient refused", fakeSMTPOptions{rcptRefuse: true}, "RCPT TO", "550", "5.1.1"},
		{"DATA refused", fakeSMTPOptions{dataRefuse: true}, "DATA", "554", "5.6.0"},
		{"content refused", fakeSMTPOptions{contentRefuse: true}, "DATA writer", "554", "5.6.0"},
	}
	for _, refusal := range refusals {
		for _, login := range []bool{false, true} {
			name := refusal.name + "/no login"
			if login {
				name = refusal.name + "/login"
			}
			t.Run(name, func(t *testing.T) {
				opts := refusal.opts
				var extra []gomail.Option
				if login {
					opts.tls, opts.auth = serverTLS, true
					extra = append(extra, gomail.WithTLSConfig(clientTLS))
				}
				srv := startFakeSMTP(t, opts)
				cfg := srv.config()
				if login {
					cfg.Username, cfg.Password = testUsername, testPassword
				}
				s, err := newSMTPSender(cfg, extra...)
				if err != nil {
					t.Fatalf("newSMTPSender: %v", err)
				}
				m := sampleMessage()
				m.To = to
				err = s.Send(context.Background(), m)
				if err == nil {
					t.Fatal("Send succeeded after a refusal, want an error")
				}
				checkNoAddress(t, err.Error(), to)
				if login {
					checkNoSecrets(t, err)
				}
				for _, want := range []string{refusal.wantStep, refusal.wantCode, refusal.wantStatus} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error = %q, want %q kept", err, want)
					}
				}
				if strings.ContainsAny(err.Error(), "\r\n") {
					t.Errorf("error = %q, want one line", err)
				}
				srv.noDelivery(t)
			})
		}
	}
}

func TestSMTPSenderRedactsTheRecipientFromOtherErrors(t *testing.T) {
	// An error that is not a go-mail SendError keeps its text, minus the address:
	// in any letter case, between angle brackets, and as %q writes it.
	cases := []struct{ to, text, keep string }{
		{"Ana.Silva+agenda@Example.com", "dial failed: 550 5.1.1 <Ana.Silva+agenda@Example.com>: user unknown", "550 5.1.1"},
		{"Ana.Silva+agenda@Example.com", "closing: 551 <ANA.SILVA+AGENDA@EXAMPLE.COM> and ana.silva+agenda@example.com", "551"},
		{"Ana.Silva+agenda@Example.com", `reply "552 for ana.silva+agenda@example.com"`, "552"},
		{`o"brien\x@example.com`, `553 refused for "o\"brien\\x@example.com", also o"brien\x@example.com`, "553"},
	}
	for _, tc := range cases {
		for _, withLogin := range []bool{false, true} {
			cfg := config.SMTPConfig{Host: "smtp.example.com", Port: 587, From: testFrom}
			if withLogin {
				cfg.Username, cfg.Password = testUsername, testPassword
			}
			s, err := NewSMTPSender(cfg)
			if err != nil {
				t.Fatalf("NewSMTPSender: %v", err)
			}
			got := s.sendError(errors.New(tc.text), tc.to).Error()
			quoted := strconv.Quote(tc.to)
			checkNoAddress(t, got, tc.to)
			checkNoAddress(t, got, quoted[1:len(quoted)-1])
			if !strings.Contains(got, tc.keep) {
				t.Errorf("sendError(%q) = %q, want %q kept", tc.text, got, tc.keep)
			}
		}
	}
}

func TestSMTPSenderReturnsContextErrors(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{})
	s, err := NewSMTPSender(srv.config())
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Send(ctx, sampleMessage()); !errors.Is(err, context.Canceled) {
		t.Errorf("Send with a canceled context = %v, want context.Canceled", err)
	}
	srv.noDelivery(t)
}

func TestSMTPSenderReportsAnUnreachableServer(t *testing.T) {
	srv := startFakeSMTP(t, fakeSMTPOptions{})
	cfg := srv.config()
	srv.ln.Close() // nothing listens on the port any more
	s, err := NewSMTPSender(cfg)
	if err != nil {
		t.Fatalf("NewSMTPSender: %v", err)
	}
	if err := s.Send(context.Background(), sampleMessage()); err == nil {
		t.Error("Send succeeded with no server, want an error")
	}
}

func TestNewSMTPSenderValidatesTheConfig(t *testing.T) {
	valid := config.SMTPConfig{Host: "smtp.example.com", Port: 587, From: testFrom}
	withAuth := valid
	withAuth.Username, withAuth.Password = testUsername, testPassword
	implicitTLS := withAuth
	implicitTLS.Port = 465

	for name, cfg := range map[string]config.SMTPConfig{
		"no username (local relay)":  valid,
		"username over STARTTLS":     withAuth,
		"username over implicit TLS": implicitTLS,
	} {
		if _, err := NewSMTPSender(cfg); err != nil {
			t.Errorf("%s: NewSMTPSender: %v", name, err)
		}
	}

	bad := []struct {
		name string
		edit func(*config.SMTPConfig)
	}{
		{"no host", func(c *config.SMTPConfig) { c.Host = "" }},
		{"port zero", func(c *config.SMTPConfig) { c.Port = 0 }},
		{"port too large", func(c *config.SMTPConfig) { c.Port = 70000 }},
		{"no from", func(c *config.SMTPConfig) { c.From = "" }},
		{"from that is not an address", func(c *config.SMTPConfig) { c.From = "not an address" }},
		{"username without a password", func(c *config.SMTPConfig) { c.Username, c.Password = testUsername, "" }},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			cfg := withAuth
			tc.edit(&cfg)
			_, err := NewSMTPSender(cfg)
			if err == nil {
				t.Fatal("NewSMTPSender succeeded, want an error")
			}
			checkNoSecrets(t, err)
		})
	}
}
