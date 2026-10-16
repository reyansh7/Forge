package store

import "testing"

func TestValidateVolumeName(t *testing.T) {
	if _, err := ValidateVolumeName("data"); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateVolumeName("uploads-v2"); err != nil {
		t.Fatal(err)
	}
	if got, err := ValidateVolumeName("  DATA  "); err != nil || got != "data" {
		t.Fatalf("got %q err=%v", got, err)
	}
	for _, bad := range []string{"", "Data_1", "../etc", "a/b", "-x", "x-", "docker.sock"} {
		if _, err := ValidateVolumeName(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestValidateMountPathRejectsHostAndReserved(t *testing.T) {
	if _, err := ValidateMountPath("/data"); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateMountPath("/app/data"); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateMountPath("/var/lib/myapp"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		"", "/", "/tmp", "/app", "/etc", "/etc/passwd",
		"/var/run/docker.sock", "/run/docker.sock",
		"/root", "/home/operator", "/proc", "/sys",
		"/data/../etc", "../data", "data", "C:\\Users",
		"/data:/etc", `C:\Users\reyan`, "/data\n/etc",
		"/.ssh", "/.forge/data.key",
	} {
		if _, err := ValidateMountPath(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestDockerVolumeNameIsAppScoped(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	got, err := DockerVolumeName(id, "data", EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	want := "forgevol_aaaaaaaabbbbccccddddeeeeeeeeeeee_data"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if _, err := DockerVolumeName("not-a-uuid", "data", EnvProduction); err == nil {
		t.Fatal("expected invalid application id")
	}
}

func TestDockerVolumeNameIsolatesPreview(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	prod, err := DockerVolumeName(id, "data", EnvProduction)
	if err != nil {
		t.Fatal(err)
	}
	prev, err := DockerVolumeName(id, "data", EnvPreview)
	if err != nil {
		t.Fatal(err)
	}
	if prod == prev {
		t.Fatalf("preview must not reuse production name %q", prod)
	}
	if prev != "forgevol_aaaaaaaabbbbccccddddeeeeeeeeeeee_preview_data" {
		t.Fatalf("preview name = %q", prev)
	}
	names, err := DockerVolumeNames(id, "data")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != prod || names[1] != prev {
		t.Fatalf("DockerVolumeNames = %#v", names)
	}
}

func TestMountsOverlap(t *testing.T) {
	if !mountsOverlap("/data", "/data") {
		t.Fatal("equal")
	}
	if !mountsOverlap("/data", "/data/uploads") {
		t.Fatal("prefix")
	}
	if mountsOverlap("/data", "/database") {
		t.Fatal("sibling names must not overlap")
	}
}
