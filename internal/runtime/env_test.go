package runtime

import "testing"

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
