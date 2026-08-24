package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	// Blank import: pgx registers itself as the database/sql driver named
	// "pgx". sql.Open("pgx", url) looks up that name. Without this import
	// the pool cannot open even though pgx is in go.mod.
	//
	// We stay on database/sql (not pgx's native API) so the rest of Forge
	// does not depend on driver-specific types. Swapping drivers later is
	// easier. The cost is a slightly less pgx-idiomatic API.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Postgres is the control-plane PostgreSQL client for increment 0.2.
//
// Responsibility: connection pool, health Ping, schema migrations, and
// Project rows. It is Forge's durable store — not a database that user
// apps will attach later.
//
// Called by: cmd/api (startup) and httpapi handlers (via ProjectStore).
// It calls: PostgreSQL on FORGE_DATABASE_URL (loopback Compose in Phase 0).
//
// Boundary: API → PostgreSQL. Handlers never import database/sql. That
// keeps SQL, credentials, and driver errors on this side of the wall.
//
// sql.DB is a *pool*, not one socket. Open() does not dial; Ping does.
// We ping in NewPostgres so a bad URL fails at process start, not on the
// first HTTP request.
//
// Security: the URL often contains a password — never log it. This type
// must not exec user git/build commands.
//
// Deferred: connection pooling for many API replicas, read replicas,
// per-user databases, encryption at rest.
type Postgres struct {
	db *sql.DB
}

// NewPostgres opens a pool and proves the server accepts connections.
// ctx bounds the startup ping so a down database cannot hang `go run` forever.
func NewPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	// Cap the pool. This binary is one process on a laptop. Unbounded
	// MaxOpenConns plus a /health storm would exhaust Postgres max_connections.
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel() // release the timer; always pair WithTimeout with cancel.
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close() // do not leak sockets if startup fails after Open
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &Postgres{db: db}, nil
}

// Ping is the health-check probe (httpapi.StatusChecker). It runs
// SELECT 1-style work inside the driver — not user application code.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.db.PingContext(ctx)
}

// Close returns pool connections to the OS. cmd/api defers this on shutdown.
func (p *Postgres) Close() error {
	return p.db.Close()
}
