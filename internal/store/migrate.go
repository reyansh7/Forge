package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// migrationFS is compiled into the API binary (`go:embed`).
//
// What: SQL files under migrations/ become part of the executable.
// Why: `go run` / a copied binary still has the same schema as the repo.
//
//	Operators do not scp .sql files next to the binary in increment 0.2.
//
// Alternative: golang-migrate CLI + files on disk — extra tool for one table.
// Failure: if embed is forgotten, Migrate lists zero files and the API
//
//	serves /projects against a missing table (500s). The unit test that
//	the first filename is 0001_projects.sql guards that.
//
// The go:embed line must sit immediately above the var (compiler directive).
//
//go:embed migrations/*.sql
var migrationFS embed.FS

// Migrate brings the database schema up to the SQL shipped in this binary.
//
// Responsibility: apply each versioned .sql file once, in name order.
// Called by: cmd/api after NewPostgres, before ListenAndServe — so
//
//	POST /projects cannot race an empty database.
//
// Calls: PostgreSQL (schema_migrations + the file bodies).
//
// A "migration" is a repeatable schema change. Restarting the API must
// not re-run CREATE TABLE in a way that fails. We record each filename
// in schema_migrations and skip rows that already exist (idempotent).
//
// Each file runs in a transaction: if CREATE TABLE succeeds but the
// INSERT into schema_migrations fails, Rollback undoes both. Otherwise
// a crash between the two would leave "applied" SQL with no version row
// (or the reverse), and the next start would be confused.
//
// Invariant: version names are filenames (0001_projects.sql). Lexicographic
// sort is apply order — that is why we zero-pad 0001, not 1.
//
// Deferred: down-migrations / rollback SQL, locking for two APIs migrating
// at once (single local process in Phase 0).
func (p *Postgres) Migrate(ctx context.Context) error {
	if _, err := p.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)
	`); err != nil {
		return fmt.Errorf("schema_migrations: %w", err)
	}

	names, err := migrationFileNames()
	if err != nil {
		return err
	}

	for _, name := range names {
		var applied bool
		err := p.db.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
			name,
		).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied {
			continue
		}

		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}

		tx, err := p.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO schema_migrations (version) VALUES ($1)`,
			name,
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

func migrationFileNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}
