// Command worker is the Forge asynchronous job consumer.
//
// It is a separate process from cmd/api so HTTP handlers stay fast and
// so build/deploy never runs inside an API request.
//
// Phase 1 jobs: "example" (log and return) and "deploy" (fetch an
// application's repo → detect → docker build → docker run with env →
// health → Caddy). User application code is not executed on this host.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/reyansh7/Forge/internal/config"
	"github.com/reyansh7/Forge/internal/deploy"
	"github.com/reyansh7/Forge/internal/proxy"
	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/store"
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
	cfg, err := config.LoadWorker()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pg, err := store.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pg.Close()
	if err := pg.Migrate(ctx); err != nil {
		return err
	}

	q, err := queue.NewRedis(cfg.RedisURL, queue.DefaultKey)
	if err != nil {
		return err
	}
	if err := q.Ping(ctx); err != nil {
		return err
	}

	deployHandler := deploy.Handler{
		Log:          log,
		Store:        pg,
		Fetcher:      runtime.HostFetcher{},
		Builder:      runtime.HostDocker{},
		Runner:       runtime.HostDocker{},
		Router:       proxy.Caddy{AdminURL: cfg.CaddyAdminURL, UpstreamHost: cfg.CaddyUpstreamHost},
		WorkspaceDir: cfg.WorkspaceDir,
		ProxyBase:    cfg.ProxyPublicBase,
	}

	// Caddy's in-memory config dies on container restart. Re-apply LIVE
	// routes so a worker start restores the proxy without a new deploy.
	if live, err := pg.ListLiveDeployments(ctx); err != nil {
		log.Error("list live deployments failed", "err", err)
	} else if err := deployHandler.Router.Apply(ctx, live); err != nil {
		log.Error("caddy reconcile failed", "err", err)
	}

	log.Info("worker consuming", "queue_key", queue.DefaultKey)
	return worker.Run(ctx, q, worker.Dispatcher{
		Example: worker.ExampleHandler{Log: log},
		Deploy:  deployHandler,
	}, log)
}
