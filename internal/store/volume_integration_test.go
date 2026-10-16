package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestApplicationVolumeCRUD(t *testing.T) {
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	owner := operatorForTest(t, pg)
	proj, err := pg.CreateProject(ctx, owner.ID, ProjectInput{
		Name:          "vol-" + time.Now().UTC().Format("20060102T150405.000000000"),
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

	if _, err := pg.CreateApplicationVolume(ctx, app.ID, "data", "/var/run/docker.sock"); err == nil {
		t.Fatal("host/socket path must be rejected")
	}

	v, err := pg.CreateApplicationVolume(ctx, app.ID, "data", "/data")
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "data" || v.MountPath != "/data" {
		t.Fatalf("%+v", v)
	}
	if _, err := pg.CreateApplicationVolume(ctx, app.ID, "uploads", "/data/files"); err == nil {
		t.Fatal("overlapping mount must be rejected")
	}
	if _, err := pg.CreateApplicationVolume(ctx, app.ID, "data", "/cache"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name = %v", err)
	}
	list, err := pg.ListApplicationVolumes(ctx, app.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v err=%v", list, err)
	}
	if err := pg.DeleteApplicationVolume(ctx, app.ID, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := pg.DeleteApplicationVolume(ctx, app.ID, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}
