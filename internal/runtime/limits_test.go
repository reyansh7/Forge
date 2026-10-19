package runtime

import (
	"strings"
	"testing"
)

func TestResourceDefaultsAndBounds(t *testing.T) {
	flags, err := resourceFlags(Resources{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"--memory", "256m", "--cpus", "0.500", "--pids-limit", "256"}
	if len(flags) != len(want) {
		t.Fatalf("%v", flags)
	}
	for i := range want {
		if flags[i] != want[i] {
			t.Fatalf("got %v", flags)
		}
	}
	flags, err = resourceFlags(Resources{MemoryMB: 512, CPUMillis: 1000, Pids: 128})
	if err != nil {
		t.Fatal(err)
	}
	if flags[1] != "512m" || flags[3] != "1.000" || flags[5] != "128" {
		t.Fatalf("override %v", flags)
	}
	if err := (Resources{MemoryMB: 4096}).Validate(); err == nil {
		t.Fatal("memory above 2048 must be rejected")
	}
	if err := (Resources{CPUMillis: 50}).Validate(); err == nil {
		t.Fatal("cpu below 100 must be rejected")
	}
	if err := (Resources{Pids: 8}).Validate(); err == nil {
		t.Fatal("pid cap below 32 must be rejected")
	}
	if err := (Resources{MemoryMB: -1}).Validate(); err == nil {
		t.Fatal("negative memory must be rejected")
	}
	if _, err := resourceFlags(Resources{MemoryMB: 8192}); err == nil {
		t.Fatal("docker argv must not be built for an unsafe budget")
	}
	args, err := dockerRunArgs("forgeimg", "forge-run-abc", "127.0.0.1:9:8080", nil, 8080, nil, "", "", Resources{MemoryMB: 512, CPUMillis: 1000, Pids: 64})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--memory 512m") || !strings.Contains(joined, "--cpus 1.000") || !strings.Contains(joined, "--pids-limit 64") {
		t.Fatalf("limits not enforced: %s", joined)
	}
	if _, err := dockerRunArgs("forgeimg", "forge-run-abc", "127.0.0.1:9:8080", nil, 8080, nil, "", "", Resources{MemoryMB: 8192}); err == nil {
		t.Fatal("unsafe budget reached docker argv")
	}
}
