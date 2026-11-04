# AGENTS.md

This file tells coding agents (and people) how this repository works. Read it before you change code.

## The one rule

Run `make check` before you say that a change is done. It formats, regenerates code, fails on stale generated files, lints, and runs unit and integration tests. Integration tests need Docker.

## Map

| Path | What it holds |
| --- | --- |
| `cmd/app` | The only binary. Subcommands: `serve`, `worker`, `migrate`, `migrate-new`, `openapi`, `healthcheck`. |
| `internal/app` | Wiring. The only package that picks an adapter for each interface. |
| `internal/config` | Environment variables, parsed and validated once in `Load`. |
| `internal/httpapi` | Gin engine, middleware, Huma API, `Register`, `MapError`, health probes. |
| `internal/auth` | Keycloak token verification and the `Principal` in the request context. |
| `internal/apperr` | Domain errors: `Invalid`, `NotFound`, `Conflict`, `Unauthorized`, `Forbidden`. |
| `internal/db` | Pool, embedded goose migrations (`migrations/`), SQL queries (`queries/`). |
| `internal/db/dbgen` | sqlc output. Generated. Do not edit. |
| `internal/email` | `Sender` interface, `smtp` and `log` adapters, templates. |
| `internal/jobs` | `Queue` interface, typed jobs, `asynq` (Redis) and `inline` adapters. |
| `internal/observability` | `Provider` interface, `otel` and `noop` adapters. |
| `internal/logging` | JSON slog logger. Adds `trace_id`, `span_id`, `request_id` from the context. |
| `internal/platform/safe` | Panic recovery for goroutines (`safe.Go`, `safe.Call`). |
| `internal/notes` | The worked example feature. Copy its shape. |
| `internal/testutil` | Test helpers, for example a fake Keycloak issuer that signs tokens. |
| `api/openapi.yaml` | Generated from Go types. Do not edit. |

## Error and nil rules

These rules keep code short. Follow them.

1. Validate at the edges only. `config.Load` validates configuration. Huma validates requests from struct tags before your handler runs. Inner code trusts its inputs.
2. Never return a nil dependency. A constructor returns a usable value. If a provider is not configured, wire a `noop` or `log` adapter. Do not write `if x != nil` for dependencies.
3. Handlers do not choose error status codes. Return the error. `httpapi.MapError` maps `apperr` kinds, `pgx.ErrNoRows` (404) and Postgres constraint errors (409 or 422). Any other error becomes a 500 with no details in the body.
4. Always handle errors from I/O: database, Redis, SMTP, network, files. The `errcheck` linter enforces this.
5. If something cannot happen because of wiring, panic with a clear message (see `auth.MustFromContext`). Recovery middleware turns the panic into a 500, and the server keeps running.
6. Start background goroutines with `safe.Go`, never with a bare `go`. Job handlers already run inside panic recovery.
7. Log with `slog.InfoContext(ctx, ...)` and other `*Context` methods, so that trace and request ids are added. Never log tokens, passwords, or message bodies.

## Recipes

### Add an endpoint

1. In the feature package, define input and output structs. Path, query and header fields use `path:"..."`, `query:"..."` and `header:"..."` tags. The request body goes in a field named `Body`. Add validation tags such as `minLength`, `maximum`, `format:"uuid"`.
2. Do not embed an unexported struct to share fields. Huma skips it. Write the fields out.
3. Register with `httpapi.Register(api, huma.Operation{...}, handler)`. Set `Security: httpapi.BearerAuth` for authenticated routes. Use `httpapi.RequireRole(op, "admin")` for a role.
4. Get the caller with `auth.MustFromContext(ctx)`.
5. Call the routes function from `app.Routes`.
6. Run `make gen`. The new operation appears in `api/openapi.yaml`.
7. Add a unit test. If the endpoint touches SQL, extend `internal/app/integration_test.go`.

### Add a table and queries

1. `make migrate-new NAME=create_widgets`. Edit the new file in `internal/db/migrations/`. Write both `-- +goose Up` and `-- +goose Down`.
2. Add queries to `internal/db/queries/<table>.sql` with `-- name: Foo :one|:many|:exec|:execrows`. Use `sqlc.arg(name)` for parameters.
3. Filter by owner (or tenant) in SQL, not in Go.
4. `make gen`. Use the generated methods through the `dbgen.Querier` interface so that unit tests can use a fake.

### Add a background job

1. Declare it: `var MyJob = jobs.Define[MyPayload]("feature:my_job")`.
2. Enqueue: `MyJob.Enqueue(ctx, queue, payload)`.
3. Handle: `jobs.Handle(reg, MyJob, func(ctx context.Context, p MyPayload) error {...})` in a `RegisterJobs` function, called from `app.New`.
4. If a retry cannot help (for example, bad input), return `jobs.Permanent(err)`. To retry with backoff, return a plain error.
5. Make handlers idempotent (safe to run twice). The queue delivers at least once.

### Add an email

1. Add `internal/email/templates/<name>.txt.tmpl` with `{{define "<name>.subject"}}...{{end}}` at the top, and `<name>.html.tmpl`.
2. `email.Render("<name>", to, data)`, then `sender.Send(ctx, msg)`. Send from a job, not from the request path.

### Add an adapter (email, jobs, observability)

1. Implement the interface in the package (`email.Sender`, `jobs.Queue`, `observability.Provider`).
2. Add the driver name to the `oneOf` list in `config.Validate`, and add its fields to `config.Config`.
3. Select it in `internal/app`.

## Generated files

`internal/db/dbgen/*` and `api/openapi.yaml` come from `make gen`. Never edit them by hand. If they are stale, CI fails.

## Local stack

`make up` starts the API, worker, Postgres, Redis, Keycloak, Mailpit and Jaeger. `make token` prints an access token for user `dev` (password `dev`). User `admin` (password `admin`) has the `admin` role. These credentials are for local development only.

## Do not

- Do not add a dependency without an exact version. Run `go get pkg@vX.Y.Z`.
- Do not disable TLS verification, or accept JWT algorithms other than RS256.
- Do not put secrets in code, tests, logs, or `compose.yaml` beyond the local dev defaults.
- Do not build SQL strings by hand. Write a sqlc query.
