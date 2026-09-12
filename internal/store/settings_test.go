package store

import (
	"strings"
	"testing"
	"time"
)

func TestValidateRootDirectory(t *testing.T) {
	ok, err := ValidateRootDirectory("  apps/web  ")
	if err != nil || ok != "apps/web" {
		t.Fatalf("got %q err=%v", ok, err)
	}
	if _, err := ValidateRootDirectory("../etc"); err == nil {
		t.Fatal("expected .. rejection")
	}
	if _, err := ValidateRootDirectory("/etc"); err == nil {
		t.Fatal("expected absolute rejection")
	}
	if _, err := ValidateRootDirectory(`C:\Windows`); err == nil {
		t.Fatal("expected drive rejection")
	}
	got, err := ValidateRootDirectory("")
	if err != nil || got != "." {
		t.Fatalf("empty -> . got %q err=%v", got, err)
	}
}

func TestValidateHealthPath(t *testing.T) {
	got, err := ValidateHealthPath("/ready")
	if err != nil || got != "/ready" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if _, err := ValidateHealthPath("http://127.0.0.1/"); err == nil {
		t.Fatal("expected URL rejection")
	}
	if _, err := ValidateHealthPath("ready"); err == nil {
		t.Fatal("must start with /")
	}
}

func TestSuggestedLocalHost(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if got := SuggestedLocalHost("My Portfolio", id); got != "my-portfolio" {
		t.Fatalf("named = %q", got)
	}
	if got := SuggestedLocalHost("app", id); got != "app-aaaa" {
		t.Fatalf("default app = %q", got)
	}
	if got := SuggestedLocalHost("forge", id); got != "forge-aaaa" {
		t.Fatalf("reserved = %q", got)
	}
	if got := SuggestedLocalHost("", "nope"); got != "app-0000" {
		t.Fatalf("empty = %q", got)
	}
}

func TestValidateLocalHost(t *testing.T) {
	got, err := ValidateLocalHost("Hello-App")
	if err != nil || got != "hello-app" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if _, err := ValidateLocalHost("forge"); err == nil {
		t.Fatal("reserved")
	}
	if _, err := ValidateLocalHost("foo.bar"); err == nil {
		t.Fatal("dots")
	}
	empty, err := ValidateLocalHost("  ")
	if err != nil || empty != "" {
		t.Fatalf("empty slug got %q err=%v", empty, err)
	}
}

func TestDeploymentDurationMS(t *testing.T) {
	d := Deployment{
		Status:    StatusFailed,
		CreatedAt: time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 9, 12, 12, 0, 3, 0, time.UTC),
	}
	if d.DurationMS() != 3000 {
		t.Fatalf("duration = %d", d.DurationMS())
	}
}

func TestSanitizeBuildLogKeepsTail(t *testing.T) {
	raw := strings.Repeat("a", maxBuildLogLen+8) + "END"
	got := SanitizeBuildLog(raw)
	if len(got) != maxBuildLogLen {
		t.Fatalf("len = %d", len(got))
	}
	if got[len(got)-3:] != "END" {
		t.Fatalf("tail = %q", got[len(got)-3:])
	}
}
