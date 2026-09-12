package store

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// postgresForTest talks to Docker Postgres when it is up.
// Skip (do not fail) if Compose is down so `go test ./...` still works offline.
func postgresForTest(t *testing.T) *Postgres {
	t.Helper()
	if os.Getenv("FORGE_SKIP_POSTGRES") != "" {
		t.Skip("FORGE_SKIP_POSTGRES is set")
	}

	databaseURL := strings.TrimSpace(os.Getenv("FORGE_DATABASE_URL"))
	if databaseURL == "" {
		// `go test` cwd is this package (internal/store), not the repo root.
		databaseURL = databaseURLFromDotEnv(t, "../../.env")
	}
	if databaseURL == "" {
		t.Skip("FORGE_DATABASE_URL is not set and ../../.env was not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pg, err := NewPostgres(ctx, databaseURL)
	if err != nil {
		t.Skipf("postgres not reachable: %v", err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	if err := pg.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return pg
}

// databaseURLFromDotEnv reads only FORGE_DATABASE_URL. Do not log the file
// (it contains a password even in local defaults).
func databaseURLFromDotEnv(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "FORGE_DATABASE_URL" {
			continue
		}
		return strings.Trim(strings.TrimSpace(val), `"'`)
	}
	return ""
}

// operatorForTest inserts a throwaway user so CreateProject can set owner_id.
// Username is random so parallel integration tests do not collide on the
// unique (lower(username)) index.
func operatorForTest(t *testing.T, pg *Postgres) User {
	t.Helper()
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatal(err)
	}
	hash, err := HashPassword("test-pass-ok")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	u, err := pg.CreateUser(ctx, "op"+hex.EncodeToString(buf[:]), hash)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.db.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, u.ID)
	})
	return u
}

func TestProjectCreateGetListAgainstPostgres(t *testing.T) {
	// End-to-end against the real table: INSERT, SELECT by id, LIST, 404.
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	owner := operatorForTest(t, pg)
	name := "p02-" + time.Now().UTC().Format("20060102T150405.000000000")
	created, err := pg.CreateProject(ctx, owner.ID, ProjectInput{
		Name:          name,
		RepositoryURL: "https://github.com/example/forge-phase-0-2.git",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.OwnerID != owner.ID {
		t.Fatalf("OwnerID = %q, want %q", created.OwnerID, owner.ID)
	}
	t.Cleanup(func() {
		_, _ = pg.db.ExecContext(context.Background(), `DELETE FROM projects WHERE id = $1::uuid`, created.ID)
	})

	got, err := pg.GetProject(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != name || got.RepositoryURL != created.RepositoryURL {
		t.Fatalf("got %+v", got)
	}

	list, err := pg.ListProjects(ctx, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range list {
		if p.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("created project missing from list")
	}

	if _, err := pg.GetProject(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: %v", err)
	}
}
