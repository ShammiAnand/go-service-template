package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ShammiAnand/go-service-template/internal/apperr"
	"github.com/ShammiAnand/go-service-template/internal/auth"
	"github.com/ShammiAnand/go-service-template/internal/config"
	"github.com/ShammiAnand/go-service-template/internal/httpapi"
	"github.com/ShammiAnand/go-service-template/internal/testutil"
)

type msgOutput struct {
	Body struct {
		Msg string `json:"msg"`
	}
}

func testConfig() config.Config {
	return config.Config{
		ServiceName: "test",
		HTTP: config.HTTP{
			BodyLimitBytes: 64, RateLimitRPS: 1000, RateLimitBurst: 1000,
			CORSOrigins: []string{"https://app.example.com"},
		},
	}
}

// newServer builds the real middleware chain plus a few probe operations.
func newServer(t *testing.T, cfg config.Config) (*httptest.Server, *testutil.Issuer) {
	t.Helper()
	iss := testutil.NewIssuer(t)
	log := slog.New(slog.DiscardHandler)
	engine, err := httpapi.NewEngine(t.Context(), cfg, log)
	require.NoError(t, err)
	api := httpapi.NewAPI(engine, iss.Verifier(t.Context()), false)

	httpapi.Register(api, huma.Operation{OperationID: "panic", Method: http.MethodGet, Path: "/panic"},
		func(context.Context, *struct{}) (*msgOutput, error) { panic("boom") })
	httpapi.Register(api, huma.Operation{OperationID: "fail", Method: http.MethodGet, Path: "/fail"},
		func(context.Context, *struct{}) (*msgOutput, error) {
			return nil, errors.New("db password=secret leaked?")
		})
	httpapi.Register(api, huma.Operation{OperationID: "me", Method: http.MethodGet, Path: "/me", Security: httpapi.BearerAuth},
		func(ctx context.Context, _ *struct{}) (*msgOutput, error) {
			out := &msgOutput{}
			out.Body.Msg = auth.MustFromContext(ctx).Subject
			return out, nil
		})
	httpapi.Register(api, httpapi.RequireRole(huma.Operation{OperationID: "admin", Method: http.MethodGet, Path: "/admin"}, "admin"),
		func(context.Context, *struct{}) (*msgOutput, error) { return &msgOutput{}, nil })
	httpapi.Register(api, huma.Operation{OperationID: "echo", Method: http.MethodPost, Path: "/echo"},
		func(_ context.Context, in *struct {
			Body struct {
				S string `json:"s" maxLength:"10"`
			}
		},
		) (*msgOutput, error) {
			out := &msgOutput{}
			out.Body.Msg = in.Body.S
			return out, nil
		})

	srv := httptest.NewServer(engine)
	t.Cleanup(srv.Close)
	return srv, iss
}

func do(t *testing.T, req *http.Request) (*http.Response, map[string]any) {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return resp, body
}

func get(t *testing.T, url, token string) (*http.Response, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return do(t, req)
}

func TestPanicIsRecovered(t *testing.T) {
	srv, _ := newServer(t, testConfig())
	resp, body := get(t, srv.URL+"/panic", "")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	assert.Equal(t, "application/problem+json", resp.Header.Get("Content-Type"))
	assert.InDelta(t, 500, body["status"], 0)

	resp, _ = get(t, srv.URL+"/livez-missing", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "server still serving after panic")
}

func TestServerErrorsDoNotLeakDetails(t *testing.T) {
	srv, _ := newServer(t, testConfig())
	resp, body := get(t, srv.URL+"/fail", "")
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	raw, _ := json.Marshal(body)
	assert.NotContains(t, string(raw), "secret")
}

func TestAuth(t *testing.T) {
	srv, iss := newServer(t, testConfig())

	resp, _ := get(t, srv.URL+"/me", "")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "Bearer", resp.Header.Get("WWW-Authenticate"))

	resp, _ = get(t, srv.URL+"/me", "garbage")
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp, body := get(t, srv.URL+"/me", iss.Token(t, testutil.Claims{Subject: "user-1"}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "user-1", body["msg"])

	resp, _ = get(t, srv.URL+"/admin", iss.Token(t, testutil.Claims{Subject: "u", Roles: []string{"user"}}))
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)

	resp, _ = get(t, srv.URL+"/admin", iss.Token(t, testutil.Claims{Subject: "u", Roles: []string{"admin"}}))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestValidationAndBodyLimit(t *testing.T) {
	srv, _ := newServer(t, testConfig())
	post := func(body string) *http.Response {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/echo", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		resp, _ := do(t, req)
		return resp
	}
	assert.Equal(t, http.StatusOK, post(`{"s":"ok"}`).StatusCode)
	assert.Equal(t, http.StatusUnprocessableEntity, post(`{"s":"waaaaaaaaaay too long"}`).StatusCode)
	assert.Equal(t, http.StatusRequestEntityTooLarge, post(`{"s":"`+strings.Repeat("x", 100)+`"}`).StatusCode)
}

func TestHeadersRequestIDAndCORS(t *testing.T) {
	srv, _ := newServer(t, testConfig())

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/fail", nil)
	req.Header.Set("X-Request-ID", "abc-123")
	req.Header.Set("Origin", "https://app.example.com")
	resp, _ := do(t, req)
	assert.Equal(t, "abc-123", resp.Header.Get("X-Request-ID"))
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
	assert.NotEmpty(t, resp.Header.Get("Content-Security-Policy"))
	assert.NotEmpty(t, resp.Header.Get("Strict-Transport-Security"))
	assert.Equal(t, "https://app.example.com", resp.Header.Get("Access-Control-Allow-Origin"))

	req, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/fail", nil)
	req.Header.Set("X-Request-ID", "bad id with spaces")
	req.Header.Set("Origin", "https://evil.example.com")
	resp, _ = do(t, req)
	assert.NotEqual(t, "bad id with spaces", resp.Header.Get("X-Request-ID"))
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestRateLimit(t *testing.T) {
	cfg := testConfig()
	cfg.HTTP.RateLimitRPS, cfg.HTTP.RateLimitBurst = 0.001, 2
	srv, _ := newServer(t, cfg)
	codes := make([]int, 3)
	for i := range codes {
		resp, _ := get(t, srv.URL+"/fail", "")
		codes[i] = resp.StatusCode
	}
	assert.Equal(t, []int{500, 500, http.StatusTooManyRequests}, codes)
}

func TestMapError(t *testing.T) {
	ctx := t.Context()
	cases := []struct {
		err  error
		want int
	}{
		{apperr.Invalid("x"), 422},
		{apperr.NotFound("x"), 404},
		{fmt.Errorf("wrapped: %w", apperr.Conflict("x")), 409},
		{apperr.Unauthorized("x"), 401},
		{apperr.Forbidden("x"), 403},
		{pgx.ErrNoRows, 404},
		{fmt.Errorf("q: %w", pgx.ErrNoRows), 404},
		{&pgconn.PgError{Code: "23505"}, 409},
		{&pgconn.PgError{Code: "23514"}, 422},
		{context.DeadlineExceeded, 504},
		{huma.Error418Teapot("x"), 418},
		{errors.New("anything else"), 500},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, httpapi.MapError(ctx, c.err).GetStatus(), c.err.Error())
	}
}
