// Package safe runs code so that a panic becomes a logged error instead
// of a crashed process. Start every background goroutine with Go.
package safe

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
)

// PanicError carries a recovered panic value and its stack.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// Call runs fn and converts a panic into a *PanicError.
func Call(fn func() error) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = &PanicError{Value: v, Stack: debug.Stack()}
		}
	}()
	return fn()
}

// Go runs fn in a goroutine. A panic or error is logged with name; the
// process keeps running.
func Go(ctx context.Context, log *slog.Logger, name string, fn func(ctx context.Context) error) {
	go func() {
		err := Call(func() error { return fn(ctx) })
		if err == nil || ctx.Err() != nil {
			return
		}
		attrs := []any{"goroutine", name, "error", err}
		if pe := (*PanicError)(nil); errors.As(err, &pe) {
			attrs = append(attrs, "stack", string(pe.Stack))
		}
		log.ErrorContext(ctx, "background goroutine failed", attrs...)
	}()
}
