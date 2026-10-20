package detect

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorDiscardsExampleValues(t *testing.T) {
	dir := t.TempDir()
	secret := "do-not-keep-this-password"
	write(t, filepath.Join(dir, ".env.example"), "# sample\nexport DB_PASSWORD="+secret+"\nVITE_API_URL=http://example.test/api\nPORT=5000\nFORGE_API_ADDR=127.0.0.1:8080\n")
	write(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\nEXPOSE 8080\n")

	plan := CompareEnv(dir, nil, false)
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "example.test") {
		t.Fatalf("example values leaked: %s", raw)
	}
	if !hasCode(plan, "env_declared") || !hasCode(plan, "env_reserved") {
		t.Fatalf("plan %+v", plan)
	}
	for _, d := range plan {
		if d.Blocking {
			t.Fatalf("environment gaps must not block: %+v", d)
		}
		if d.Code == "env_missing" {
			t.Fatal("a plan that has not loaded stored env must not claim a key is missing")
		}
	}

	checked := CompareEnv(dir, []string{"VITE_API_URL"}, true)
	raw, _ = json.Marshal(checked)
	if strings.Contains(string(raw), secret) {
		t.Fatal("stored comparison leaked the example value")
	}
	if !hasEvidence(checked, "env_missing", "DB_PASSWORD") {
		t.Fatalf("missing key not reported: %+v", checked)
	}
	if hasEvidence(checked, "env_missing", "VITE_API_URL") {
		t.Fatal("a stored key must not be reported missing")
	}
	log := FormatDoctor(checked)
	if strings.Contains(log, secret) || !strings.Contains(log, "env_missing") {
		t.Fatalf("log %q", log)
	}
}

func TestDiscoverPlanOmitsExampleValues(t *testing.T) {
	dir := t.TempDir()
	secret := "plan-must-not-contain-this"
	write(t, filepath.Join(dir, "api", "Dockerfile"), "FROM scratch\nEXPOSE 8080\n")
	write(t, filepath.Join(dir, "api", ".env.example"), "DB_PASSWORD="+secret+"\n")
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("plan leaked %s", raw)
	}
	if len(got) != 1 || len(got[0].Diagnoses) == 0 || got[0].Diagnoses[0].Code != "env_declared" {
		t.Fatalf("%+v", got)
	}
}

func hasCode(list []Diagnosis, code string) bool {
	for _, d := range list {
		if d.Code == code {
			return true
		}
	}
	return false
}

func hasEvidence(list []Diagnosis, code, key string) bool {
	for _, d := range list {
		if d.Code == code && strings.Contains(d.Evidence, key) {
			return true
		}
	}
	return false
}
