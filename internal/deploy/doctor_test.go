package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reyansh7/Forge/internal/runtime"
)

func TestEnvDoctorKeepsKeysAndDropsValues(t *testing.T) {
	dir := t.TempDir()
	secret := "example-password-must-not-appear"
	stored := "stored-value-must-not-appear"
	if err := os.WriteFile(filepath.Join(dir, ".env.example"), []byte("DB_PASSWORD="+secret+"\nVITE_API_URL=http://example.test\nPORT=5000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	note := envDoctor(dir, []runtime.EnvPair{{Key: "VITE_API_URL", Value: stored}})
	if strings.Contains(note, secret) || strings.Contains(note, stored) || strings.Contains(note, "example.test") {
		t.Fatalf("doctor leaked a value: %q", note)
	}
	if !strings.Contains(note, "env_missing") || !strings.Contains(note, "DB_PASSWORD") {
		t.Fatalf("missing key not named: %q", note)
	}
	if strings.Contains(note, "env_missing .env.example key VITE_API_URL") {
		t.Fatalf("stored key reported missing: %q", note)
	}
	err := withEnvDoctor(dir, nil, os.ErrNotExist)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "env_missing") {
		t.Fatalf("failure note: %v", err)
	}
}
