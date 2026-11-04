//go:build integration

// Integration tests run the real HTTP stack against Postgres and Redis in
// containers (testcontainers). Run with: make test-int (needs Docker).
package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/ShammiAnand/go-service-template/internal/app"
	"github.com/ShammiAnand/go-service-template/internal/config"
	"github.com/ShammiAnand/go-service-template/internal/db"
	"github.com/ShammiAnand/go-service-template/internal/email"
	"github.com/ShammiAnand/go-service-template/internal/jobs"
	"github.com/ShammiAnand/go-service-template/internal/notes"
	"github.com/ShammiAnand/go-service-template/internal/testutil"
)

type env struct {
	srv *httptest.Server
	iss *testutil.Issuer
	app *app.App
}

func setup(t *testing.T) env {
	t.Helper()
	ctx := t.Context()

	pg, err := tcpostgres.Run(ctx, "postgres:18.6-alpine",
		tcpostgres.WithDatabase("app"), tcpostgres.WithUsername("app"), tcpostgres.WithPassword("app"),
		tcpostgres.BasicWaitStrategies())
	testcontainers.CleanupContainer(t, pg)
	require.NoError(t, err)
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	rc, err := tcredis.Run(ctx, "redis:8.10.3-alpine")
	testcontainers.CleanupContainer(t, rc)
	require.NoError(t, err)
	redisURL, err := rc.ConnectionString(ctx)
	require.NoError(t, err)

	iss := testutil.NewIssuer(t)
	cfg := config.Config{
		Env: config.EnvTest, ServiceName: "it", Version: "test", LogLevel: "error",
		HTTP:          config.HTTP{BodyLimitBytes: 1 << 20, RateLimitRPS: 1000, RateLimitBurst: 1000},
		Database:      config.Database{URL: dsn, MaxConns: 5},
		Redis:         config.Redis{URL: redisURL},
		Auth:          config.Auth{IssuerURL: iss.URL, JWKSURL: iss.JWKSURL(), Audience: testutil.Audience},
		Email:         config.Email{Driver: "log", From: "noreply@example.com"},
		Jobs:          config.Jobs{Driver: "asynq", Concurrency: 2, MaxRetry: 1},
		Observability: config.Observability{Driver: "noop"},
	}
	require.NoError(t, cfg.Validate())

	log := slog.New(slog.DiscardHandler)
	a, err := app.New(ctx, cfg, log)
	require.NoError(t, err)
	t.Cleanup(func() { _ = a.Close(context.Background()) })
	require.NoError(t, db.Migrate(ctx, a.Pool, "up", log))

	h, _, err := a.HTTPHandler(ctx)
	require.NoError(t, err)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return env{srv: srv, iss: iss, app: a}
}

func call(t *testing.T, method, url, token string, body any) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, url, r)
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

type capture struct {
	mu   sync.Mutex
	sent []email.Message
}

func (c *capture) Send(_ context.Context, m email.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, m)
	return nil
}

func (c *capture) count() int { c.mu.Lock(); defer c.mu.Unlock(); return len(c.sent) }

func TestNotesEndToEnd(t *testing.T) {
	e := setup(t)
	base := e.srv.URL + "/v1/notes"
	alice := e.iss.Token(t, testutil.Claims{Subject: "alice", Email: "alice@example.com"})
	bob := e.iss.Token(t, testutil.Claims{Subject: "bob"})

	// Worker with a capturing sender, sharing the app's Redis.
	sender := &capture{}
	reg := jobs.NewRegistry(slog.New(slog.DiscardHandler))
	notes.RegisterJobs(reg, sender)
	wctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = jobs.NewAsynqWorker(e.app.Redis, reg, 2, slog.New(slog.DiscardHandler)).Run(wctx)
	}()
	// The worker must stop before the app closes Redis (cleanups run LIFO).
	t.Cleanup(func() { stop(); <-done })

	status, _ := call(t, http.MethodGet, e.srv.URL+"/readyz", "", nil)
	assert.Equal(t, http.StatusOK, status)

	status, _ = call(t, http.MethodPost, base, "", map[string]string{"title": "x"})
	assert.Equal(t, http.StatusUnauthorized, status)

	status, _ = call(t, http.MethodPost, base, alice, map[string]string{"title": ""})
	assert.Equal(t, http.StatusUnprocessableEntity, status)

	status, created := call(t, http.MethodPost, base, alice, map[string]string{"title": "first", "body": "hello"})
	require.Equal(t, http.StatusCreated, status)
	id, _ := created["id"].(string)
	require.NotEmpty(t, id)

	status, got := call(t, http.MethodGet, base+"/"+id, alice, nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "hello", got["body"])

	status, _ = call(t, http.MethodGet, base+"/"+id, bob, nil)
	assert.Equal(t, http.StatusNotFound, status, "bob cannot read alice's note")

	status, _ = call(t, http.MethodGet, base+"/not-a-uuid", alice, nil)
	assert.Equal(t, http.StatusUnprocessableEntity, status)

	status, list := call(t, http.MethodGet, base, bob, nil)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, []any{}, list["items"])

	status, updated := call(t, http.MethodPut, base+"/"+id, alice, map[string]string{"title": "renamed", "body": ""})
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, "renamed", updated["title"])

	status, _ = call(t, http.MethodDelete, base+"/"+id, bob, nil)
	assert.Equal(t, http.StatusNotFound, status)
	status, _ = call(t, http.MethodDelete, base+"/"+id, alice, nil)
	assert.Equal(t, http.StatusNoContent, status)
	status, _ = call(t, http.MethodGet, base+"/"+id, alice, nil)
	assert.Equal(t, http.StatusNotFound, status)

	require.Eventually(t, func() bool { return sender.count() == 1 }, 15*time.Second, 100*time.Millisecond,
		"created-note email delivered through Redis worker")
}

func TestReadyzReportsDependencyOutage(t *testing.T) {
	e := setup(t)
	require.NoError(t, e.app.Redis.Close()) // simulate Redis loss
	status, body := call(t, http.MethodGet, e.srv.URL+"/readyz", "", nil)
	assert.Equal(t, http.StatusServiceUnavailable, status)
	assert.Equal(t, "unavailable", body["checks"].(map[string]any)["redis"])

	status, _ = call(t, http.MethodGet, e.srv.URL+"/livez", "", nil)
	assert.Equal(t, http.StatusOK, status, "process stays live")
}
