// Command api is the Forge control-plane HTTP process.
//
// A "control plane" is the software that *manages* deployments. This
// binary is that process: it loads config, checks that Postgres and Redis
// are reachable, then serves HTTP. It must never clone user git repos or
// exec user build commands on the host — that is untrusted code.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/reyansh7/Forge/internal/config"
	"github.com/reyansh7/Forge/internal/httpapi"
	"github.com/reyansh7/Forge/internal/store"
)

func main() {
	// slog is Go's structured logger. JSON on stdout is easy to grep and
	// later ship to a log aggregator. We do not log connection URLs or
	// passwords — those live only in env / .env.
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("api exited", "err", err)
		// Non-zero exit tells Compose/systemd/the shell that the process
		// failed. Returning from main with no os.Exit would look like success.
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	// Load() reads FORGE_* from the environment (and a local .env if present).
	// Fail here, before opening sockets, if required store URLs are missing.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// NotifyContext cancels ctx when the OS sends Ctrl+C (SIGINT) or SIGTERM
	// (what Docker/Kubernetes send on stop). That is how we shut down cleanly
	// instead of dying mid-request.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop() // stop() undoes the signal registration; always pair with NotifyContext.

	// Open the control-plane database first. If it is down, there is no
	// point listening: /health would only report failure, and /projects
	// cannot persist. NewPostgres pings so a bad URL fails here.
	pg, err := store.NewPostgres(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pg.Close() // Close the pool when run() returns (shutdown or startup failure after open).

	// Apply embedded SQL before serving HTTP so /projects cannot race an
	// empty database. Migrate is idempotent (schema_migrations): restart
	// does not recreate tables or fail on "already exists".
	if err := pg.Migrate(ctx); err != nil {
		return err
	}

	rdb, err := store.NewRedisPinger(cfg.RedisURL)
	if err != nil {
		return err
	}

	// One Postgres value fills two ports: Ping for /health, ProjectStore
	// for /projects. Redis is still liveness-only in 0.2 (no queue).
	api := &httpapi.Server{
		Log:      log,
		Postgres: pg,
		Redis:    rdb,
		Projects: pg,
	}

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: api.Handler(),
		// ReadHeaderTimeout is Slowloris protection: a client that dribbles
		// headers forever cannot hold a goroutine open indefinitely.
		ReadHeaderTimeout: 5 * time.Second,
	}

	// ListenAndServe blocks until the server stops. We run it in a goroutine
	// so this function can also wait on the shutdown signal. The buffer of 1
	// means a send to errCh never blocks if we already left the select.
	errCh := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", cfg.Addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		// Signal received. Shutdown stops accepting new connections and waits
		// for in-flight handlers, bounded by this 5s timeout so we cannot hang
		// forever on a stuck client.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		// ListenAndServe returns ErrServerClosed after a successful Shutdown.
		// Treat that as a clean exit, not a crash.
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
