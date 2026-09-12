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
