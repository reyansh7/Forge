package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reyansh7/Forge/internal/detect"
)

func TestPrepareDockerfileWritesGoRecipe(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareDockerfile(dir, string(detect.KindGo)); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "go build") {
		t.Fatalf("%s", body)
	}
}

func TestPrepareDockerfileLeavesUserDockerfile(t *testing.T) {
	dir := t.TempDir()
	want := "FROM scratch\nEXPOSE 80\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareDockerfile(dir, string(detect.KindDockerfile)); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != want {
		t.Fatalf("rewrote user Dockerfile: %s", body)
	}
}

func TestDockerRunArgsUsesDetectedPort(t *testing.T) {
	args, err := dockerRunArgs("forgeimg", "forge-run-abc", "127.0.0.1:9:3000", nil, 3000)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "PORT=3000") {
		t.Fatalf("%q", joined)
	}
	if !containsPair(args, "-p", "127.0.0.1:9:3000") {
		t.Fatalf("publish: %q", joined)
	}
}
