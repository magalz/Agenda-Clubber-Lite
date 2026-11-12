package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"testing"
)

const okOrigin = "https://agenda.example"

// getenvOf builds a getenv from key/value pairs; later pairs win.
func getenvOf(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

// withOrigin returns kv preceded by a valid APP_ORIGIN.
func withOrigin(kv ...string) []string {
	return append([]string{"APP_ORIGIN", okOrigin}, kv...)
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(getenvOf("APP_ORIGIN", okOrigin))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "prod" {
		t.Errorf("Env = %q, want prod", cfg.Env)
	}
	if cfg.IsDev() {
		t.Error("IsDev() = true, want false")
	}
	if cfg.Addr != ":8120" {
		t.Errorf("Addr = %q, want :8120", cfg.Addr)
	}
	if cfg.Origin != okOrigin {
		t.Errorf("Origin = %q, want %q", cfg.Origin, okOrigin)
	}
	if cfg.Location == nil || cfg.Location.String() != "America/Fortaleza" {
		t.Errorf("Location = %v, want America/Fortaleza", cfg.Location)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want ./data", cfg.DataDir)
	}
	if len(cfg.TrustedProxyCIDRs) != 0 {
		t.Errorf("TrustedProxyCIDRs = %v, want empty", cfg.TrustedProxyCIDRs)
	}
	want := SMTPConfig{Port: 587, From: "IgnisDev <ignisdev@magalz.space>"}
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
		"DATA_DIR", "/var/lib/agenda",
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
	if cfg.Addr != "127.0.0.1:9000" || cfg.Origin != "http://localhost:9000" || cfg.DataDir != "/var/lib/agenda" {
		t.Errorf("Addr/Origin/DataDir = %q/%q/%q", cfg.Addr, cfg.Origin, cfg.DataDir)
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
			cfg, err := Load(getenvOf("APP_ORIGIN", tt.in))
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
			cfg, err := Load(getenvOf(withOrigin("APP_ADDR", addr)...))
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
			cfg, err := Load(getenvOf(withOrigin("TRUSTED_PROXY_CIDRS", tt.in)...))
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
		{"bad env", withOrigin("APP_ENV", "staging"), "APP_ENV"},
		{"env is case sensitive", withOrigin("APP_ENV", "DEV"), "APP_ENV"},
		{"addr without port", withOrigin("APP_ADDR", "localhost"), "APP_ADDR"},
		{"addr port only digits", withOrigin("APP_ADDR", "8120"), "APP_ADDR"},
		{"addr non numeric port", withOrigin("APP_ADDR", ":http"), "APP_ADDR"},
		{"addr empty port", withOrigin("APP_ADDR", "localhost:"), "APP_ADDR"},
		{"addr port too large", withOrigin("APP_ADDR", ":99999"), "APP_ADDR"},
		{"addr negative port", withOrigin("APP_ADDR", ":-1"), "APP_ADDR"},
		{"bad tz", withOrigin("APP_TZ", "Mars/Olympus"), "APP_TZ"},
		{"bad cidr", withOrigin("TRUSTED_PROXY_CIDRS", "10.0.0.0/8,notacidr"), "TRUSTED_PROXY_CIDRS"},
		{"cidr without prefix", withOrigin("TRUSTED_PROXY_CIDRS", "10.0.0.1"), "TRUSTED_PROXY_CIDRS"},
		{"cidr prefix too long", withOrigin("TRUSTED_PROXY_CIDRS", "10.0.0.0/33"), "TRUSTED_PROXY_CIDRS"},
		{"smtp port not a number", withOrigin("SMTP_PORT", "abc"), "SMTP_PORT"},
		{"smtp port zero", withOrigin("SMTP_PORT", "0"), "SMTP_PORT"},
		{"smtp port negative", withOrigin("SMTP_PORT", "-1"), "SMTP_PORT"},
		{"smtp port too large", withOrigin("SMTP_PORT", "65536"), "SMTP_PORT"},
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
}

func TestSMTPPasswordNeverPrinted(t *testing.T) {
	cfg, err := Load(getenvOf(withOrigin("SMTP_PASSWORD", "smtp-secret", "SMTP_USERNAME", "user")...))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SMTP.Password != "smtp-secret" {
		t.Fatalf("Password = %q, want it kept in memory", cfg.SMTP.Password)
	}
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("cfg", "smtp", cfg.SMTP)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("cfg", "smtp", cfg.SMTP)
	outputs := map[string]string{
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
