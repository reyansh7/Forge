package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestApplicationAndEnvAgainstPostgres(t *testing.T) {
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	name := "p11-" + time.Now().UTC().Format("20060102T150405.000000000")
	proj, err := pg.CreateProject(ctx, ProjectInput{
		Name:          name,
		RepositoryURL: SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.db.ExecContext(context.Background(), `DELETE FROM projects WHERE id = $1::uuid`, proj.ID)
	})

	apps, err := pg.ListApplicationsByProject(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 || apps[0].Name != DefaultApplicationName {
		t.Fatalf("default app = %#v", apps)
	}

	second, err := pg.CreateApplication(ctx, proj.ID, ApplicationInput{
		Name:          "api",
		RepositoryURL: SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.CreateApplication(ctx, proj.ID, ApplicationInput{
		Name:          "api",
		RepositoryURL: SampleHelloURL,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name: %v", err)
	}

	if err := pg.PutEnvVar(ctx, second.ID, "GREETING", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := pg.PutEnvVar(ctx, second.ID, "PORT", "9"); err == nil {
		t.Fatal("PORT must be rejected")
	}
	vars, err := pg.ListEnvVars(ctx, second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(vars) != 1 || vars[0].Key != "GREETING" || vars[0].Value != "hello" {
		t.Fatalf("env = %#v", vars)
	}
	if err := pg.DeleteEnvVar(ctx, second.ID, "GREETING"); err != nil {
		t.Fatal(err)
	}
	if err := pg.DeleteApplication(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
}
