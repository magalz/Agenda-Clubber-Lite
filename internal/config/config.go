// Package config loads the service configuration from the environment.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"regexp"
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
	Password string `json:"-"` // secret: kept out of encoding/json output
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

// MongoConfig holds the MongoDB connection settings.
type MongoConfig struct {
	URI string `json:"-"` // secret: it may carry credentials; kept out of encoding/json output
	DB  string
}

// String redacts the URI so a printed Config cannot leak its credentials.
func (m MongoConfig) String() string {
	return fmt.Sprintf("{URI:[redacted] DB:%s}", m.DB)
}

// GoString makes %#v redact the URI as well.
func (m MongoConfig) GoString() string { return m.String() }

// LogValue keeps the URI out of slog output.
func (m MongoConfig) LogValue() slog.Value {
	return slog.GroupValue(slog.String("db", m.DB))
}

// mongoDBName is what MONGO_DB may hold: MongoDB itself caps a database name
// at 63 bytes, and this keeps room for a suffix such as a test database's.
var mongoDBName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,38}$`)

// LoadMongo reads and validates MONGO_URI and MONGO_DB, and no other variable:
// a command that needs only the database (migrate, the test helper) must not
// fail on, say, an SMTP setting it never uses. Errors name the variable and
// never echo a value, so credentials in the URI cannot end up in a log.
func LoadMongo(getenv func(string) string) (MongoConfig, error) {
	uri := trimmedEnv(getenv, "MONGO_URI", "")
	if uri == "" {
		return MongoConfig{}, fmt.Errorf("MONGO_URI: required")
	}
	// Only mongodb://: the server is a single-node replica set reached with
	// directConnection=true, which the Go driver refuses for mongodb+srv://.
	rest, ok := strings.CutPrefix(uri, "mongodb://")
	if !ok || rest == "" {
		return MongoConfig{}, fmt.Errorf("MONGO_URI: must start with mongodb://")
	}
	if !hasDirectConnection(rest) {
		return MongoConfig{}, fmt.Errorf("MONGO_URI: must carry directConnection=true (single-node replica set)")
	}
	db := trimmedEnv(getenv, "MONGO_DB", "agenda")
	if !mongoDBName.MatchString(db) {
		return MongoConfig{}, fmt.Errorf("MONGO_DB: must be 1 to 38 letters, digits, _ or -")
	}
	return MongoConfig{URI: uri, DB: db}, nil
}

// hasDirectConnection reports whether the options part of a mongodb:// URI
// (after the scheme) sets directConnection=true, once. Option keys are
// case-insensitive in the connection string format; the value is not. An option
// that is set twice, by a repeated key or by two keys that differ only by case,
// is refused: which one a driver honors is not defined, and the map order must
// not decide.
func hasDirectConnection(rest string) bool {
	_, query, found := strings.Cut(rest, "?")
	if !found {
		return false
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return false
	}
	var matches [][]string
	for key, vals := range values {
		if strings.EqualFold(key, "directConnection") {
			matches = append(matches, vals)
		}
	}
	return len(matches) == 1 && len(matches[0]) == 1 && matches[0][0] == "true"
}

// Config is the validated service configuration.
type Config struct {
	Env               string // "dev" or "prod"
	Addr              string // listen address, host:port
	Origin            string // scheme://host[:port], no trailing slash
	Location          *time.Location
	TrustedProxyCIDRs []string // validated CIDR strings
	SMTP              SMTPConfig
	Mongo             MongoConfig
}

// IsDev reports whether the service runs in development mode.
func (c Config) IsDev() bool { return c.Env == "dev" }

// Load reads and validates the configuration through getenv. The first
// invalid variable ends the load; its error names the variable. Errors never
// echo values, so a secret set in the wrong variable cannot end up in a log.
func Load(getenv func(string) string) (Config, error) {
	get := func(key, def string) string { return trimmedEnv(getenv, key, def) }
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

	if cfg.Mongo, err = LoadMongo(getenv); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// trimmedEnv returns the value of key without surrounding spaces, or def when
// it is empty.
func trimmedEnv(getenv func(string) string, key, def string) string {
	if v := strings.TrimSpace(getenv(key)); v != "" {
		return v
	}
	return def
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
