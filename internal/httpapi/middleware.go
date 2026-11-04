package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"golang.org/x/time/rate"

	"github.com/ShammiAnand/go-service-template/internal/logging"
	"github.com/ShammiAnand/go-service-template/internal/platform/safe"
)

const headerRequestID = "X-Request-ID"

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// RequestID accepts a safe incoming X-Request-ID or generates one, echoes
// it in the response and stores it in the request context for logs.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(headerRequestID)
		if !validRequestID.MatchString(id) {
			id = uuid.NewString()
		}
		c.Header(headerRequestID, id)
		c.Request = c.Request.WithContext(logging.WithRequestID(c.Request.Context(), id))
		c.Next()
	}
}

// writeProblem writes an RFC 9457 problem body.
func writeProblem(c *gin.Context, status int, detail string) {
	c.Header("Content-Type", "application/problem+json")
	c.AbortWithStatusJSON(status, gin.H{"title": http.StatusText(status), "status": status, "detail": detail})
}

// Recovery turns a panic into a 500 problem response, logs the stack and
// marks the span as failed. The server keeps serving other requests.
func Recovery(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler { //nolint:errorlint // sentinel compared by identity, as net/http does
				panic(v) // net/http uses this to abort a response on purpose
			}
			ctx := c.Request.Context()
			span := trace.SpanFromContext(ctx)
			span.RecordError(&safe.PanicError{Value: v}, trace.WithStackTrace(true))
			span.SetStatus(codes.Error, "panic")
			log.ErrorContext(ctx, "panic recovered", "panic", v, "stack", string(debug.Stack()))
			if !c.Writer.Written() {
				writeProblem(c, http.StatusInternalServerError, "")
			}
		}()
		c.Next()
	}
}

// AccessLog writes one JSON line per request.
func AccessLog(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		lvl := slog.LevelInfo
		if c.Writer.Status() >= http.StatusInternalServerError {
			lvl = slog.LevelError
		}
		log.Log(c.Request.Context(), lvl, "http request",
			"method", c.Request.Method,
			"route", route,
			"status", c.Writer.Status(),
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", c.Writer.Size(),
			"client_ip", c.ClientIP(),
		)
	}
}

// SecurityHeaders sets defensive headers. The docs page loads its UI from
// unpkg, so it gets a wider CSP than JSON endpoints.
func SecurityHeaders() gin.HandlerFunc {
	const apiCSP = "default-src 'none'; frame-ancestors 'none'"
	const docsCSP = "default-src 'self'; script-src 'self' https://unpkg.com; style-src 'self' 'unsafe-inline' https://unpkg.com; img-src 'self' data: https:; font-src 'self' data: https://unpkg.com; connect-src 'self'; frame-ancestors 'none'"
	return func(c *gin.Context) {
		h := c.Writer.Header()
		csp := apiCSP
		if c.Request.URL.Path == "/docs" {
			csp = docsCSP
		}
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Next()
	}
}

// CORS allows only listed origins. An empty list disables CORS.
func CORS(origins []string) gin.HandlerFunc {
	allowAll := slices.Contains(origins, "*")
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" || (!allowAll && !slices.Contains(origins, origin)) {
			c.Next()
			return
		}
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Add("Vary", "Origin")
		if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID")
			h.Set("Access-Control-Max-Age", "600")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		h.Set("Access-Control-Expose-Headers", headerRequestID)
		c.Next()
	}
}

// BodyLimit caps request body size.
func BodyLimit(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > limit {
			writeProblem(c, http.StatusRequestEntityTooLarge, "body exceeds "+strconv.FormatInt(limit, 10)+" bytes")
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		c.Next()
	}
}

// RateLimiter is a per client IP token bucket. State is per process; use a
// shared store (Redis) if you run many replicas and need a global limit.
type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*client
	rps     rate.Limit
	burst   int
}

type client struct {
	lim  *rate.Limiter
	seen time.Time
}

// NewRateLimiter starts a janitor goroutine bound to ctx that drops idle
// clients.
func NewRateLimiter(ctx context.Context, log *slog.Logger, rps float64, burst int) *RateLimiter {
	rl := &RateLimiter{clients: map[string]*client{}, rps: rate.Limit(rps), burst: burst}
	safe.Go(ctx, log, "rate-limit-janitor", func(ctx context.Context) error {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case now := <-t.C:
				rl.evict(now.Add(-3 * time.Minute))
			}
		}
	})
	return rl
}

func (rl *RateLimiter) evict(before time.Time) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	for ip, cl := range rl.clients {
		if cl.seen.Before(before) {
			delete(rl.clients, ip)
		}
	}
}

func (rl *RateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cl, ok := rl.clients[ip]
	if !ok {
		cl = &client{lim: rate.NewLimiter(rl.rps, rl.burst)}
		rl.clients[ip] = cl
	}
	cl.seen = time.Now()
	return cl.lim.Allow()
}

// Middleware rejects over-limit clients with 429.
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.allow(c.ClientIP()) {
			c.Header("Retry-After", "1")
			writeProblem(c, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		c.Next()
	}
}
