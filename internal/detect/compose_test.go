package detect

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stackCompose = `
services:
  client:
    build:
      context: ./client
      dockerfile: Dockerfile
  api:
    build:
      context: ./server
      dockerfile: Dockerfile
    depends_on:
      postgres:
        condition: service_healthy
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_PASSWORD: do-not-print-this
    volumes:
      - postgres_data:/var/lib/postgresql/data
    ports:
      - "5433:5432"
`

func TestComposeWithoutRootDockerfileIsNotOneApplication(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "docker-compose.yml"), stackCompose)
	write(t, filepath.Join(dir, "docker-compose.dev.yml"), "services:\n  client:\n    build: ./client\n")
	write(t, filepath.Join(dir, "client", "Dockerfile"), "FROM scratch\nEXPOSE 80\n")
	write(t, filepath.Join(dir, "server", "Dockerfile"), "FROM scratch\nEXPOSE 5000\n")

	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Deployable {
		t.Fatalf("discover %+v", got)
	}
	for _, want := range []string{"forge.yaml", "client", "api", "postgres", "private", "not created or deleted", "docker-compose.dev.yml", "do-not-print-this"} {
		has := strings.Contains(got[0].Detail, want)
		if want == "do-not-print-this" {
			if has {
				t.Fatal("compose password leaked into the diagnostic")
			}
			continue
		}
		if !has {
			t.Fatalf("detail missing %q:\n%s", want, got[0].Detail)
		}
	}
	_, err = Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "forge.yaml") || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("tree: %v", err)
	}
}

func TestForgeYAMLRoutesToMultipleServices(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "docker-compose.yml"), stackCompose)
	write(t, filepath.Join(dir, "client", "Dockerfile"), "FROM scratch\nEXPOSE 80\n")
	write(t, filepath.Join(dir, "server", "Dockerfile"), "FROM scratch\nEXPOSE 5000\n")
	write(t, filepath.Join(dir, "forge.yaml"), `
services:
  - name: api
    root: server
    public: false
    port: 5000
    health: /api/health
  - name: client
    root: client
    public: true
    port: 80
    health: /nginx-health
    depends_on: [api]
`)
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "api" || got[1].Name != "client" || !got[0].Deployable || !got[1].Deployable {
		t.Fatalf("%+v", got)
	}
	if got[0].Public == nil || *got[0].Public {
		t.Fatal("api must be private")
	}
	_, err = Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "multi-service") || !strings.Contains(err.Error(), "api") {
		t.Fatalf("root deploy should explain the manifest: %v", err)
	}
}

func TestInvalidManifestFailsBeforeABuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "a"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "docker-compose.yml"), stackCompose)
	write(t, filepath.Join(dir, "forge.yaml"), `
services:
  - name: a
    root: a
    kind: python
    public: true
    depends_on: [a]
`)
	_, err := Discover(dir)
	if err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("invalid manifest: %v", err)
	}
	_, err = Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("tree should fail the manifest before a build: %v", err)
	}
}

func TestRootDockerfileStaysSingleService(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\nEXPOSE 8080\n")
	write(t, filepath.Join(dir, "docker-compose.yml"), stackCompose)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindDockerfile {
		t.Fatalf("kind %s", got.Kind)
	}
	services, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	var rootDeployable bool
	for _, svc := range services {
		if svc.Root == "." && svc.Deployable {
			rootDeployable = true
		}
	}
	if !rootDeployable {
		t.Fatalf("root Dockerfile should stay deployable: %+v", services)
	}
}
