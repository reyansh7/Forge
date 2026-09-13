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
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/reyansh7/Forge/internal/config"
	"github.com/reyansh7/Forge/internal/deploy"
	"github.com/reyansh7/Forge/internal/proxy"
	"github.com/reyansh7/Forge/internal/queue"
	"github.com/reyansh7/Forge/internal/runtime"
	"github.com/reyansh7/Forge/internal/secrets"
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

	// Same data key as cmd/api. Start both from the repo root so the
	// default .forge/data.key file is shared. A mismatch injects
	// ciphertext into the container environment.
	box, err := secrets.LoadOrCreate(cfg.DataKeyFile, cfg.DataKey)
	if err != nil {
		return err
	}
	pg.SetCrypter(box)

	nodeName := cfg.NodeName
	if nodeName == "" {
		nodeName = store.DefaultNodeName
	}
	// local is created here too so a worker started before the API
	// can still claim. Named nodes must already exist (POST /nodes).
	if nodeName == store.DefaultNodeName {
		if _, err := pg.EnsureLocalNode(ctx); err != nil {
			return err
		}
	}
	node, err := pg.ClaimNode(ctx, nodeName, cfg.NodeToken, cfg.NodeAdvertiseHost)
	if err != nil {
		// Name the node: a leftover FORGE_NODE_NAME in the shell is a
		// common "I ran go run ./cmd/worker" failure. Do not log the token.
		return fmt.Errorf("claim node %q: %w", nodeName, err)
	}
	key, err := queue.NodeKey(node.ID)
	if err != nil {
		return err
	}
	q, err := queue.NewRedis(cfg.RedisURL, key)
	if err != nil {
		return err
	}
	if err := q.Ping(ctx); err != nil {
		return err
	}

	go heartbeatNode(ctx, log, pg, node.ID, cfg.NodeAdvertiseHost)

	deployHandler := deploy.Handler{
		Log:               log,
		Store:             pg,
		Fetcher:           runtime.HostFetcher{},
		Builder:           runtime.HostDocker{},
		Runner:            runtime.HostDocker{},
		Router:            proxy.Caddy{AdminURL: cfg.CaddyAdminURL, UpstreamHost: cfg.CaddyUpstreamHost},
		WorkspaceDir:      cfg.WorkspaceDir,
		WorkspaceMaxBytes: cfg.WorkspaceMaxBytes,
		ProxyBase:         cfg.ProxyPublicBase,
	}

	// A killed worker leaves rows in BUILDING. Those block Deploy until
	// someone Stops. Close them here so a restart unsticks the app.
	if stale, err := pg.ListInterruptedDeployments(ctx, node.ID); err != nil {
		log.Error("list in-progress deployments failed", "err", err)
	} else {
		docker := runtime.HostDocker{}
		for _, d := range stale {
			if d.ID != "" {
				_ = docker.Stop(ctx, runtime.ResolveContainerName(d.ID, d.ContainerName))
			}
			stage := string(d.Status)
			d.FailedStage = stage
			d.Status = store.StatusFailed
			d.ErrorMessage = store.SanitizeErrorMessage("worker restarted before this deploy finished")
			if err := pg.UpdateDeployment(ctx, d); err != nil {
				log.Error("abandon stale deploy failed", "id", d.ID, "err", err)
			} else {
				log.Info("abandoned stale deploy", "id", d.ID, "was", stage)
			}
		}
	}

	// Caddy's in-memory config dies on container restart. Re-apply LIVE
	// routes so a worker start restores the proxy without a new deploy.
	if live, err := pg.ListLiveDeployments(ctx); err != nil {
		log.Error("list live deployments failed", "err", err)
	} else if err := deployHandler.Router.Apply(ctx, live); err != nil {
		log.Error("caddy reconcile failed", "err", err)
	}

	log.Info("worker consuming", "node", node.Name, "node_id", node.ID, "queue_key", key)
	return worker.Run(ctx, q, worker.Dispatcher{
		Example: worker.ExampleHandler{Log: log},
		Deploy:  deployHandler,
	}, log)
}

// heartbeatNode keeps last_seen_at fresh so the scheduler does not
// mark this process dead. Interval is one-third of NodeStaleAfter so
// a single missed tick is not fatal. Never logs the join token.
func heartbeatNode(ctx context.Context, log *slog.Logger, pg *store.Postgres, id, advertiseHost string) {
	t := time.NewTicker(store.NodeHeartbeatEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			hbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			_, err := pg.HeartbeatNode(hbCtx, id, advertiseHost)
			cancel()
			if err != nil && ctx.Err() == nil {
				log.Error("node heartbeat failed", "err", err, "node_id", id)
			}
		}
	}
}
