package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"testing"
)

const (
	okOrigin = "https://agenda.example"
	okMongo  = "mongodb://localhost:27017/?directConnection=true"

	// secretMongo is a valid URI that carries credentials: no output may show it.
	secretMongo = "mongodb://dbuser:mongo-secret@db.example:27017/?directConnection=true"
)

// getenvOf builds a getenv from key/value pairs; later pairs win.
func getenvOf(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// withOrigin returns kv preceded by a valid APP_ORIGIN and the SMTP host and
// username that production requires (see TestLoadProductionSMTP).
func withOrigin(kv ...string) []string {
	return append([]string{"APP_ORIGIN", okOrigin, "SMTP_HOST", "smtp.example", "SMTP_USERNAME", "smtp-user"}, kv...)
}

// withMongo returns kv preceded by the two required variables, a valid
// APP_ORIGIN and a valid MONGO_URI.
func withMongo(kv ...string) []string {
	return withOrigin(append([]string{"MONGO_URI", okMongo}, kv...)...)
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(getenvOf(withMongo()...))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "prod" {
		t.Errorf("Env = %q, want prod", cfg.Env)
	}
	if cfg.IsDev() {
		t.Error("IsDev() = true, want false")
	}
	if cfg.Addr != DefaultAddr || DefaultAddr != ":8120" {
		t.Errorf("Addr = %q, DefaultAddr = %q; want both :8120", cfg.Addr, DefaultAddr)
	}
	if cfg.Origin != okOrigin {
		t.Errorf("Origin = %q, want %q", cfg.Origin, okOrigin)
	}
	if cfg.Location == nil || cfg.Location.String() != "America/Fortaleza" {
		t.Errorf("Location = %v, want America/Fortaleza", cfg.Location)
	}
	if want := (MongoConfig{URI: okMongo, DB: "agenda"}); cfg.Mongo != want {
		t.Errorf("Mongo = %#v, want %#v", cfg.Mongo, want)
	}
	if len(cfg.TrustedProxyCIDRs) != 0 {
		t.Errorf("TrustedProxyCIDRs = %v, want empty", cfg.TrustedProxyCIDRs)
	}
	want := SMTPConfig{Host: "smtp.example", Port: 587, Username: "smtp-user", From: "IgnisDev <ignisdev@magalz.space>"}
	if cfg.SMTP != want {
		t.Errorf("SMTP = %#v, want %#v", cfg.SMTP, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(getenvOf(
		"APP_ENV", "dev",
		"APP_ADDR", "127.0.0.1:9000",
		"APP_ORIGIN", "http://localhost:9000",
		"APP_TZ", "UTC",
		"MONGO_URI", secretMongo,
		"MONGO_DB", "agenda_prod-1",
		"TRUSTED_PROXY_CIDRS", "10.0.0.0/8",
		"SMTP_HOST", "smtp.example",
		"SMTP_PORT", "2525",
		"SMTP_USERNAME", "user",
		"SMTP_PASSWORD", "pw",
		"SMTP_FROM", "Agenda <agenda@example.org>",
	))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.IsDev() || cfg.Env != "dev" {
		t.Errorf("Env = %q, IsDev = %v, want dev, true", cfg.Env, cfg.IsDev())
	}
	if cfg.Addr != "127.0.0.1:9000" || cfg.Origin != "http://localhost:9000" {
		t.Errorf("Addr/Origin = %q/%q", cfg.Addr, cfg.Origin)
	}
	if want := (MongoConfig{URI: secretMongo, DB: "agenda_prod-1"}); cfg.Mongo != want {
		t.Errorf("Mongo = %#v, want %#v", cfg.Mongo, want)
	}
	if cfg.Location.String() != "UTC" {
		t.Errorf("Location = %v, want UTC", cfg.Location)
	}
	if !reflect.DeepEqual(cfg.TrustedProxyCIDRs, []string{"10.0.0.0/8"}) {
		t.Errorf("TrustedProxyCIDRs = %v", cfg.TrustedProxyCIDRs)
	}
	want := SMTPConfig{Host: "smtp.example", Port: 2525, Username: "user", Password: "pw", From: "Agenda <agenda@example.org>"}
	if cfg.SMTP != want {
		t.Errorf("SMTP = %#v, want %#v", cfg.SMTP, want)
	}
}

func TestLoadMailDropDir(t *testing.T) {
	tests := []struct {
		name string
		kv   []string
		want string
	}{
		{"unset in prod", withMongo(), ""},
		{"unset in dev", withMongo("APP_ENV", "dev"), ""},
		{"blank in prod counts as unset", withMongo("MAIL_DROP_DIR", "   "), ""},
		{"set in dev", withMongo("APP_ENV", "dev", "MAIL_DROP_DIR", "tmp/mail"), "tmp/mail"},
		{"trimmed in dev", withMongo("APP_ENV", "dev", "MAIL_DROP_DIR", "  /var/mail-drop  "), "/var/mail-drop"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(getenvOf(tt.kv...))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.MailDropDir != tt.want {
				t.Errorf("MailDropDir = %q, want %q", cfg.MailDropDir, tt.want)
			}
		})
	}
}

func TestLoadMailDropDirRefusedInProdDoesNotEchoThePath(t *testing.T) {
	_, err := Load(getenvOf(withMongo("MAIL_DROP_DIR", "/home/someone/private-drop")...))
	if err == nil || !strings.Contains(err.Error(), "MAIL_DROP_DIR:") {
		t.Fatalf("Load error = %v, want one naming MAIL_DROP_DIR", err)
	}
	if strings.Contains(err.Error(), "private-drop") {
		t.Errorf("error %q echoes the path", err)
	}
}

// Production sends mail only through a server that needs a login, and the SMTP
// sender then insists on TLS: a missing host or username would leave the app
// sending in clear text, or not at all. Development keeps its freedom (Mailpit
// takes no login, and without a host a drop folder or no mail at all does).
func TestLoadProductionSMTP(t *testing.T) {
	base := []string{"APP_ORIGIN", okOrigin, "MONGO_URI", okMongo}
	with := func(kv ...string) []string { return append(slices.Clone(base), kv...) }
	tests := []struct {
		name string
		kv   []string
		want string // variable that the error must name; empty when Load must succeed
	}{
		{"prod without a host", with(), "SMTP_HOST"},
		{"prod with a username but no host", with("SMTP_USERNAME", "user"), "SMTP_HOST"},
		{"prod with a blank host", with("SMTP_HOST", "   ", "SMTP_USERNAME", "user"), "SMTP_HOST"},
		{"prod with a host but no username", with("SMTP_HOST", "smtp.example"), "SMTP_USERNAME"},
		{"prod with a blank username", with("SMTP_HOST", "smtp.example", "SMTP_USERNAME", "  "), "SMTP_USERNAME"},
		{"explicit prod without a host", with("APP_ENV", "prod"), "SMTP_HOST"},
		{"prod with a host and a username", with("SMTP_HOST", "smtp.example", "SMTP_USERNAME", "user"), ""},
		{"dev without SMTP", with("APP_ENV", "dev"), ""},
		{"dev with a host and no username (Mailpit)", with("APP_ENV", "dev", "SMTP_HOST", "localhost", "SMTP_PORT", "1025"), ""},
		{"dev with a drop folder", with("APP_ENV", "dev", "MAIL_DROP_DIR", "tmp/mail"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(getenvOf(tt.kv...))
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("Load: %v, want no error", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want+":")):
				t.Errorf("Load error = %v, want one naming %s", err, tt.want)
			}
		})
	}
}

// The error of a missing SMTP setting never echoes a value: neither the
// password set in the same environment nor the host or username next to the
// missing one.
func TestLoadProductionSMTPErrorsDoNotLeakSecrets(t *testing.T) {
	for name, kv := range map[string][]string{
		"no host":     {"SMTP_USERNAME", "user-secret", "SMTP_PASSWORD", "smtp-secret"},
		"no username": {"SMTP_HOST", "smtp.private-relay.example", "SMTP_PASSWORD", "smtp-secret"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(getenvOf(append([]string{"APP_ORIGIN", okOrigin, "MONGO_URI", secretMongo}, kv...)...))
			if err == nil {
				t.Fatal("Load returned no error")
			}
			for _, leak := range []string{"smtp-secret", "user-secret", "private-relay", "mongo-secret", "dbuser"} {
				if strings.Contains(err.Error(), leak) {
					t.Errorf("error %q leaks %q", err, leak)
				}
			}
		})
	}
}

func TestLoadOriginNormalization(t *testing.T) {
	tests := []struct{ in, want string }{
		{"https://agenda.example", "https://agenda.example"},
		{"https://agenda.example/", "https://agenda.example"},
		{"  https://agenda.example/  ", "https://agenda.example"},
		{"HTTPS://Agenda.Example", "https://agenda.example"},
		{"http://localhost:8120", "http://localhost:8120"},
		{"https://agenda.example:8443/", "https://agenda.example:8443"},
		{"https://agenda.example:443", "https://agenda.example"},
		{"http://agenda.example:80/", "http://agenda.example"},
		{"http://agenda.example:443", "http://agenda.example:443"},
		{"http://[::1]:8120", "http://[::1]:8120"},
		{"https://[::1]:443", "https://[::1]"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			cfg, err := Load(getenvOf("APP_ORIGIN", tt.in, "MONGO_URI", okMongo, "SMTP_HOST", "smtp.example", "SMTP_USERNAME", "smtp-user"))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Origin != tt.want {
				t.Errorf("Origin = %q, want %q", cfg.Origin, tt.want)
			}
		})
	}
}

func TestLoadAddrValid(t *testing.T) {
	for _, addr := range []string{":8120", "0.0.0.0:80", "127.0.0.1:0", "[::1]:8120", "localhost:8120"} {
		t.Run(addr, func(t *testing.T) {
			cfg, err := Load(getenvOf(withMongo("APP_ADDR", addr)...))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Addr != addr {
				t.Errorf("Addr = %q, want %q", cfg.Addr, addr)
			}
		})
	}
}

func TestLoadCIDRParsing(t *testing.T) {
	tests := []struct {
		name, in string
		want     []string
	}{
		{"empty", "", nil},
		{"only separators", " , ,, ", nil},
		{"one", "10.0.0.0/8", []string{"10.0.0.0/8"}},
		{"spaces and empty entries", " 10.0.0.0/8 , ,fd00::/8,, 172.16.0.0/12 ", []string{"10.0.0.0/8", "fd00::/8", "172.16.0.0/12"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(getenvOf(withMongo("TRUSTED_PROXY_CIDRS", tt.in)...))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.TrustedProxyCIDRs) != len(tt.want) || (len(tt.want) > 0 && !reflect.DeepEqual(cfg.TrustedProxyCIDRs, tt.want)) {
				t.Errorf("TrustedProxyCIDRs = %q, want %q", cfg.TrustedProxyCIDRs, tt.want)
			}
		})
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name string
		kv   []string
		want string // variable that the error must name
	}{
		{"origin missing", []string{}, "APP_ORIGIN"},
		{"origin blank", []string{"APP_ORIGIN", "   "}, "APP_ORIGIN"},
		{"origin without scheme", []string{"APP_ORIGIN", "agenda.example"}, "APP_ORIGIN"},
		{"origin ftp scheme", []string{"APP_ORIGIN", "ftp://agenda.example"}, "APP_ORIGIN"},
		{"origin with path", []string{"APP_ORIGIN", "https://agenda.example/app"}, "APP_ORIGIN"},
		{"origin with double slash path", []string{"APP_ORIGIN", "https://agenda.example//"}, "APP_ORIGIN"},
		{"origin with query", []string{"APP_ORIGIN", "https://agenda.example?x=1"}, "APP_ORIGIN"},
		{"origin with empty query", []string{"APP_ORIGIN", "https://agenda.example/?"}, "APP_ORIGIN"},
		{"origin with fragment", []string{"APP_ORIGIN", "https://agenda.example/#top"}, "APP_ORIGIN"},
		{"origin with userinfo", []string{"APP_ORIGIN", "https://user:pw@agenda.example"}, "APP_ORIGIN"},
		{"origin without host", []string{"APP_ORIGIN", "https://"}, "APP_ORIGIN"},
		{"origin with port only", []string{"APP_ORIGIN", "http://:8120"}, "APP_ORIGIN"},
		{"origin with bad port", []string{"APP_ORIGIN", "https://agenda.example:abc"}, "APP_ORIGIN"},
		{"bad env", withMongo("APP_ENV", "staging"), "APP_ENV"},
		{"env is case sensitive", withMongo("APP_ENV", "DEV"), "APP_ENV"},
		{"addr without port", withMongo("APP_ADDR", "localhost"), "APP_ADDR"},
		{"addr port only digits", withMongo("APP_ADDR", "8120"), "APP_ADDR"},
		{"addr non numeric port", withMongo("APP_ADDR", ":http"), "APP_ADDR"},
		{"addr empty port", withMongo("APP_ADDR", "localhost:"), "APP_ADDR"},
		{"addr port too large", withMongo("APP_ADDR", ":99999"), "APP_ADDR"},
		{"addr negative port", withMongo("APP_ADDR", ":-1"), "APP_ADDR"},
		{"bad tz", withMongo("APP_TZ", "Mars/Olympus"), "APP_TZ"},
		{"bad cidr", withMongo("TRUSTED_PROXY_CIDRS", "10.0.0.0/8,notacidr"), "TRUSTED_PROXY_CIDRS"},
		{"cidr without prefix", withMongo("TRUSTED_PROXY_CIDRS", "10.0.0.1"), "TRUSTED_PROXY_CIDRS"},
		{"cidr prefix too long", withMongo("TRUSTED_PROXY_CIDRS", "10.0.0.0/33"), "TRUSTED_PROXY_CIDRS"},
		{"smtp port not a number", withMongo("SMTP_PORT", "abc"), "SMTP_PORT"},
		{"smtp port zero", withMongo("SMTP_PORT", "0"), "SMTP_PORT"},
		{"smtp port negative", withMongo("SMTP_PORT", "-1"), "SMTP_PORT"},
		{"smtp port too large", withMongo("SMTP_PORT", "65536"), "SMTP_PORT"},
		{"mongo uri missing", withOrigin(), "MONGO_URI"},
		{"mongo db invalid", withMongo("MONGO_DB", "bad name"), "MONGO_DB"},
		{"mail drop dir in prod", withMongo("MAIL_DROP_DIR", "/tmp/mail"), "MAIL_DROP_DIR"},
		{"mail drop dir with explicit prod", withMongo("APP_ENV", "prod", "MAIL_DROP_DIR", "/tmp/mail"), "MAIL_DROP_DIR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(getenvOf(tt.kv...))
			if err == nil {
				t.Fatal("Load returned no error")
			}
			if !strings.Contains(err.Error(), tt.want+":") {
				t.Errorf("error %q does not name %s", err, tt.want)
			}
		})
	}
}

func TestLoadErrorsDoNotLeakSecrets(t *testing.T) {
	_, err := Load(getenvOf(
		"APP_ORIGIN", "https://user:origin-secret@agenda.example/path",
		"SMTP_PASSWORD", "smtp-secret",
		"SMTP_PORT", "abc",
	))
	if err == nil {
		t.Fatal("Load returned no error")
	}
	for _, secret := range []string{"origin-secret", "smtp-secret"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q leaks %q", err, secret)
		}
	}
	_, err = Load(getenvOf("APP_ORIGIN", okOrigin, "SMTP_PASSWORD", "smtp-secret", "SMTP_PORT", "abc"))
	if err == nil || strings.Contains(err.Error(), "smtp-secret") {
		t.Errorf("error %v leaks the SMTP password", err)
	}
	for _, uri := range []string{
		"postgres://dbuser:mongo-secret@db.example:5432/agenda", // wrong scheme
		"dbuser:mongo-secret@db.example:27017",                  // no scheme
		"MONGODB://dbuser:mongo-secret@db.example:27017",        // the scheme is case sensitive
	} {
		_, err = Load(getenvOf(withOrigin("MONGO_URI", uri)...))
		if err == nil || !strings.Contains(err.Error(), "MONGO_URI:") {
			t.Fatalf("MONGO_URI=%q: error %v, want one naming MONGO_URI", uri, err)
		}
		for _, leak := range []string{"mongo-secret", "dbuser", "db.example", uri} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("MONGO_URI=%q: error %q leaks %q", uri, err, leak)
			}
		}
	}
}

func TestSMTPPasswordNeverPrinted(t *testing.T) {
	cfg, err := Load(getenvOf(withMongo("SMTP_PASSWORD", "smtp-secret", "SMTP_USERNAME", "user")...))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SMTP.Password != "smtp-secret" {
		t.Fatalf("Password = %q, want it kept in memory", cfg.SMTP.Password)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("cfg", "smtp", cfg.SMTP)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("cfg", "smtp", cfg.SMTP)
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("cfg", "config", cfg)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("cfg", "config", cfg)
	asJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	outputs := map[string]string{
		"json cfg":    string(asJSON),
		"%v cfg":      fmt.Sprintf("%v", cfg),
		"%+v cfg":     fmt.Sprintf("%+v", cfg),
		"%#v cfg":     fmt.Sprintf("%#v", cfg),
		"Sprint smtp": fmt.Sprint(cfg.SMTP),
		"%v smtp":     fmt.Sprintf("%v", cfg.SMTP),
		"%#v smtp":    fmt.Sprintf("%#v", cfg.SMTP),
		"slog smtp":   buf.String(),
		"%+v *smtp":   fmt.Sprintf("%+v", &cfg.SMTP),
		"%v ptr cfg":  fmt.Sprintf("%v", &cfg),
	}
	for name, out := range outputs {
		if strings.Contains(out, "smtp-secret") {
			t.Errorf("%s leaks the SMTP password: %s", name, out)
		}
	}
	if !strings.Contains(fmt.Sprintf("%v", cfg.SMTP), "user") {
		t.Errorf("%%v smtp = %q, want the username kept", fmt.Sprintf("%v", cfg.SMTP))
	}
}

// The Mongo cases below run against Load and LoadMongo alike.
var mongoInvalid = []struct {
	name string
	kv   []string
	want string // variable that the error must name
}{
	{"uri missing", nil, "MONGO_URI"},
	{"uri blank", []string{"MONGO_URI", "   "}, "MONGO_URI"},
	{"uri without scheme", []string{"MONGO_URI", "localhost:27017"}, "MONGO_URI"},
	{"uri http scheme", []string{"MONGO_URI", "http://localhost:27017"}, "MONGO_URI"},
	{"uri postgres scheme", []string{"MONGO_URI", "postgres://localhost:5432/agenda"}, "MONGO_URI"},
	{"uri upper case scheme", []string{"MONGO_URI", "MONGODB://localhost:27017"}, "MONGO_URI"},
	{"uri scheme only", []string{"MONGO_URI", "mongodb://"}, "MONGO_URI"},
	// The Go driver refuses directConnection with an SRV URI, and every URI must carry it.
	{"uri srv scheme", []string{"MONGO_URI", "mongodb+srv://cluster.example/?directConnection=true"}, "MONGO_URI"},
	{"uri without directConnection", []string{"MONGO_URI", "mongodb://localhost:27017/"}, "MONGO_URI"},
	{"uri directConnection false", []string{"MONGO_URI", "mongodb://localhost:27017/?directConnection=false"}, "MONGO_URI"},
	{"uri directConnection in path", []string{"MONGO_URI", "mongodb://localhost:27017/directConnection=true"}, "MONGO_URI"},
	{"db with space", []string{"MONGO_URI", okMongo, "MONGO_DB", "my db"}, "MONGO_DB"},
	{"db with dot", []string{"MONGO_URI", okMongo, "MONGO_DB", "my.db"}, "MONGO_DB"},
	{"db with slash", []string{"MONGO_URI", okMongo, "MONGO_DB", "a/b"}, "MONGO_DB"},
	{"db with dollar", []string{"MONGO_URI", okMongo, "MONGO_DB", "$agenda"}, "MONGO_DB"},
	{"db with accent", []string{"MONGO_URI", okMongo, "MONGO_DB", "agenda\u00e7"}, "MONGO_DB"},
	{"db 39 characters", []string{"MONGO_URI", okMongo, "MONGO_DB", strings.Repeat("a", 39)}, "MONGO_DB"},
}

func TestLoadMongoInvalid(t *testing.T) {
	for _, tt := range mongoInvalid {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := LoadMongo(getenvOf(tt.kv...)); err == nil || !strings.Contains(err.Error(), tt.want+":") {
				t.Errorf("LoadMongo error = %v, want one naming %s", err, tt.want)
			}
			if _, err := Load(getenvOf(withOrigin(tt.kv...)...)); err == nil || !strings.Contains(err.Error(), tt.want+":") {
				t.Errorf("Load error = %v, want one naming %s", err, tt.want)
			}
		})
	}
}

// Option keys are case-insensitive, so ?directConnection=false&directconnection=true
// sets the option twice. Which of the two a driver honors is not defined, and Go
// ranges over a map in random order, so such a URI (and a repeated key) is
// refused, every time, whatever the values.
func TestLoadMongoRefusesADuplicatedDirectConnection(t *testing.T) {
	for _, query := range []string{
		"directConnection=true&directConnection=true",
		"directConnection=true&DIRECTCONNECTION=true",
		"directConnection=false&directconnection=true",
		"directConnection=true&directconnection=false",
	} {
		uri := "mongodb://localhost:27017/?" + query
		for range 100 {
			_, err := LoadMongo(getenvOf("MONGO_URI", uri))
			if err == nil || !strings.Contains(err.Error(), "MONGO_URI:") {
				t.Errorf("LoadMongo(%q) error = %v, want one naming MONGO_URI", uri, err)
				break
			}
		}
	}
}

func TestLoadMongoValid(t *testing.T) {
	tests := []struct {
		name string
		kv   []string
		want MongoConfig
	}{
		{"db defaults to agenda", []string{"MONGO_URI", okMongo}, MongoConfig{URI: okMongo, DB: "agenda"}},
		{"blank db defaults to agenda", []string{"MONGO_URI", okMongo, "MONGO_DB", "  "}, MongoConfig{URI: okMongo, DB: "agenda"}},
		{"directConnection among other options, any key case", []string{"MONGO_URI", "mongodb://db:27017/?retryWrites=true&DirectConnection=true"}, MongoConfig{URI: "mongodb://db:27017/?retryWrites=true&DirectConnection=true", DB: "agenda"}},
		{"values are trimmed", []string{"MONGO_URI", "  " + okMongo + "  ", "MONGO_DB", " agenda_test "}, MongoConfig{URI: okMongo, DB: "agenda_test"}},
		{"db letters digits underscore hyphen", []string{"MONGO_URI", okMongo, "MONGO_DB", "Agenda_2-test"}, MongoConfig{URI: okMongo, DB: "Agenda_2-test"}},
		{"db one character", []string{"MONGO_URI", okMongo, "MONGO_DB", "a"}, MongoConfig{URI: okMongo, DB: "a"}},
		{"db 38 characters", []string{"MONGO_URI", okMongo, "MONGO_DB", strings.Repeat("a", 38)}, MongoConfig{URI: okMongo, DB: strings.Repeat("a", 38)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadMongo(getenvOf(tt.kv...))
			if err != nil || got != tt.want {
				t.Errorf("LoadMongo = %#v, %v; want %#v", got, err, tt.want)
			}
			cfg, err := Load(getenvOf(withOrigin(tt.kv...)...))
			if err != nil || cfg.Mongo != tt.want {
				t.Errorf("Load().Mongo = %#v, %v; want %#v", cfg.Mongo, err, tt.want)
			}
		})
	}
}

// LoadMongo serves the commands that need the database and nothing else (the
// migrate command, the test helper), so unrelated variables must not matter.
func TestLoadMongoIgnoresUnrelatedVariables(t *testing.T) {
	got, err := LoadMongo(getenvOf(
		"MONGO_URI", okMongo,
		"SMTP_PORT", "abc",
		"APP_ENV", "staging",
		"APP_ADDR", "nonsense",
		"APP_ORIGIN", "not a url",
		"APP_TZ", "Mars/Olympus",
		"TRUSTED_PROXY_CIDRS", "notacidr",
	))
	if want := (MongoConfig{URI: okMongo, DB: "agenda"}); err != nil || got != want {
		t.Errorf("LoadMongo = %#v, %v; want %#v", got, err, want)
	}
}

func TestMongoURINeverPrinted(t *testing.T) {
	cfg, err := Load(getenvOf(withOrigin("MONGO_URI", secretMongo, "MONGO_DB", "agenda_prod")...))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Mongo.URI != secretMongo {
		t.Fatalf("URI = %q, want it kept in memory", cfg.Mongo.URI)
	}
	mongo, err := LoadMongo(getenvOf("MONGO_URI", secretMongo))
	if err != nil {
		t.Fatalf("LoadMongo: %v", err)
	}

	var buf bytes.Buffer
	for _, h := range []slog.Handler{slog.NewJSONHandler(&buf, nil), slog.NewTextHandler(&buf, nil)} {
		slog.New(h).Info("cfg", "mongo", cfg.Mongo, "config", cfg)
	}
	asJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	outputs := map[string]string{
		"%v cfg":       fmt.Sprintf("%v", cfg),
		"%+v cfg":      fmt.Sprintf("%+v", cfg),
		"%#v cfg":      fmt.Sprintf("%#v", cfg),
		"%v ptr cfg":   fmt.Sprintf("%v", &cfg),
		"%v mongo":     fmt.Sprintf("%v", cfg.Mongo),
		"%+v mongo":    fmt.Sprintf("%+v", cfg.Mongo),
		"%#v mongo":    fmt.Sprintf("%#v", cfg.Mongo),
		"%s mongo":     fmt.Sprintf("%s", cfg.Mongo),
		"Sprint mongo": fmt.Sprint(cfg.Mongo),
		"%+v *mongo":   fmt.Sprintf("%+v", &cfg.Mongo),
		"%v LoadMongo": fmt.Sprintf("%v", mongo),
		"slog":         buf.String(),
		"json.Marshal": string(asJSON),
		"String":       cfg.Mongo.String(),
		"GoString":     cfg.Mongo.GoString(),
		"LogValue":     cfg.Mongo.LogValue().String(),
		"%+v nested":   fmt.Sprintf("%+v", struct{ M MongoConfig }{cfg.Mongo}),
	}
	for name, out := range outputs {
		for _, leak := range []string{secretMongo, "mongo-secret", "dbuser", "db.example"} {
			if strings.Contains(out, leak) {
				t.Errorf("%s leaks %q: %s", name, leak, out)
			}
		}
	}
	if got := fmt.Sprintf("%v", cfg.Mongo); !strings.Contains(got, "agenda_prod") {
		t.Errorf("%%v mongo = %q, want the database name kept", got)
	}
}

// DATA_DIR went with the SQLite file; the field must not come back.
func TestConfigHasNoDataDir(t *testing.T) {
	if _, ok := reflect.TypeOf(Config{}).FieldByName("DataDir"); ok {
		t.Error("Config.DataDir exists, want it removed")
	}
}
