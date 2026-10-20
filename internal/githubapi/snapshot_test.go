package githubapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reyansh7/Forge/internal/detect"
)

func TestWriteDiscoverTreeSeesNestedServicesAndGoCmd(t *testing.T) {
	var sawSrc bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/repos/acme/demo/contents/"), "/")
		if strings.Contains(path, "apps/web/src") {
			sawSrc = true
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch path {
		case "":
			_ = json.NewEncoder(w).Encode([]contentEntry{dirEntry("apps"), dirEntry("services")})
		case "apps":
			_ = json.NewEncoder(w).Encode([]contentEntry{dirEntry("apps/web")})
		case "apps/web":
			_ = json.NewEncoder(w).Encode([]contentEntry{
				fileEntry("package.json", "apps/web/package.json"),
				dirEntry("apps/web/src"),
			})
		case "apps/web/package.json":
			_ = json.NewEncoder(w).Encode(fileEntry("package.json", "apps/web/package.json"))
		case "services":
			_ = json.NewEncoder(w).Encode([]contentEntry{dirEntry("services/api")})
		case "services/api":
			_ = json.NewEncoder(w).Encode([]contentEntry{
				fileEntry("go.mod", "services/api/go.mod"),
				dirEntry("services/api/cmd"),
			})
		case "services/api/go.mod":
			_ = json.NewEncoder(w).Encode(fileEntry("go.mod", "services/api/go.mod"))
		case "services/api/cmd":
			_ = json.NewEncoder(w).Encode([]contentEntry{dirEntry("services/api/cmd/worker")})
		case "services/api/cmd/worker":
			_ = json.NewEncoder(w).Encode([]contentEntry{
				fileEntry("main.go", "services/api/cmd/worker/main.go"),
				fileEntry("weights.bin", "services/api/cmd/worker/weights.bin"),
			})
		case "services/api/cmd/worker/main.go":
			_ = json.NewEncoder(w).Encode(fileEntry("main.go", "services/api/cmd/worker/main.go"))
		default:
			t.Errorf("unexpected contents path %q", path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dest := t.TempDir()
	c := &Client{HTTP: srv.Client(), APIBase: srv.URL}
	if err := c.WriteDiscoverTree(context.Background(), "token", "acme/demo", "main", dest); err != nil {
		t.Fatal(err)
	}
	if sawSrc {
		t.Fatal("plan snapshot must not fetch source inside an already reached service directory")
	}
	for _, rel := range []string{
		"apps/web/package.json",
		"services/api/go.mod",
		"services/api/cmd/worker/main.go",
	} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "services", "api", "cmd", "worker", "weights.bin")); !os.IsNotExist(err) {
		t.Fatal("non-manifest files must not be downloaded for discovery")
	}
}

func TestWriteDiscoverTreeSeesThirdLevel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/repos/acme/demo/contents/"), "/")
		w.Header().Set("Content-Type", "application/json")
		switch path {
		case "":
			_ = json.NewEncoder(w).Encode([]contentEntry{dirEntry("group")})
		case "group":
			_ = json.NewEncoder(w).Encode([]contentEntry{dirEntry("group/team")})
		case "group/team":
			_ = json.NewEncoder(w).Encode([]contentEntry{dirEntry("group/team/web")})
		case "group/team/web":
			_ = json.NewEncoder(w).Encode([]contentEntry{
				fileEntry("package.json", "group/team/web/package.json"),
				dirEntry("group/team/web/src"),
			})
		case "group/team/web/package.json":
			_ = json.NewEncoder(w).Encode(fileEntry("package.json", "group/team/web/package.json"))
		case "group/team/web/src":
			t.Error("source inside a service must not be listed")
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected contents path %q", path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dest := t.TempDir()
	c := &Client{HTTP: srv.Client(), APIBase: srv.URL}
	if err := c.WriteDiscoverTree(context.Background(), "token", "acme/demo", "main", dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "group", "team", "web", "package.json")); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverTreeExampleValuesStayOutOfThePlan(t *testing.T) {
	secret := "snapshot-example-secret"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/repos/acme/demo/contents/"), "/")
		w.Header().Set("Content-Type", "application/json")
		switch path {
		case "":
			_ = json.NewEncoder(w).Encode([]contentEntry{dirEntry("api")})
		case "api":
			_ = json.NewEncoder(w).Encode([]contentEntry{
				fileEntry("Dockerfile", "api/Dockerfile"),
				fileBody("api/.env.example", "DB_PASSWORD="+secret+"\n"),
			})
		case "api/Dockerfile":
			_ = json.NewEncoder(w).Encode(fileBody("api/Dockerfile", "FROM scratch\nEXPOSE 8080\n"))
		case "api/.env.example":
			_ = json.NewEncoder(w).Encode(fileBody("api/.env.example", "DB_PASSWORD="+secret+"\n"))
		default:
			t.Errorf("unexpected contents path %q", path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dest := t.TempDir()
	c := &Client{HTTP: srv.Client(), APIBase: srv.URL}
	if err := c.WriteDiscoverTree(context.Background(), "token", "acme/demo", "main", dest); err != nil {
		t.Fatal(err)
	}
	services, err := detect.Discover(dest)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(services)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("plan JSON contained the example file value")
	}
	if !strings.Contains(string(raw), "env_declared") || !strings.Contains(string(raw), "DB_PASSWORD") {
		t.Fatal("plan did not name the example key")
	}
}

func TestDiscoverTreeSkipsOversizedExample(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/repos/acme/demo/contents/"), "/")
		w.Header().Set("Content-Type", "application/json")
		switch path {
		case "":
			_ = json.NewEncoder(w).Encode([]contentEntry{fileBody(".env.example", strings.Repeat("A", 70<<10))})
		case ".env.example":
			_ = json.NewEncoder(w).Encode(fileBody(".env.example", strings.Repeat("A", 70<<10)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dest := t.TempDir()
	c := &Client{HTTP: srv.Client(), APIBase: srv.URL}
	if err := c.WriteDiscoverTree(context.Background(), "token", "acme/demo", "main", dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".env.example")); !os.IsNotExist(err) {
		t.Fatal("an example file over 64 KiB must not be saved")
	}
}

func dirEntry(path string) contentEntry {
	return contentEntry{Name: filepath.Base(path), Path: path, Type: "dir"}
}

func fileEntry(name, path string) contentEntry {
	return fileBody(path, "ok\n")
}

func fileBody(path, body string) contentEntry {
	return contentEntry{
		Name:     filepath.Base(path),
		Path:     path,
		Type:     "file",
		Encoding: "base64",
		Content:  base64.StdEncoding.EncodeToString([]byte(body)),
	}
}
