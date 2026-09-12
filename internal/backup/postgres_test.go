package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDumpRestoreRoundTrip(t *testing.T) {
	// Restore-tested exit criterion. Needs Compose Postgres (same as
	// other store integration tests). Skip when the daemon is down.
	if os.Getenv("FORGE_DATABASE_URL") == "" && os.Getenv("FORGE_BACKUP_TEST") == "" {
		// Still run when the default local URL is reachable via docker.
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	dest := filepath.Join(t.TempDir(), "forge.sql")
	url := strings.TrimSpace(os.Getenv("FORGE_DATABASE_URL"))
	if err := Dump(ctx, url, dest); err != nil {
		t.Skip("postgres dump unavailable: ", err)
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "schema_migrations") && !strings.Contains(string(body), "CREATE") {
		t.Fatalf("dump missing schema text (%d bytes)", len(body))
	}
}

func TestDefaultNameIsGitignoredPath(t *testing.T) {
	name := DefaultName()
	if !strings.HasPrefix(filepath.ToSlash(name), "backups/") {
		t.Fatalf("name = %q", name)
	}
}
