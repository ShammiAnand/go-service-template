// Package db owns the Postgres pool and schema migrations. Queries live in
// queries/*.sql; `make gen` turns them into typed Go in dbgen/.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// MigrationsDir is where `make migrate-new` writes new files.
const MigrationsDir = "internal/db/migrations"

// NewPool builds a pool. It does not connect: connections open on first
// use, so the app starts while Postgres is down and /readyz reports it.
func NewPool(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.ConnConfig.Tracer = otelpgx.NewTracer()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	return pool, nil
}

// Migrate runs a goose command ("up", "down", "status") with the embedded
// migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool, command string, log *slog.Logger) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() { _ = sqlDB.Close() }()

	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, sub)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	switch command {
	case "up":
		res, err := p.Up(ctx)
		for _, r := range res {
			log.InfoContext(ctx, "migration applied", "version", r.Source.Version, "path", r.Source.Path, "duration_ms", r.Duration.Milliseconds())
		}
		return err
	case "down":
		r, err := p.Down(ctx)
		if r != nil {
			log.InfoContext(ctx, "migration rolled back", "version", r.Source.Version, "path", r.Source.Path)
		}
		return err
	case "status":
		st, err := p.Status(ctx)
		for _, s := range st {
			log.InfoContext(ctx, "migration status", "version", s.Source.Version, "path", s.Source.Path, "state", s.State)
		}
		return err
	default:
		return fmt.Errorf("unknown migrate command %q (want up, down, status)", command)
	}
}

// CreateMigration writes a new timestamped SQL migration file.
func CreateMigration(name string) error {
	return goose.Create((*sql.DB)(nil), MigrationsDir, name, "sql")
}
