package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestIdentitySessionAuditClaimAgainstPostgres(t *testing.T) {
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	u := operatorForTest(t, pg)

	got, err := pg.GetUserByUsername(ctx, strings.ToUpper(u.Username))
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != u.ID {
		t.Fatalf("lookup = %q", got.ID)
	}

	raw, tokenHash, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if err := pg.CreateSession(ctx, u.ID, tokenHash, SessionExpiry(time.Now())); err != nil {
		t.Fatal(err)
	}

	sess, err := pg.LookupSession(ctx, HashSessionToken(raw))
	if err != nil {
		t.Fatal(err)
	}
	if sess.ID != u.ID {
		t.Fatalf("session user = %q", sess.ID)
	}

	if err := pg.RecordAudit(ctx, AuditEvent{
		ActorID:      u.ID,
		Action:       "login",
		ResourceType: "session",
		ResourceID:   u.ID,
		IP:           "127.0.0.1",
		Metadata:     map[string]string{"username": u.Username},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := pg.db.ExecContext(ctx, `
		INSERT INTO projects (name, repository_url, owner_id)
		VALUES ($1, $2, NULL)
	`, "orphan-"+u.Username, SampleHelloURL); err != nil {
		t.Fatal(err)
	}
	n, err := pg.ClaimOrphanedProjects(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("claimed %d, want at least 1", n)
	}
	t.Cleanup(func() {
		_, _ = pg.db.ExecContext(context.Background(), `DELETE FROM projects WHERE owner_id = $1::uuid`, u.ID)
	})

	if _, err := pg.LookupSession(ctx, HashSessionToken("deadbeef")); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bogus token: %v", err)
	}
}

func TestCreateFirstUserConflictsWhenUsersExist(t *testing.T) {
	pg := postgresForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = operatorForTest(t, pg)
	hash, err := HashPassword("test-pass-ok")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pg.CreateFirstUser(ctx, "second-operator", hash); !errors.Is(err, ErrConflict) {
		t.Fatalf("second bootstrap: %v", err)
	}
}
