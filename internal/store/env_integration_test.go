package store

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/secrets"
)

func TestEnvVarsEncryptedAtRest(t *testing.T) {
	// Seal must change the SQL cell. ListEnvVars must still return
	// plaintext so the worker can inject docker -e. Skip if Postgres
	// is down (same policy as other store integration tests).
	pg := postgresForTest(t)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	box, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}
	pg.SetCrypter(box)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	owner := operatorForTest(t, pg)
	proj, err := pg.CreateProject(ctx, owner.ID, ProjectInput{
		Name:          "env-seal-" + time.Now().UTC().Format("20060102T150405.000000000"),
		RepositoryURL: SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.db.ExecContext(context.Background(), `DELETE FROM projects WHERE id = $1::uuid`, proj.ID)
	})

	apps, err := pg.ListApplicationsByProject(ctx, proj.ID)
	if err != nil || len(apps) == 0 {
		t.Fatalf("default app: %v n=%d", err, len(apps))
	}
	plain := "never-store-me-in-cleartext"
	if err := pg.PutEnvVar(ctx, apps[0].ID, "SECRET_TOKEN", plain); err != nil {
		t.Fatal(err)
	}

	listed, err := pg.ListEnvVars(ctx, apps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Value != plain {
		t.Fatalf("list = %#v", listed)
	}

	var stored string
	if err := pg.db.QueryRowContext(ctx, `
		SELECT value FROM application_env_vars
		WHERE application_id = $1::uuid AND key = $2
	`, apps[0].ID, "SECRET_TOKEN").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == plain {
		t.Fatal("SQL cell is plaintext; Seal did not run")
	}
	if !secrets.Sealed(stored) {
		t.Fatalf("stored = %q (want enc:v1: prefix)", stored)
	}
}
