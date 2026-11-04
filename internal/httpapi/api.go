// Package httpapi builds the HTTP server: Gin engine, Huma API (typed
// operations and OpenAPI 3.1), middleware, and the single error mapper.
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humagin"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ShammiAnand/go-service-template/internal/apperr"
	"github.com/ShammiAnand/go-service-template/internal/auth"
)

// API metadata used in the OpenAPI document.
const (
	Title      = "go-service-template API"
	APIVersion = "1.0.0"
	// SecurityBearer is the security scheme name. Put
	// Security: BearerAuth on every operation that needs a caller.
	SecurityBearer = "bearer"
	// metaRole is the Operation.Metadata key for a required realm role.
	metaRole = "requiredRole"
)

// BearerAuth is the Security value for authenticated operations.
var BearerAuth = []map[string][]string{{SecurityBearer: {}}}

// RequireRole marks op as needing a realm role in addition to a token.
func RequireRole(op huma.Operation, role string) huma.Operation {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[metaRole] = role
	op.Security = BearerAuth
	return op
}

var scrubOnce sync.Once

// scrubServerErrors stops Huma from echoing internal error text in 5xx
// bodies. Client errors (4xx) keep their details, for example validation.
func scrubServerErrors() {
	scrubOnce.Do(func() {
		orig := huma.NewError
		huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
			if status >= http.StatusInternalServerError {
				return orig(status, http.StatusText(status))
			}
			return orig(status, msg, errs...)
		}
	})
}

// NewAPI registers Huma on engine. verifier may be nil only when the API
// is built to dump the OpenAPI document (no requests are served).
func NewAPI(engine *gin.Engine, verifier auth.Verifier, docsEnabled bool) huma.API {
	scrubServerErrors()

	cfg := huma.DefaultConfig(Title, APIVersion)
	cfg.CreateHooks = nil // no $schema field or Link header in responses
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		SecurityBearer: {Type: "http", Scheme: "bearer", BearerFormat: "JWT"},
	}
	if !docsEnabled {
		cfg.DocsPath = ""
	}
	api := humagin.New(engine, cfg)
	if verifier != nil {
		api.UseMiddleware(authMiddleware(api, verifier))
	}
	return api
}

// Handler is the signature every operation handler implements.
type Handler[I, O any] func(ctx context.Context, in *I) (*O, error)

// Register adds a typed operation. Errors returned by h go through
// MapError, so handlers just `return nil, err`.
func Register[I, O any](api huma.API, op huma.Operation, h Handler[I, O]) {
	huma.Register(api, op, func(ctx context.Context, in *I) (*O, error) {
		out, err := h(ctx, in)
		if err != nil {
			return nil, MapError(ctx, err)
		}
		return out, nil
	})
}

// MapError converts any error into a Huma status error. This is the only
// place that decides HTTP status codes for errors.
func MapError(ctx context.Context, err error) huma.StatusError {
	var se huma.StatusError
	if errors.As(err, &se) {
		return se
	}
	if e, ok := apperr.As(err); ok {
		switch e.Kind {
		case apperr.KindInvalid:
			return huma.Error422UnprocessableEntity(e.Msg)
		case apperr.KindNotFound:
			return huma.Error404NotFound(e.Msg)
		case apperr.KindConflict:
			return huma.Error409Conflict(e.Msg)
		case apperr.KindUnauthorized:
			return huma.Error401Unauthorized(e.Msg)
		case apperr.KindForbidden:
			return huma.Error403Forbidden(e.Msg)
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return huma.Error404NotFound("not found")
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return huma.Error409Conflict("already exists")
		case "23503": // foreign_key_violation
			return huma.Error409Conflict("referenced resource conflict")
		case "23514", "22001", "22P02": // check, string too long, bad text repr
			return huma.Error422UnprocessableEntity("invalid value")
		}
	}
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		// Client went away; nothing useful to send and nothing to alert on.
		return huma.NewError(499, "client closed request")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		slog.WarnContext(ctx, "request deadline exceeded", "error", err)
		return huma.Error504GatewayTimeout("")
	}
	slog.ErrorContext(ctx, "unhandled error", "error", err)
	return huma.Error500InternalServerError("")
}

func authMiddleware(api huma.API, v auth.Verifier) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		op := ctx.Operation()
		if len(op.Security) == 0 {
			next(ctx)
			return
		}
		p, err := verifyHeader(ctx.Context(), v, ctx.Header("Authorization"))
		if err == nil {
			if role, ok := op.Metadata[metaRole].(string); ok && !p.HasRole(role) {
				err = auth.ErrNoRole(role)
			}
		}
		if err != nil {
			se := MapError(ctx.Context(), err)
			if se.GetStatus() == http.StatusUnauthorized {
				ctx.SetHeader("WWW-Authenticate", `Bearer`)
			}
			_ = huma.WriteErr(api, ctx, se.GetStatus(), se.Error())
			return
		}
		next(huma.WithContext(ctx, auth.WithPrincipal(ctx.Context(), p)))
	}
}

func verifyHeader(ctx context.Context, v auth.Verifier, header string) (auth.Principal, error) {
	raw, err := auth.BearerToken(header)
	if err != nil {
		return auth.Principal{}, err
	}
	return v.Verify(ctx, raw)
}
