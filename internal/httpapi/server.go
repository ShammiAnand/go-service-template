package httpapi

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"

	"github.com/ShammiAnand/go-service-template/internal/config"
)

// NewEngine returns a Gin engine with the full middleware chain. Order
// matters: recovery sits inside tracing and access logging so a panic is
// recorded on the span and logged as a 500.
func NewEngine(ctx context.Context, cfg config.Config, log *slog.Logger) (*gin.Engine, error) {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.HandleMethodNotAllowed = true
	// Only trust X-Forwarded-For from listed proxies; otherwise ClientIP is
	// the socket peer and cannot be spoofed to dodge the rate limiter.
	if err := e.SetTrustedProxies(cfg.HTTP.TrustedProxies); err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}
	e.Use(
		RequestID(),
		otelgin.Middleware(cfg.ServiceName),
		AccessLog(log),
		Recovery(log),
		SecurityHeaders(),
		CORS(cfg.HTTP.CORSOrigins),
		BodyLimit(cfg.HTTP.BodyLimitBytes),
		NewRateLimiter(ctx, log, cfg.HTTP.RateLimitRPS, cfg.HTTP.RateLimitBurst).Middleware(),
	)
	e.NoRoute(func(c *gin.Context) { writeProblem(c, http.StatusNotFound, "no such route") })
	e.NoMethod(func(c *gin.Context) { writeProblem(c, http.StatusMethodNotAllowed, "") })
	return e, nil
}

// NewServer wraps h with the configured timeouts.
func NewServer(cfg config.HTTP, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           h,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}
