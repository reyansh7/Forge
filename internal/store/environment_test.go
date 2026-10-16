package store

import (
	"strings"
	"testing"
)

func TestParseEnvironment(t *testing.T) {
	got, err := ParseEnvironment("")
	if err != nil || got != EnvProduction {
		t.Fatalf("empty = %q %v", got, err)
	}
	got, err = ParseEnvironment("PREVIEW")
	if err != nil || got != EnvPreview {
		t.Fatalf("preview = %q %v", got, err)
	}
	if _, err := ParseEnvironment("staging"); err == nil {
		t.Fatal("staging is not an environment")
	}
}

func TestPreviewLocalHost(t *testing.T) {
	if PreviewLocalHost("api") != "api-preview" {
		t.Fatalf("%q", PreviewLocalHost("api"))
	}
	if PreviewLocalHost("api-preview") != "api-preview" {
		t.Fatal("already suffixed")
	}
	got := PreviewLocalHost(strings.Repeat("a", 63))
	if len(got) > 63 || !strings.HasSuffix(got, "-preview") {
		t.Fatalf("truncated = %q len=%d", got, len(got))
	}
}

func TestRouteLocalHost(t *testing.T) {
	if RouteLocalHost(EnvProduction, "api") != "api" {
		t.Fatal("production")
	}
	if RouteLocalHost(EnvPreview, "api") != "api-preview" {
		t.Fatal("preview")
	}
}

func TestValidateLocalHostRejectsPreviewSuffix(t *testing.T) {
	if _, err := ValidateLocalHost("hello-preview"); err == nil {
		t.Fatal("operator must not claim the preview suffix")
	}
	if _, err := ValidateLocalHost("hello"); err != nil {
		t.Fatal(err)
	}
}
