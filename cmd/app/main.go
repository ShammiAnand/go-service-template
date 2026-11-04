// Command app is the single binary for the service.
//
//	app serve                 run the HTTP API
//	app worker                run the background job worker
//	app migrate up|down|status
//	app migrate-new NAME      create a new SQL migration file
//	app openapi               print the OpenAPI 3.1 document (YAML)
//	app healthcheck [URL]     exit 0 if GET URL (default /livez) returns 200
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ShammiAnand/go-service-template/internal/app"
	"github.com/ShammiAnand/go-service-template/internal/config"
	"github.com/ShammiAnand/go-service-template/internal/db"
	"github.com/ShammiAnand/go-service-template/internal/httpapi"
	"github.com/ShammiAnand/go-service-template/internal/jobs"
	"github.com/ShammiAnand/go-service-template/internal/logging"
	"github.com/ShammiAnand/go-service-template/internal/platform/safe"
)

const usage = "usage: app serve | worker | migrate up|down|status | migrate-new NAME | openapi | healthcheck [URL]"

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1], os.Args[2:])
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd string, args []string) error {
	switch cmd {
	case "openapi":
		return printOpenAPI(ctx, os.Stdout)
	case "healthcheck":
		target := "http://127.0.0.1:8080/livez"
		if len(args) > 0 {
			target = args[0]
		}
		return healthcheck(ctx, target)
	case "migrate-new":
		if len(args) != 1 {
			return errors.New("usage: app migrate-new NAME")
		}
		return db.CreateMigration(args[0])
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := logging.New(os.Stdout, cfg.LogLevel, cfg.ServiceName, cfg.Version)
	slog.SetDefault(log)

	a, err := app.New(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := a.Close(closeCtx); err != nil {
			log.Error("close", "error", err)
		}
	}()

	switch cmd {
	case "serve":
		return serve(ctx, a)
	case "worker":
		return worker(ctx, a)
	case "migrate":
		if len(args) != 1 {
			return errors.New("usage: app migrate up|down|status")
		}
		return db.Migrate(ctx, a.Pool, args[0], log)
	default:
		return errors.New(usage)
	}
}

func serve(ctx context.Context, a *app.App) error {
	h, _, err := a.HTTPHandler(ctx)
	if err != nil {
		return err
	}
	srv := httpapi.NewServer(a.Cfg.HTTP, h, a.Log)

	errCh := make(chan error, 1)
	safe.Go(ctx, a.Log, "http-server", func(context.Context) error {
		a.Log.Info("http server listening", "addr", a.Cfg.HTTP.Addr, "env", a.Cfg.Env)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		return nil
	})

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	a.Log.Info("shutting down: draining in-flight requests", "timeout", a.Cfg.HTTP.ShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), a.Cfg.HTTP.ShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

func worker(ctx context.Context, a *app.App) error {
	if a.Redis == nil {
		return errors.New("worker needs JOBS_DRIVER=asynq (inline jobs run inside serve)")
	}
	a.Log.Info("worker started", "concurrency", a.Cfg.Jobs.Concurrency)
	return jobs.NewAsynqWorker(a.Redis, a.Jobs, a.Cfg.Jobs.Concurrency, a.Log).Run(ctx)
}

func printOpenAPI(ctx context.Context, w io.Writer) error {
	discard := slog.New(slog.DiscardHandler)
	a, err := app.New(ctx, app.SpecConfig(), discard)
	if err != nil {
		return err
	}
	_, api, err := a.HTTPHandler(ctx)
	if err != nil {
		return err
	}
	b, err := api.OpenAPI().YAML()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// healthcheck only probes loopback: it backs the container HEALTHCHECK and
// must not become a generic HTTP client.
func healthcheck(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("healthcheck: invalid URL %q", rawURL)
	}
	if ip := net.ParseIP(u.Hostname()); u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("healthcheck: host must be loopback, got %q", u.Hostname())
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil) //nolint:gosec // host restricted to loopback above
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // host restricted to loopback above
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck: status %d", resp.StatusCode)
	}
	return nil
}
