package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Check reports whether one dependency is usable.
type Check func(ctx context.Context) error

type livezOutput struct {
	Body struct {
		Status string `json:"status" example:"ok"`
	}
}

type readyzOutput struct {
	Status int
	Body   struct {
		Status string            `json:"status" enum:"ok,unavailable"`
		Checks map[string]string `json:"checks"`
	}
}

// RegisterHealth adds /livez (process is up) and /readyz (dependencies
// reachable). Load balancers should route on /readyz.
func RegisterHealth(api huma.API, checks map[string]Check) {
	Register(api, huma.Operation{
		OperationID: "livez", Method: http.MethodGet, Path: "/livez",
		Summary: "Liveness probe", Tags: []string{"health"},
	}, func(context.Context, *struct{}) (*livezOutput, error) {
		out := &livezOutput{}
		out.Body.Status = "ok"
		return out, nil
	})

	Register(api, huma.Operation{
		OperationID: "readyz", Method: http.MethodGet, Path: "/readyz",
		Summary: "Readiness probe", Tags: []string{"health"},
		Responses: map[string]*huma.Response{"503": {Description: "A dependency is unavailable"}},
	}, func(ctx context.Context, _ *struct{}) (*readyzOutput, error) {
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		out := &readyzOutput{Status: http.StatusOK}
		out.Body.Status = "ok"
		out.Body.Checks = make(map[string]string, len(checks))
		for name, check := range checks {
			if err := check(ctx); err != nil {
				out.Status = http.StatusServiceUnavailable
				out.Body.Status = "unavailable"
				out.Body.Checks[name] = "unavailable"
				continue
			}
			out.Body.Checks[name] = "ok"
		}
		return out, nil
	})
}
