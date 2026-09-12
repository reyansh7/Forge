package runtime

import (
	"strings"
	"testing"
)

func TestValidateEnvPairRejectsReserved(t *testing.T) {
	if err := validateEnvPair(EnvPair{Key: "PORT", Value: "9"}); err == nil {
		t.Fatal("PORT")
	}
	if err := validateEnvPair(EnvPair{Key: "FORGE_X", Value: "1"}); err == nil {
		t.Fatal("FORGE_")
	}
	if err := validateEnvPair(EnvPair{Key: "OK", Value: "a\nb"}); err == nil {
		t.Fatal("newline")
	}
	if err := validateEnvPair(EnvPair{Key: "OK", Value: "hi"}); err != nil {
		t.Fatal(err)
	}
}

func TestContainerNameStripsHyphens(t *testing.T) {
	got := ContainerName("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	if got != "forge-run-aaaaaaaabbbbccccddddeeeeeeeeeeee" {
		t.Fatalf("got %q", got)
	}
}

func TestDockerLogsFollowArgs(t *testing.T) {
	args, err := dockerLogsFollowArgs("forge-run-abc", 50)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-f") || !strings.Contains(joined, "--tail") {
		t.Fatalf("follow args: %q", joined)
	}
	if args[len(args)-1] != "forge-run-abc" || args[len(args)-2] != "--" {
		t.Fatalf("name boundary: %#v", args)
	}
	if _, err := dockerLogsFollowArgs("not a name", 10); err == nil {
		t.Fatal("expected invalid container name")
	}
}

func TestDockerRunArgsDropCapabilities(t *testing.T) {
	args, err := dockerRunArgs("forgeimg", "forge-run-abc", "127.0.0.1:9:8080", nil)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !containsPair(args, "--cap-drop", "ALL") {
		t.Fatalf("cap-drop: %q", joined)
	}
	if !containsPair(args, "--security-opt", "no-new-privileges") {
		t.Fatalf("no-new-privileges: %q", joined)
	}
	if !containsPair(args, "--pull", "never") {
		t.Fatalf("pull: %q", joined)
	}
	if !strings.Contains(joined, "--tmpfs") {
		t.Fatalf("tmpfs: %q", joined)
	}
	if strings.Contains(joined, "--privileged") || strings.Contains(joined, "docker.sock") {
		t.Fatalf("forbidden flag: %q", joined)
	}
	if !strings.HasPrefix(args[len(args)-3], "127.0.0.1:") && !containsPair(args, "-p", "127.0.0.1:9:8080") {
		t.Fatalf("publish: %q", joined)
	}
}

func containsPair(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
