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

func dirEntry(path string) contentEntry {
	return contentEntry{Name: filepath.Base(path), Path: path, Type: "dir"}
}

func fileEntry(name, path string) contentEntry {
	body := base64.StdEncoding.EncodeToString([]byte("ok\n"))
	return contentEntry{Name: name, Path: path, Type: "file", Encoding: "base64", Content: body}
}
