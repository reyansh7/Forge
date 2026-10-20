package detect

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightMissingStartAndInvalidPort(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "web", "package.json"), `{"name":"web","dependencies":{"leftpad":"1.0.0"}}`)
	write(t, filepath.Join(dir, "web", ".env.example"), "DB_PASSWORD=do-not-copy\n")
	write(t, filepath.Join(dir, "api", "Dockerfile"), "FROM scratch\nEXPOSE 70000\n")

	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Service{}
	for _, s := range got {
		byName[s.Name] = s
	}
	web := byName["web"]
	if web.Deployable || !strings.Contains(web.Detail, "missing_start") {
		t.Fatalf("web %+v", web)
	}
	if strings.Contains(web.Detail, "do-not-copy") {
		t.Fatal("preflight must not copy environment file values")
	}
	api := byName["api"]
	if api.Deployable || !strings.Contains(api.Detail, "invalid_port") {
		t.Fatalf("api %+v", api)
	}
}

func TestPreflightUnsupportedAndServiceCap(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "aaa-tools", "Makefile"), "all:\n\ttrue\n")
	for i := 0; i < 17; i++ {
		name := "svc-" + string(rune('a'+i))
		write(t, filepath.Join(dir, name, "Dockerfile"), "FROM scratch\nEXPOSE 8080\n")
	}
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	deployable := 0
	var capRow Service
	var tools Service
	for _, s := range got {
		if s.Deployable {
			deployable++
		}
		if s.Name == "preflight" {
			capRow = s
		}
		if s.Name == "aaa-tools" {
			tools = s
		}
	}
	if deployable > 16 {
		t.Fatalf("deployable %d", deployable)
	}
	if capRow.Deployable || !strings.Contains(capRow.Detail, "preflight_service_cap") {
		t.Fatalf("cap %+v", capRow)
	}
	if tools.Deployable || !strings.Contains(tools.Detail, "unsupported_strategy") {
		t.Fatalf("aaa-tools %+v", tools)
	}
}

func TestRejectInvalidExposeAllowsDefault(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\n")
	if err := RejectInvalidExpose(dir); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\nEXPOSE 8080\n")
	if err := RejectInvalidExpose(dir); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\nEXPOSE 0\n")
	if err := RejectInvalidExpose(dir); err == nil || !strings.Contains(err.Error(), "invalid_port") {
		t.Fatalf("got %v", err)
	}
}
