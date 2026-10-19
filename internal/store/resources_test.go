package store

import "testing"

func TestValidateResourcesDefaultsAndRejects(t *testing.T) {
	if err := ValidateResources(0, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := ValidateResources(256, 500, 256, 45); err != nil {
		t.Fatal(err)
	}
	if err := ValidateResources(2048, 2000, 1024, 180); err != nil {
		t.Fatal(err)
	}
	if err := ValidateResources(32, 0, 0, 0); err == nil {
		t.Fatal("memory below 64")
	}
	if err := ValidateResources(4096, 0, 0, 0); err == nil {
		t.Fatal("memory above 2048")
	}
	if err := ValidateResources(0, 50, 0, 0); err == nil {
		t.Fatal("cpu below 100")
	}
	if err := ValidateResources(0, 0, 8, 0); err == nil {
		t.Fatal("pids below 32")
	}
	if err := ValidateResources(0, 0, 0, 1); err == nil {
		t.Fatal("health timeout below 5")
	}
	if err := ValidateResources(0, 0, 0, 181); err == nil {
		t.Fatal("health timeout above 180")
	}
	got, err := ResolveHealthTimeout(0, 0)
	if err != nil || got.Seconds() != 45 {
		t.Fatalf("default %s %v", got, err)
	}
	got, err = ResolveHealthTimeout(90, 0)
	if err != nil || got.Seconds() != 90 {
		t.Fatalf("override %s %v", got, err)
	}
}
