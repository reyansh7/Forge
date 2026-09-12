package store

import (
	"errors"
	"testing"
)

func TestMigrationFileNamesAreVersioned(t *testing.T) {
	// Guard: embed worked and 0001_ sorts first (apply order is lexicographic).
	names, err := migrationFileNames()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("expected at least one embedded migration")
	}
	if names[0] != "0001_projects.sql" {
		t.Fatalf("first migration = %q, want 0001_projects.sql", names[0])
	}
	if len(names) < 2 || names[1] != "0002_deployments.sql" {
		t.Fatalf("second migration = %v, want 0002_deployments.sql", names)
	}
	if len(names) < 3 || names[2] != "0003_applications.sql" {
		t.Fatalf("third migration = %v, want 0003_applications.sql", names)
	}
	if len(names) < 4 || names[3] != "0004_one_live_per_application.sql" {
		t.Fatalf("fourth migration = %v, want 0004_one_live_per_application.sql", names)
	}
	if len(names) < 5 || names[4] != "0005_application_settings.sql" {
		t.Fatalf("fifth migration = %v, want 0005_application_settings.sql", names)
	}
	if len(names) < 7 || names[5] != "0006_identity.sql" || names[6] != "0007_runtime_metadata.sql" {
		t.Fatalf("migrations = %v, want …0006_identity.sql, 0007_runtime_metadata.sql", names)
	}
}

func TestUsesBundledHello(t *testing.T) {
	if !UsesBundledHello(SampleHelloURL) {
		t.Fatal("forge://hello")
	}
	if !UsesBundledHello("https://github.com/example/forge.git") {
		t.Fatal("github.com/example must use the bundled sample")
	}
	if UsesBundledHello("https://github.com/octocat/Hello-World") {
		t.Fatal("real orgs must not be rewritten to the sample")
	}
}

func TestValidateProjectInputAcceptsForgeHello(t *testing.T) {
	if _, err := ValidateProjectInput("hello", SampleHelloURL); err != nil {
		t.Fatal(err)
	}
}

func TestValidateProjectInputRejectsArbitraryForge(t *testing.T) {
	if _, err := ValidateProjectInput("x", "forge://other"); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateProjectInputAcceptsHTTPS(t *testing.T) {
	// Trim is part of the contract: "  demo  " must become "demo".
	in, err := ValidateProjectInput("  demo  ", "https://github.com/example/app.git")
	if err != nil {
		t.Fatal(err)
	}
	if in.Name != "demo" {
		t.Fatalf("Name = %q", in.Name)
	}
}

func TestValidateProjectInputAcceptsSCPGitRemote(t *testing.T) {
	if _, err := ValidateProjectInput("demo", "git@github.com:example/app.git"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateProjectInputRejectsEmptyName(t *testing.T) {
	if _, err := ValidateProjectInput("  ", "https://example.com/repo.git"); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateProjectInputRejectsFileURL(t *testing.T) {
	// file:// must never be stored; a later fetch must not hit the host disk.
	if _, err := ValidateProjectInput("demo", "file:///etc/passwd"); err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateProjectInputRejectsControlChars(t *testing.T) {
	if _, err := ValidateProjectInput("bad\nname", "https://example.com/repo.git"); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseProjectIDRejectsGarbage(t *testing.T) {
	if _, err := ParseProjectID("not-a-uuid"); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("err = %v", err)
	}
}

func TestParseProjectIDLowercases(t *testing.T) {
	got, err := ParseProjectID("AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE")
	if err != nil {
		t.Fatal(err)
	}
	if got != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Fatalf("got %q", got)
	}
}
