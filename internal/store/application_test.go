package store

import "testing"

func TestValidateApplicationInput(t *testing.T) {
	in, err := ValidateApplicationInput("  api  ", SampleHelloURL)
	if err != nil {
		t.Fatal(err)
	}
	if in.Name != "api" {
		t.Fatalf("name = %q", in.Name)
	}
	if _, err := ValidateApplicationInput("", SampleHelloURL); err == nil {
		t.Fatal("empty name")
	}
	if _, err := ValidateApplicationInput("api", "file:///etc/passwd"); err == nil {
		t.Fatal("file url")
	}
}
