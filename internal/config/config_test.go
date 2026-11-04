package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/app")
	t.Setenv("AUTH_ISSUER_URL", "http://localhost:8081/realms/app/")
}

func TestLoadDefaults(t *testing.T) {
	setRequired(t)
	cfg, err := Load()
	require.NoError(t, err)
	assert.Equal(t, EnvDevelopment, cfg.Env)
	assert.Equal(t, "http://localhost:8081/realms/app", cfg.Auth.IssuerURL, "trailing slash trimmed")
	assert.Equal(t, "http://localhost:8081/realms/app/protocol/openid-connect/certs", cfg.Auth.JWKSURL)
	assert.Equal(t, "asynq", cfg.Jobs.Driver)
}

func TestLoadRejectsMissingAndInvalid(t *testing.T) {
	t.Setenv("JOBS_DRIVER", "kafka")
	_, err := Load()
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "DATABASE_URL is required")
	assert.Contains(t, msg, "AUTH_ISSUER_URL must be an absolute URL")
	assert.Contains(t, msg, `JOBS_DRIVER must be one of`)
}

func TestProductionRules(t *testing.T) {
	setRequired(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("HTTP_CORS_ORIGINS", "*")
	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not contain *")
	assert.Contains(t, err.Error(), "must use https")
}
