package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/reyansh7/Forge/internal/secrets"
)

func TestGitHubAccountAndBindingAgainstPostgres(t *testing.T) {
	pg := postgresForTest(t)
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
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
		Name:          "gh-" + time.Now().UTC().Format("20060102T150405.000000000"),
		RepositoryURL: SampleHelloURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pg.db.ExecContext(context.Background(), `DELETE FROM projects WHERE id = $1::uuid`, proj.ID)
		_, _ = pg.db.ExecContext(context.Background(), `DELETE FROM github_accounts WHERE user_id = $1::uuid`, owner.ID)
	})
	apps, err := pg.ListApplicationsByProject(ctx, proj.ID)
	if err != nil || len(apps) == 0 {
		t.Fatalf("apps: %v", err)
	}

	acc, err := pg.UpsertGitHubAccount(ctx, owner.ID, 4242, "octocat", "gho_test_token_value")
	if err != nil {
		t.Fatal(err)
	}
	if acc.AccessToken != "gho_test_token_value" {
		t.Fatal("Open must return plaintext to the process")
	}
	var stored string
	if err := pg.db.QueryRowContext(ctx, `SELECT access_token FROM github_accounts WHERE user_id = $1::uuid`, owner.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == "gho_test_token_value" || !secrets.Sealed(stored) {
		t.Fatalf("token must be sealed at rest, got %q", stored)
	}
	tok, err := pg.GitHubCloneToken(ctx, apps[0].ID)
	if err != nil || tok != "" {
		t.Fatalf("non-github url must not use the oauth token, got %q err=%v", tok, err)
	}
	if _, err := pg.UpdateApplication(ctx, apps[0].ID, ApplicationInput{
		Name:                 apps[0].Name,
		RepositoryURL:        "https://github.com/octo/hello.git",
		RootDirectory:        apps[0].RootDirectory,
		HealthPath:           apps[0].HealthPath,
		LocalHost:            apps[0].LocalHost,
		BuildPackage:         apps[0].BuildPackage,
		MemoryMB:             apps[0].MemoryMB,
		CPUMillis:            apps[0].CPUMillis,
		PidsLimit:            apps[0].PidsLimit,
		HealthTimeoutSeconds: apps[0].HealthTimeoutSeconds,
		Exposure:             apps[0].Exposure,
	}); err != nil {
		t.Fatal(err)
	}
	tok, err = pg.GitHubCloneToken(ctx, apps[0].ID)
	if err != nil || tok != "gho_test_token_value" {
		t.Fatalf("owner token fallback %q err=%v", tok, err)
	}

	secret, err := NewWebhookSecret()
	if err != nil {
		t.Fatal(err)
	}
	b, err := pg.UpsertApplicationGitHub(ctx, ApplicationGitHub{
		ApplicationID: apps[0].ID,
		OwnerID:       owner.ID,
		RepoFullName:  "octo/hello",
		Branch:        "main",
		Environment:   EnvProduction,
		WebhookID:     99,
		WebhookSecret: secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	if b.WebhookSecret != secret {
		t.Fatal("webhook secret Open mismatch")
	}
	got, err := pg.GetApplicationGitHubByRepoBranch(ctx, "octo/hello", "main")
	if err != nil || got.ApplicationID != apps[0].ID {
		t.Fatalf("lookup: %+v err=%v", got, err)
	}
	tok, err = pg.GitHubCloneToken(ctx, apps[0].ID)
	if err != nil || tok != "gho_test_token_value" {
		t.Fatalf("clone token %q err=%v", tok, err)
	}

	deliveryID := "delivery-" + time.Now().UTC().Format("150405.000000000")
	fresh, err := pg.ClaimWebhookDelivery(ctx, deliveryID)
	if err != nil || !fresh {
		t.Fatalf("first claim: %v %v", fresh, err)
	}
	fresh, err = pg.ClaimWebhookDelivery(ctx, deliveryID)
	if err != nil || fresh {
		t.Fatalf("dup claim must be false: %v %v", fresh, err)
	}

	d, err := pg.CreateDeploymentIn(ctx, apps[0].ID, EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	d, err = pg.SetDeploymentCommit(ctx, d.ID, CommitMeta{SHA: "abc1234", Message: "hi", Author: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if d.CommitSHA != "abc1234" || d.CommitAuthor != "Ada" {
		t.Fatalf("commit %+v", d)
	}

	if err := pg.DeleteApplicationGitHub(ctx, apps[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pg.GetApplicationGitHub(ctx, apps[0].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected not found: %v", err)
	}
}
