// Command worker is the Forge asynchronous job consumer.
//
// It is a separate process from cmd/api so HTTP handlers stay fast and
// so a future build/deploy job cannot run inside the API request.
// Increment 0.3 only runs the internal "example" job. It must never
// clone git, exec shells, or run user application code.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/reyansh7/Forge/internal/config"
	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/worker"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("worker exited", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// Worker needs Redis only. Postgres is the durable store; this process
	// does not persist jobs there (LIST is transient by design).
	cfg, err := config.LoadWorker()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	q, err := queue.NewRedis(cfg.RedisURL, queue.DefaultKey)
	if err != nil {
		return err
	}
	if err := q.Ping(ctx); err != nil {
		return err
	}

	log.Info("worker consuming", "queue_key", queue.DefaultKey)
	return worker.Run(ctx, q, worker.ExampleHandler{Log: log}, log)
}
