// Package app wires dependencies. It is the only package that knows which
// adapter backs each interface. Add a new feature by constructing it here
// and registering its routes in Routes and its jobs in Jobs.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/ShammiAnand/go-service-template/internal/auth"
	"github.com/ShammiAnand/go-service-template/internal/config"
	"github.com/ShammiAnand/go-service-template/internal/db"
	"github.com/ShammiAnand/go-service-template/internal/db/dbgen"
	"github.com/ShammiAnand/go-service-template/internal/email"
	"github.com/ShammiAnand/go-service-template/internal/httpapi"
	"github.com/ShammiAnand/go-service-template/internal/jobs"
	"github.com/ShammiAnand/go-service-template/internal/notes"
	"github.com/ShammiAnand/go-service-template/internal/observability"
)

// App holds every long-lived dependency. After New, only Redis can be
// nil (when JOBS_DRIVER=inline).
type App struct {
	Cfg      config.Config
	Log      *slog.Logger
	Obs      observability.Provider
	Pool     *pgxpool.Pool
	Redis    *redis.Client // nil unless JOBS_DRIVER=asynq
	Jobs     *jobs.Registry
	Queue    jobs.Queue
	Email    email.Sender
	Verifier auth.Verifier
	Notes    *notes.Service
}

// New builds the App. Nothing here waits for a network dependency: pools
// and clients connect lazily, so the process starts and reports readiness
// through /readyz.
func New(ctx context.Context, cfg config.Config, log *slog.Logger) (*App, error) {
	a := &App{Cfg: cfg, Log: log}
	var err error

	a.Obs, err = observability.New(ctx, observability.Settings{
		Driver: cfg.Observability.Driver, ServiceName: cfg.ServiceName,
		Version: cfg.Version, Environment: cfg.Env,
	})
	if err != nil {
		return nil, err
	}

	a.Pool, err = db.NewPool(ctx, cfg.Database.URL, cfg.Database.MaxConns)
	if err != nil {
		return nil, err
	}

	a.Email, err = newSender(cfg, log)
	if err != nil {
		return nil, err
	}

	a.Jobs = jobs.NewRegistry(log)
	switch cfg.Jobs.Driver {
	case "asynq":
		opts, err := redis.ParseURL(cfg.Redis.URL)
		if err != nil {
			return nil, fmt.Errorf("parse REDIS_URL: %w", err)
		}
		a.Redis = redis.NewClient(opts)
		a.Queue = jobs.NewAsynqQueue(a.Redis, cfg.Jobs.MaxRetry)
	case "inline":
		a.Queue = jobs.Inline{Registry: a.Jobs}
	}

	a.Verifier = auth.NewKeycloakVerifier(ctx, cfg.Auth.IssuerURL, cfg.Auth.JWKSURL, cfg.Auth.Audience)

	a.Notes = notes.NewService(dbgen.New(a.Pool), a.Queue, log)
	notes.RegisterJobs(a.Jobs, a.Email)
	return a, nil
}

func newSender(cfg config.Config, log *slog.Logger) (email.Sender, error) {
	if cfg.Email.Driver == "log" {
		return email.LogSender{Log: log}, nil
	}
	s := cfg.Email.SMTP
	return email.NewSMTPSender(email.SMTPSettings{
		Host: s.Host, Port: s.Port, Username: s.Username, Password: s.Password, TLS: s.TLS, From: cfg.Email.From,
	})
}

// SpecConfig is a valid offline config used to build the App only to
// dump the OpenAPI document. Nothing connects with it.
func SpecConfig() config.Config {
	return config.Config{
		Env: config.EnvTest, ServiceName: "spec", Version: "spec", LogLevel: "error",
		Database:      config.Database{URL: "postgres://spec@localhost:1/spec", MaxConns: 1},
		Auth:          config.Auth{IssuerURL: "http://localhost/spec", JWKSURL: "http://localhost/spec/certs", Audience: "api"},
		Email:         config.Email{Driver: "log"},
		Jobs:          config.Jobs{Driver: "inline", Concurrency: 1},
		Observability: config.Observability{Driver: "noop"},
	}
}

// Routes registers every HTTP operation.
func Routes(api huma.API, a *App) {
	checks := map[string]httpapi.Check{"postgres": a.Pool.Ping}
	if a.Redis != nil {
		checks["redis"] = func(ctx context.Context) error { return a.Redis.Ping(ctx).Err() }
	}
	httpapi.RegisterHealth(api, checks)
	notes.Routes(api, a.Notes)
}

// Close releases resources in reverse order of creation.
func (a *App) Close(ctx context.Context) error {
	var errs []error
	if a.Redis != nil {
		errs = append(errs, a.Redis.Close())
	}
	a.Pool.Close()
	errs = append(errs, a.Obs.Shutdown(ctx))
	return errors.Join(errs...)
}

// HTTPHandler builds the engine with middleware, the Huma API and all
// routes. ctx bounds background goroutines (rate limiter janitor).
func (a *App) HTTPHandler(ctx context.Context) (http.Handler, huma.API, error) {
	engine, err := httpapi.NewEngine(ctx, a.Cfg, a.Log)
	if err != nil {
		return nil, nil, err
	}
	api := httpapi.NewAPI(engine, a.Verifier, a.Cfg.HTTP.DocsEnabled)
	Routes(api, a)
	return engine, api, nil
}
