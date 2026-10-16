package store

import (
	"context"
	"testing"
	"time"
)

func TestSagaCompensationAgainstPostgres(t *testing.T) {
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	owner := operatorForTest(t, pg)
	proj, err := pg.CreateProject(ctx, owner.ID, ProjectInput{
		Name:          "saga-" + time.Now().UTC().Format("150405.000000000"),
		RepositoryURL: SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.db.ExecContext(context.Background(), `DELETE FROM projects WHERE id = $1::uuid`, proj.ID)
	})
	apps, err := pg.ListApplicationsByProject(ctx, proj.ID)
	if err != nil || len(apps) != 1 {
		t.Fatalf("apps %d err %v", len(apps), err)
	}
	api, err := pg.CreateApplication(ctx, proj.ID, ApplicationInput{
		Name: "api", RepositoryURL: SampleHelloURL, RootDirectory: ".", HealthPath: "/", BuildPackage: "./cmd/api",
	})
	if err != nil {
		t.Fatal(err)
	}
	web := apps[0]

	prev, err := pg.CreateDeployment(ctx, api.ID)
	if err != nil {
		t.Fatal(err)
	}
	prev.Status = StatusStopped
	prev.ContainerName = "prev"
	if err := pg.UpdateDeployment(ctx, prev); err != nil {
		t.Fatal(err)
	}
	cur, err := pg.CreateDeployment(ctx, api.ID)
	if err != nil {
		t.Fatal(err)
	}
	cur.Status = StatusLive
	cur.ContainerName = "cur"
	cur.ListenPort = 8080
	if err := pg.UpdateDeployment(ctx, cur); err != nil {
		t.Fatal(err)
	}
	webDep, err := pg.CreateDeployment(ctx, web.ID)
	if err != nil {
		t.Fatal(err)
	}
	webDep.Status = StatusLive
	if err := pg.UpdateDeployment(ctx, webDep); err != nil {
		t.Fatal(err)
	}

	roll, err := pg.CreateProjectDeployment(ctx, proj.ID, "", []byte(`{"services":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.AddProjectDeploymentService(ctx, ProjectDeploymentService{
		ProjectDeploymentID: roll.ID, ApplicationID: api.ID, DeploymentID: cur.ID,
		ServiceName: "api", RootDirectory: ".", BuildPackage: "./cmd/api", Status: "queued",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.AddProjectDeploymentService(ctx, ProjectDeploymentService{
		ProjectDeploymentID: roll.ID, ApplicationID: web.ID, DeploymentID: webDep.ID,
		ServiceName: "frontend", RootDirectory: "frontend", Status: "queued",
	}); err != nil {
		t.Fatal(err)
	}
	if err := pg.MarkPromoted(ctx, cur.ID, prev.ID); err != nil {
		t.Fatal(err)
	}
	view, err := pg.SagaView(ctx, cur.ID)
	if err != nil || !view.InSaga || view.ProjectID != proj.ID {
		t.Fatalf("%+v err %v", view, err)
	}
	var saw bool
	for _, sib := range view.Siblings {
		if sib.DeploymentID == cur.ID {
			saw = sib.PreviousDeploymentID == prev.ID && !sib.PromotedAt.IsZero()
		}
	}
	if !saw {
		t.Fatalf("siblings %+v", view.Siblings)
	}
	stops, committed, err := pg.CommitIfReady(ctx, cur.ID)
	if err != nil || !committed {
		t.Fatalf("commit %v stops %d err %v", committed, len(stops), err)
	}
	foundPrev := false
	for _, d := range stops {
		if d.ID == prev.ID {
			foundPrev = true
		}
	}
	if !foundPrev {
		t.Fatalf("held previous missing: %+v", stops)
	}
	again, committed, err := pg.CommitIfReady(ctx, cur.ID)
	if err != nil || committed || len(again) != 0 {
		t.Fatalf("second commit %v rows %d err %v", committed, len(again), err)
	}

	if err := pg.RestorePrevious(ctx, cur.ID, prev.ID); err != nil {
		t.Fatal(err)
	}
	gotPrev, err := pg.GetDeployment(ctx, prev.ID)
	if err != nil || gotPrev.Status != StatusLive {
		t.Fatalf("previous %+v err %v", gotPrev, err)
	}
	gotCur, err := pg.GetDeployment(ctx, cur.ID)
	if err != nil || gotCur.Status != StatusStopped {
		t.Fatalf("promoted %+v err %v", gotCur, err)
	}
	if err := pg.RestorePrevious(ctx, cur.ID, prev.ID); err == nil {
		t.Fatal("a second restore must fail when the promoted row is no longer live")
	}

	_, rows, err := pg.GetProjectDeployment(ctx, roll.ID)
	if err != nil || len(rows) == 0 {
		t.Fatal(err)
	}
	if err := pg.RecordCompensation(ctx, rows[0].ID, "failed", "routing restore failed"); err != nil {
		t.Fatal(err)
	}
	_, rows, err = pg.GetProjectDeployment(ctx, roll.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Compensation != "failed" || rows[0].CompensationError == "" {
		t.Fatalf("%+v", rows[0])
	}
	if err := pg.SetBuildPackage(ctx, api.ID, "./cmd/worker"); err != nil {
		t.Fatal(err)
	}
	got, err := pg.GetApplication(ctx, api.ID)
	if err != nil || got.BuildPackage != "./cmd/worker" {
		t.Fatalf("%+v err %v", got, err)
	}
}

func TestProjectNetworkName(t *testing.T) {
	name, err := ProjectNetworkName("AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEE1")
	if err != nil || name != "forgep-aaaaaaaabbbb" {
		t.Fatalf("%s %v", name, err)
	}
	if _, err := ProjectNetworkName("not-a-uuid"); err == nil {
		t.Fatal("expected rejection")
	}
	if got := InternalServiceURL("api", 8080); got != "http://api:8080/" {
		t.Fatal(got)
	}
}
