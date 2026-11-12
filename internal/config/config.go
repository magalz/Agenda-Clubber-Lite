// Package config loads the service configuration from the environment.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultAddr is the listen address when APP_ADDR is unset. The healthcheck
// subcommand, which reads APP_ADDR without loading the full Config, falls back
// to it too.
const DefaultAddr = ":8120"

// SMTPConfig holds the outgoing mail settings.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// String redacts the password so a printed Config cannot leak it.
func (s SMTPConfig) String() string {
	return fmt.Sprintf("{Host:%s Port:%d Username:%s Password:[redacted] From:%s}", s.Host, s.Port, s.Username, s.From)
}

// GoString makes %#v redact the password as well.
func (s SMTPConfig) GoString() string { return s.String() }

// LogValue keeps the password out of slog output.
func (s SMTPConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", s.Host),
		slog.Int("port", s.Port),
		slog.String("username", s.Username),
		slog.String("from", s.From),
	)
}

// Config is the validated service configuration.
type Config struct {
	Env               string // "dev" or "prod"
	Addr              string // listen address, host:port
	Origin            string // scheme://host[:port], no trailing slash
	Location          *time.Location
	DataDir           string
	TrustedProxyCIDRs []string // validated CIDR strings
	SMTP              SMTPConfig
}

// IsDev reports whether the service runs in development mode.
func (c Config) IsDev() bool { return c.Env == "dev" }

// Load reads and validates the configuration through getenv. The first
// invalid variable ends the load; its error names the variable. Errors never
// echo values, so a secret set in the wrong variable cannot end up in a log.
func Load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	var cfg Config
	var err error

	cfg.Env = get("APP_ENV", "prod")
	if cfg.Env != "dev" && cfg.Env != "prod" {
		return Config{}, fmt.Errorf(`APP_ENV: must be "dev" or "prod"`)
	}

	cfg.Addr = get("APP_ADDR", DefaultAddr)
	if _, port, err := net.SplitHostPort(cfg.Addr); err != nil {
		return Config{}, fmt.Errorf("APP_ADDR: must be host:port")
	} else if _, err := strconv.ParseUint(port, 10, 16); err != nil {
		return Config{}, fmt.Errorf("APP_ADDR: port must be a number from 0 to 65535")
	}

	if cfg.Origin, err = parseOrigin(getenv("APP_ORIGIN")); err != nil {
		return Config{}, fmt.Errorf("APP_ORIGIN: %w", err)
	}

	if cfg.Location, err = time.LoadLocation(get("APP_TZ", "America/Fortaleza")); err != nil {
		return Config{}, fmt.Errorf("APP_TZ: unknown IANA time zone")
	}

	cfg.DataDir = get("DATA_DIR", "./data")

	for _, entry := range strings.Split(getenv("TRUSTED_PROXY_CIDRS"), ",") {
		if entry = strings.TrimSpace(entry); entry == "" {
			continue
		}
		if _, err := netip.ParsePrefix(entry); err != nil {
			return Config{}, fmt.Errorf("TRUSTED_PROXY_CIDRS: invalid CIDR %q", entry)
		}
		cfg.TrustedProxyCIDRs = append(cfg.TrustedProxyCIDRs, entry)
	}

	cfg.SMTP.Host = get("SMTP_HOST", "")
	if cfg.SMTP.Port, err = strconv.Atoi(get("SMTP_PORT", "587")); err != nil || cfg.SMTP.Port < 1 || cfg.SMTP.Port > 65535 {
		return Config{}, fmt.Errorf("SMTP_PORT: must be a number from 1 to 65535")
	}
	cfg.SMTP.Username = get("SMTP_USERNAME", "")
	cfg.SMTP.Password = getenv("SMTP_PASSWORD") // verbatim: a password may carry spaces
	cfg.SMTP.From = get("SMTP_FROM", "IgnisDev <ignisdev@magalz.space>")

	return cfg, nil
}

// parseOrigin returns raw as scheme://host[:port]: lower case, no trailing
// slash and no port that is the scheme's default (browsers omit it from the
// Origin header, so keeping it would make every same-origin check fail).
func parseOrigin(raw string) (string, error) {
	errInvalid := fmt.Errorf("must be an absolute http(s) origin")
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "?#") {
		return "", errInvalid
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil ||
		u.Hostname() == "" || (u.Path != "" && u.Path != "/") {
		return "", errInvalid
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") { // IPv6 literal
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && !(u.Scheme == "http" && port == "80") && !(u.Scheme == "https" && port == "443") {
		host += ":" + port
	}
	return u.Scheme + "://" + host, nil
}
