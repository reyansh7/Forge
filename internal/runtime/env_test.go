package runtime

import (
	"strings"
	"testing"
)

func TestValidateEnvPairRejectsReserved(t *testing.T) {
	if err := validateEnvPair(EnvPair{Key: "PORT", Value: "9"}); err == nil {
		t.Fatal("PORT")
	}
	if err := validateEnvPair(EnvPair{Key: "HOST", Value: "1"}); err == nil {
		t.Fatal("HOST")
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
	args, err := dockerRunArgs("forgeimg", "forge-run-abc", "127.0.0.1:9:8080", nil, 8080, nil, "", "", Resources{})
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
	if !containsPair(args, "-p", "127.0.0.1:9:8080") {
		t.Fatalf("publish: %q", joined)
	}
}

func TestDockerRunArgsNamedVolume(t *testing.T) {
	name := "forgevol_aaaaaaaabbbbccccddddeeeeeeeeeeee_data"
	args, err := dockerRunArgs("forgeimg", "forge-run-abc", "127.0.0.1:9:8080", nil, 8080, []VolumeMount{
		{Name: name, Path: "/data"},
	}, "", "", Resources{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(args, "-v", name+":/data") {
		t.Fatalf("named volume: %q", strings.Join(args, " "))
	}
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "docker.sock") || strings.Contains(joined, "/var/run") {
		t.Fatalf("forbidden path: %q", joined)
	}
}

func TestDockerRunArgsPreviewVolume(t *testing.T) {
	name := "forgevol_aaaaaaaabbbbccccddddeeeeeeeeeeee_preview_data"
	args, err := dockerRunArgs("forgeimg", "forge-run-abc", "127.0.0.1:9:8080", nil, 8080, []VolumeMount{
		{Name: name, Path: "/data"},
	}, "", "", Resources{})
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(args, "-v", name+":/data") {
		t.Fatalf("preview volume: %q", strings.Join(args, " "))
	}
}

func TestDockerRunArgsRejectsHostBind(t *testing.T) {
	if _, err := dockerRunArgs("forgeimg", "c", "127.0.0.1:9:8080", nil, 8080, []VolumeMount{
		{Name: "/var/run/docker.sock", Path: "/var/run/docker.sock"},
	}, "", "", Resources{}); err == nil {
		t.Fatal("host bind")
	}
	if _, err := dockerRunArgs("forgeimg", "c", "127.0.0.1:9:8080", nil, 8080, []VolumeMount{
		{Name: "forgevol_aaaaaaaabbbbccccddddeeeeeeeeeeee_data", Path: "/etc"},
	}, "", "", Resources{}); err == nil {
		t.Fatal("reserved path")
	}
}

func TestDockerRunArgsProjectNetworkStaysLoopback(t *testing.T) {
	args, err := dockerRunArgs("forgeimg", "forge-run-abc", "127.0.0.1:9:8080", nil, 8080, nil, "forgep-aaaaaaaabbbb", "api", Resources{})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !containsPair(args, "--network", "forgep-aaaaaaaabbbb") || !containsPair(args, "--network-alias", "api") {
		t.Fatalf("%s", joined)
	}
	if strings.Contains(joined, "--network host") || strings.Contains(joined, "0.0.0.0:") {
		t.Fatalf("%s", joined)
	}
	if !containsPair(args, "-p", "127.0.0.1:9:8080") {
		t.Fatalf("%s", joined)
	}
	if _, err := dockerRunArgs("forgeimg", "c", "127.0.0.1:9:8080", nil, 8080, nil, "host", "api", Resources{}); err == nil {
		t.Fatal("host network must be rejected")
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
