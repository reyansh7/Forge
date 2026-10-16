package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDeploymentCreateGetListAgainstPostgres(t *testing.T) {
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	owner := operatorForTest(t, pg)
	name := "p04-" + time.Now().UTC().Format("20060102T150405.000000000")
	proj, err := pg.CreateProject(ctx, owner.ID, ProjectInput{
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
	if len(apps) != 1 {
		t.Fatalf("default apps = %d", len(apps))
	}
	created, err := pg.CreateDeployment(ctx, apps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != StatusQueued {
		t.Fatalf("status = %q", created.Status)
	}

	got, err := pg.GetDeployment(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != proj.ID || got.ApplicationID != apps[0].ID {
		t.Fatalf("ids project=%q app=%q", got.ProjectID, got.ApplicationID)
	}

	created.Status = StatusLive
	created.HostPort = 49152
	created.PublicURL = "http://127.0.0.1:9080/d/" + created.ID + "/"
	if err := pg.UpdateDeployment(ctx, created); err != nil {
		t.Fatal(err)
	}

	live, err := pg.ListLiveDeployments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range live {
		if d.ID == created.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("live list missing deployment")
	}

	if _, err := pg.GetDeployment(ctx, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing id: %v", err)
	}
}

func TestPreviewAndProductionCanBothBeLiveAgainstPostgres(t *testing.T) {
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	owner := operatorForTest(t, pg)
	name := "p73-" + time.Now().UTC().Format("20060102T150405.000000000")
	proj, err := pg.CreateProject(ctx, owner.ID, ProjectInput{
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
	prod, err := pg.CreateDeploymentIn(ctx, apps[0].ID, EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	prod.Status = StatusLive
	prod.HostPort = 49152
	if err := pg.UpdateDeployment(ctx, prod); err != nil {
		t.Fatal(err)
	}
	prev, err := pg.CreateDeploymentIn(ctx, apps[0].ID, EnvPreview)
	if err != nil {
		t.Fatal(err)
	}
	if prev.Environment != EnvPreview {
		t.Fatalf("environment = %q", prev.Environment)
	}
	prev.Status = StatusLive
	prev.HostPort = 49153
	if err := pg.UpdateDeployment(ctx, prev); err != nil {
		t.Fatal(err)
	}
	dup, err := pg.CreateDeploymentIn(ctx, apps[0].ID, EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	dup.Status = StatusLive
	dup.HostPort = 49154
	if err := pg.UpdateDeployment(ctx, dup); err == nil {
		t.Fatal("two production LIVE rows must violate unique (application_id, environment)")
	}
}

func TestCutoverLiveReplacesSameEnvironmentAgainstPostgres(t *testing.T) {
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	owner := operatorForTest(t, pg)
	name := "p74-" + time.Now().UTC().Format("20060102T150405.000000000")
	proj, err := pg.CreateProject(ctx, owner.ID, ProjectInput{
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
	old, err := pg.CreateDeploymentIn(ctx, apps[0].ID, EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	old.Status = StatusLive
	old.HostPort = 49152
	if err := pg.UpdateDeployment(ctx, old); err != nil {
		t.Fatal(err)
	}
	next, err := pg.CreateDeploymentIn(ctx, apps[0].ID, EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	next.HostPort = 49153
	next.PublicURL = "http://hello.localhost:9080/"
	replaced, err := pg.CutoverLive(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(replaced) != 1 || replaced[0].ID != old.ID {
		t.Fatalf("replaced = %#v", replaced)
	}
	gotOld, err := pg.GetDeployment(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotOld.Status != StatusStopped {
		t.Fatalf("old = %q", gotOld.Status)
	}
	gotNext, err := pg.GetDeployment(ctx, next.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotNext.Status != StatusLive || gotNext.HostPort != 49153 {
		t.Fatalf("next = %+v", gotNext)
	}
}

func TestClaimQueuedJobAgainstPostgres(t *testing.T) {
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	owner := operatorForTest(t, pg)
	name := "p14-" + time.Now().UTC().Format("20060102T150405.000000000")
	proj, err := pg.CreateProject(ctx, owner.ID, ProjectInput{
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
	created, err := pg.CreateDeployment(ctx, apps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := pg.ClaimQueuedJob(ctx, created.ID, "job-a")
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ActiveJobID != "job-a" || claimed.Status != StatusQueued {
		t.Fatalf("claimed = %+v", claimed)
	}
	again, err := pg.ClaimQueuedJob(ctx, created.ID, "job-b")
	if err != nil {
		t.Fatal(err)
	}
	if again.ActiveJobID != "job-b" {
		t.Fatalf("second claim = %q", again.ActiveJobID)
	}
	again.Status = StatusDetecting
	if err := pg.UpdateDeployment(ctx, again); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.ClaimQueuedJob(ctx, created.ID, "job-c"); !errors.Is(err, ErrConflict) {
		t.Fatalf("detecting claim err = %v", err)
	}
}
