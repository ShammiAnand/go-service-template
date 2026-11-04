// Package config loads and validates all runtime configuration from
// environment variables. Load is the only place that validates config:
// code that receives a Config can trust every field.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Environment names.
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
	EnvTest        = "test"
)

// Config is the full application configuration.
type Config struct {
	Env         string `env:"APP_ENV" envDefault:"development"`
	ServiceName string `env:"SERVICE_NAME" envDefault:"go-service-template"`
	Version     string `env:"SERVICE_VERSION" envDefault:"dev"`
	LogLevel    string `env:"LOG_LEVEL" envDefault:"info"`

	HTTP          HTTP          `envPrefix:"HTTP_"`
	Database      Database      `envPrefix:"DATABASE_"`
	Redis         Redis         `envPrefix:"REDIS_"`
	Auth          Auth          `envPrefix:"AUTH_"`
	Email         Email         `envPrefix:"EMAIL_"`
	Jobs          Jobs          `envPrefix:"JOBS_"`
	Observability Observability `envPrefix:"OBSERVABILITY_"`
}

// HTTP configures the API server.
type HTTP struct {
	Addr            string        `env:"ADDR" envDefault:":8080"`
	ReadTimeout     time.Duration `env:"READ_TIMEOUT" envDefault:"10s"`
	WriteTimeout    time.Duration `env:"WRITE_TIMEOUT" envDefault:"15s"`
	IdleTimeout     time.Duration `env:"IDLE_TIMEOUT" envDefault:"60s"`
	ShutdownTimeout time.Duration `env:"SHUTDOWN_TIMEOUT" envDefault:"20s"`
	BodyLimitBytes  int64         `env:"BODY_LIMIT_BYTES" envDefault:"1048576"`
	CORSOrigins     []string      `env:"CORS_ORIGINS" envSeparator:","`
	TrustedProxies  []string      `env:"TRUSTED_PROXIES" envSeparator:","`
	RateLimitRPS    float64       `env:"RATE_LIMIT_RPS" envDefault:"20"`
	RateLimitBurst  int           `env:"RATE_LIMIT_BURST" envDefault:"40"`
	DocsEnabled     bool          `env:"DOCS_ENABLED" envDefault:"true"`
}

// Database configures the Postgres pool.
type Database struct {
	URL      string `env:"URL"`
	MaxConns int32  `env:"MAX_CONNS" envDefault:"10"`
}

// Redis configures the Redis client used by the asynq job driver.
type Redis struct {
	URL string `env:"URL" envDefault:"redis://localhost:6379/0"`
}

// Auth configures bearer token verification against Keycloak.
type Auth struct {
	// IssuerURL must equal the "iss" claim in tokens, for example
	// http://localhost:8081/realms/app.
	IssuerURL string `env:"ISSUER_URL"`
	// JWKSURL is where signing keys are fetched. Defaults to the Keycloak
	// certs endpoint under IssuerURL. Set it when the app reaches Keycloak
	// on a different host than the one in the issuer (for example inside
	// docker compose).
	JWKSURL  string `env:"JWKS_URL"`
	Audience string `env:"AUDIENCE" envDefault:"api"`
}

// Email configures outbound mail.
type Email struct {
	Driver string `env:"DRIVER" envDefault:"log"` // smtp | log
	From   string `env:"FROM" envDefault:"no-reply@example.com"`
	SMTP   SMTP   `envPrefix:"SMTP_"`
}

// SMTP configures the smtp email driver.
type SMTP struct {
	Host     string `env:"HOST" envDefault:"localhost"`
	Port     int    `env:"PORT" envDefault:"1025"`
	Username string `env:"USERNAME"`
	Password string `env:"PASSWORD"`
	// TLS is one of: mandatory | opportunistic | none.
	TLS string `env:"TLS" envDefault:"mandatory"`
}

// Jobs configures background job processing.
type Jobs struct {
	Driver      string `env:"DRIVER" envDefault:"asynq"` // asynq | inline
	Concurrency int    `env:"CONCURRENCY" envDefault:"10"`
	MaxRetry    int    `env:"MAX_RETRY" envDefault:"10"`
}

// Observability selects the telemetry backend. With "otel", the exporter
// reads the standard OTEL_EXPORTER_OTLP_* environment variables.
type Observability struct {
	Driver string `env:"DRIVER" envDefault:"noop"` // otel | noop
}

// Load reads the environment and validates the result.
func Load() (Config, error) {
	cfg, err := env.ParseAs[Config]()
	if err != nil {
		return Config{}, fmt.Errorf("parse env: %w", err)
	}
	cfg.applyDerived()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDerived() {
	c.Auth.IssuerURL = strings.TrimRight(c.Auth.IssuerURL, "/")
	if c.Auth.JWKSURL == "" && c.Auth.IssuerURL != "" {
		c.Auth.JWKSURL = c.Auth.IssuerURL + "/protocol/openid-connect/certs"
	}
}

// Validate returns every problem found, joined.
func (c Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	oneOf := func(name, v string, allowed ...string) {
		if !slices.Contains(allowed, v) {
			add("%s must be one of %v, got %q", name, allowed, v)
		}
	}
	oneOf("APP_ENV", c.Env, EnvDevelopment, EnvProduction, EnvTest)
	oneOf("LOG_LEVEL", c.LogLevel, "debug", "info", "warn", "error")
	oneOf("EMAIL_DRIVER", c.Email.Driver, "smtp", "log")
	oneOf("JOBS_DRIVER", c.Jobs.Driver, "asynq", "inline")
	oneOf("OBSERVABILITY_DRIVER", c.Observability.Driver, "otel", "noop")

	if c.Database.URL == "" {
		add("DATABASE_URL is required")
	}
	if c.Database.MaxConns < 1 {
		add("DATABASE_MAX_CONNS must be >= 1")
	}
	for name, u := range map[string]string{"AUTH_ISSUER_URL": c.Auth.IssuerURL, "AUTH_JWKS_URL": c.Auth.JWKSURL} {
		if _, err := url.ParseRequestURI(u); err != nil || u == "" {
			add("%s must be an absolute URL", name)
		}
	}
	if c.Auth.Audience == "" {
		add("AUTH_AUDIENCE is required")
	}
	if c.HTTP.BodyLimitBytes < 1 {
		add("HTTP_BODY_LIMIT_BYTES must be >= 1")
	}
	if c.HTTP.RateLimitRPS <= 0 || c.HTTP.RateLimitBurst < 1 {
		add("HTTP_RATE_LIMIT_RPS must be > 0 and HTTP_RATE_LIMIT_BURST >= 1")
	}
	if c.Jobs.Concurrency < 1 {
		add("JOBS_CONCURRENCY must be >= 1")
	}
	if c.Email.Driver == "smtp" {
		oneOf("EMAIL_SMTP_TLS", c.Email.SMTP.TLS, "mandatory", "opportunistic", "none")
		if c.Email.SMTP.Host == "" {
			add("EMAIL_SMTP_HOST is required when EMAIL_DRIVER=smtp")
		}
	}

	if c.Env == EnvProduction {
		if c.Email.SMTP.TLS == "none" && c.Email.Driver == "smtp" {
			add("EMAIL_SMTP_TLS=none is not allowed in production")
		}
		if slices.Contains(c.HTTP.CORSOrigins, "*") {
			add("HTTP_CORS_ORIGINS must not contain * in production")
		}
		if strings.HasPrefix(c.Auth.IssuerURL, "http://") {
			add("AUTH_ISSUER_URL must use https in production")
		}
	}
	return errors.Join(errs...)
}

// IsProduction reports whether the app runs in production.
func (c Config) IsProduction() bool { return c.Env == EnvProduction }
