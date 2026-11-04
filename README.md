# go-service-template

A Go API service template that is ready for production and for coding agents.

- HTTP: [Gin](https://gin-gonic.com) with [Huma](https://huma.rocks). Request and response types are Go structs. Huma validates requests and generates the OpenAPI 3.1 document from them.
- Data: Postgres with pgx, [sqlc](https://sqlc.dev) for typed queries, [goose](https://github.com/pressly/goose) migrations embedded in the binary.
- Auth: Keycloak access tokens (RS256). The app accepts a token only if the issuer, audience and expiry are correct. Handlers can read the Keycloak roles of the caller.
- Interfaces with swappable adapters: observability (OpenTelemetry or noop), email (SMTP or log), background jobs (asynq on Redis, or inline).
- Recovery: panics in handlers, jobs and goroutines become logged errors. The process keeps running. `/readyz` reports a lost database or Redis, and the app reconnects without a restart.
- JSON logs with `trace_id`, `span_id` and `request_id` on each line.
- One binary, a distroless non-root image, Docker Compose for local development, and GitHub Actions CI.

## Quick start

You need Go 1.27 and Docker.

```sh
make up                 # API, worker, Postgres, Redis, Keycloak, Mailpit, Jaeger
TOKEN=$(make -s token)  # access token for user dev/dev
curl -X POST localhost:8080/v1/notes \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"title":"hello"}'
```

| URL | What |
| --- | --- |
| http://localhost:8080/docs | API reference |
| http://localhost:8025 | Mailpit: mail the app sent |
| http://localhost:16686 | Jaeger: traces |
| http://localhost:8081 | Keycloak admin (admin / admin) |

To run the API on your machine instead of in Docker: `cp .env.example .env`, `make deps`, `make migrate-up`, `make run`.

## Commands

Run `make help` for the full list.

| Command | What it does |
| --- | --- |
| `make check` | Format, regenerate, lint, unit and integration tests. CI runs the same steps. |
| `make gen` | Regenerate sqlc code and `api/openapi.yaml`. |
| `make migrate-new NAME=x` | Create a migration. |
| `make test` / `make test-int` | Unit tests / integration tests (Docker). |

## Start a new service from this template

Use one of these:

```sh
# Option 1: gonew rewrites the Go module path.
go run golang.org/x/tools/cmd/gonew@latest github.com/ShammiAnand/go-service-template example.com/acme/billing

# Option 2: GitHub "Use this template", then in your new repo:
make rename MODULE=example.com/acme/billing NAME=billing
```

`gonew` changes only Go import paths. `make rename` also changes the service name in configuration, Compose and docs.

## Configuration

All configuration comes from environment variables. `internal/config/config.go` lists each variable with its default. If a value is invalid, the app does not start. It reports every problem at once. Production mode (`APP_ENV=production`) adds stricter rules, for example HTTPS for the token issuer.

## Working with coding agents

Read [AGENTS.md](AGENTS.md). It holds the code map, the error rules, and step-by-step recipes for endpoints, tables, jobs, emails and adapters. `CLAUDE.md` points to the same file.

## Credits

Inspired by [sriniously/go-boilerplate](https://github.com/sriniously/go-boilerplate). This repository is a separate implementation.

## License

[Apache-2.0](LICENSE)
