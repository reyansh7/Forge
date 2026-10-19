package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const resumeManifest = `
services:
  - name: ml-model
    root: ml-model
    kind: python
    public: false
    port: 8000
    health: /health
    volumes:
      - name: models
        path: /models
  - name: backend
    root: backend
    kind: python
    framework: fastapi
    public: true
    port: 8080
    health: /health
    depends_on: [ml-model]
    env:
      ML_SERVICE_URL:
        service: ml-model
        visibility: internal
      CORS_ORIGIN:
        service: frontend
        visibility: public
        origin: true
  - name: frontend
    root: frontend
    kind: node
    framework: next
    runtime: static
    public: true
    port: 3000
    health: /
    depends_on: [backend]
    env:
      NEXT_PUBLIC_BACKEND_URL:
        service: backend
        visibility: public
`

func TestManifestResumeAnalyzerPlan(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"ml-model", "backend", "frontend"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "forge.yaml"), []byte(resumeManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Name != "ml-model" || got[1].Name != "backend" || got[2].Name != "frontend" {
		t.Fatalf("discover order %+v", got)
	}
	ordered, err := OrderServices(got)
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].Name != "ml-model" || ordered[1].Name != "backend" || ordered[2].Name != "frontend" {
		t.Fatalf("order %+v", ordered)
	}
	if !DependsOn(ordered[1], ordered[0]) || !DependsOn(ordered[2], ordered[1]) || DependsOn(ordered[2], ordered[0]) {
		t.Fatal("frontend waits for the backend, and the backend waits for ML")
	}
	links, note, err := ResolveLinks(got)
	if err != nil || note != "" {
		t.Fatalf("links %+v note %q err %v", links, note, err)
	}
	want := map[string]SuggestedLink{}
	for _, link := range links {
		want[link.EnvKey] = link
	}
	if want["ML_SERVICE_URL"].Target != "ml-model" || want["ML_SERVICE_URL"].Source != "backend" {
		t.Fatalf("ml %+v", want["ML_SERVICE_URL"])
	}
	if want["NEXT_PUBLIC_BACKEND_URL"].Target != "backend" || want["NEXT_PUBLIC_BACKEND_URL"].Source != "frontend" {
		t.Fatalf("public %+v", want["NEXT_PUBLIC_BACKEND_URL"])
	}
	if !want["CORS_ORIGIN"].Origin || want["CORS_ORIGIN"].Target != "frontend" {
		t.Fatalf("cors %+v", want["CORS_ORIGIN"])
	}
	if got[0].Public == nil || *got[0].Public {
		t.Fatal("ML must be private")
	}
	if len(got[0].Volumes) != 1 || got[0].Volumes[0].Path != "/models" {
		t.Fatalf("volume %+v", got[0].Volumes)
	}
}

func TestManifestRejectsCycleAndEscape(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	body := []byte(`
services:
  - name: a
    root: a
    kind: python
    public: true
    depends_on: [b]
  - name: b
    root: b
    kind: python
    public: true
    depends_on: [a]
`)
	if err := os.WriteFile(filepath.Join(dir, "forge.yaml"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Discover(dir)
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle: %v", err)
	}
	body = []byte("services:\n  - name: a\n    root: ../secrets\n    kind: go\n    public: false\n")
	if err := os.WriteFile(filepath.Join(dir, "forge.yaml"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = Discover(dir)
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("escape: %v", err)
	}
}
