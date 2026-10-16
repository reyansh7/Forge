package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestApplicationDomainCRUDAndUniqueness(t *testing.T) {
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	owner := operatorForTest(t, pg)
	proj, err := pg.CreateProject(ctx, owner.ID, ProjectInput{
		Name:          "dom-" + time.Now().UTC().Format("20060102T150405.000000000"),
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
		t.Fatalf("apps: %v %d", err, len(apps))
	}
	app := apps[0]

	if _, err := pg.CreateApplicationDomain(ctx, app.ID, "hello.localhost", EnvProduction); err == nil {
		t.Fatal(".localhost must be rejected")
	}

	d, err := pg.CreateApplicationDomain(ctx, app.ID, "App.Example.COM", EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	if d.Hostname != "app.example.com" || d.Status != DomainPending || d.Token == "" {
		t.Fatalf("%+v", d)
	}
	if _, err := pg.CreateApplicationDomain(ctx, app.ID, "app.example.com", EnvPreview); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate hostname = %v", err)
	}

	proj2, err := pg.CreateProject(ctx, owner.ID, ProjectInput{
		Name:          "dom2-" + time.Now().UTC().Format("20060102T150405.000000000"),
		RepositoryURL: SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.db.ExecContext(context.Background(), `DELETE FROM projects WHERE id = $1::uuid`, proj2.ID)
	})
	apps2, err := pg.ListApplicationsByProject(ctx, proj2.ID)
	if err != nil || len(apps2) == 0 {
		t.Fatalf("apps2: %v", err)
	}
	if _, err := pg.CreateApplicationDomain(ctx, apps2[0].ID, "app.example.com", EnvProduction); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-app steal = %v", err)
	}

	got, err := pg.ActivateApplicationDomain(ctx, app.ID, d.ID)
	if err != nil || got.Status != DomainActive {
		t.Fatalf("activate = %+v err=%v", got, err)
	}
	active, err := pg.ListActiveDomains(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range active {
		if row.ID == d.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("active missing %+v", active)
	}
	if err := pg.DeleteApplicationDomain(ctx, app.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	if err := pg.DeleteApplicationDomain(ctx, app.ID, d.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}
