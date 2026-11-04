package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// AsynqQueue enqueues tasks in Redis.
type AsynqQueue struct {
	client   *asynq.Client
	maxRetry int
}

// NewAsynqQueue wraps a Redis client. It does not connect eagerly.
func NewAsynqQueue(rdb redis.UniversalClient, maxRetry int) *AsynqQueue {
	return &AsynqQueue{client: asynq.NewClientFromRedisClient(rdb), maxRetry: maxRetry}
}

// Enqueue stores t for a worker.
func (q *AsynqQueue) Enqueue(ctx context.Context, t Task) error {
	task := asynq.NewTaskWithHeaders(t.Type, t.Payload, t.Headers)
	if _, err := q.client.EnqueueContext(ctx, task, asynq.MaxRetry(q.maxRetry)); err != nil {
		return fmt.Errorf("enqueue %s: %w", t.Type, err)
	}
	return nil
}

// AsynqWorker pulls tasks from Redis and dispatches them via a Registry.
type AsynqWorker struct {
	srv *asynq.Server
	reg *Registry
}

// NewAsynqWorker builds a worker with the given concurrency.
func NewAsynqWorker(rdb redis.UniversalClient, reg *Registry, concurrency int, log *slog.Logger) *AsynqWorker {
	srv := asynq.NewServerFromRedisClient(rdb, asynq.Config{
		Concurrency: concurrency,
		Logger:      asynqLogger{log},
		LogLevel:    asynq.InfoLevel,
	})
	return &AsynqWorker{srv: srv, reg: reg}
}

// Run processes tasks until ctx is done, then waits for in-flight tasks.
// Close the Redis client only after Run returns: asynq's internal
// goroutines crash if the client closes under them.
func (w *AsynqWorker) Run(ctx context.Context) error {
	h := asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
		err := w.reg.Dispatch(ctx, Task{Type: t.Type(), Payload: t.Payload(), Headers: t.Headers()})
		if err != nil && IsPermanent(err) {
			return fmt.Errorf("%w: %w", err, asynq.SkipRetry)
		}
		return err
	})
	if err := w.srv.Start(h); err != nil {
		return fmt.Errorf("start worker: %w", err)
	}
	<-ctx.Done()
	w.srv.Shutdown()
	return nil
}

// asynqLogger adapts slog to asynq's logger interface.
type asynqLogger struct{ l *slog.Logger }

func (a asynqLogger) Debug(args ...any) { a.l.Debug(fmt.Sprint(args...), "component", "asynq") }
func (a asynqLogger) Info(args ...any)  { a.l.Info(fmt.Sprint(args...), "component", "asynq") }
func (a asynqLogger) Warn(args ...any)  { a.l.Warn(fmt.Sprint(args...), "component", "asynq") }
func (a asynqLogger) Error(args ...any) { a.l.Error(fmt.Sprint(args...), "component", "asynq") }
func (a asynqLogger) Fatal(args ...any) {
	a.l.Error(fmt.Sprint(args...), "component", "asynq", "fatal", true)
}
