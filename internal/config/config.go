// Package config loads the service configuration from the environment.
package config

import "time"

// SMTPConfig holds the outgoing mail settings.
type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// Config is the validated service configuration.
type Config struct {
	Env               string
	Addr              string
	Origin            string
	Location          *time.Location
	DataDir           string
	TrustedProxyCIDRs []string
	SMTP              SMTPConfig
}

// IsDev reports whether the service runs in development mode.
func (c Config) IsDev() bool { return false }

// Load reads and validates the configuration through getenv.
func Load(getenv func(string) string) (Config, error) { return Config{}, nil }
