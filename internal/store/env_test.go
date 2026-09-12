package store

import "testing"

func TestValidateEnvKeyRejectsReserved(t *testing.T) {
	cases := []string{"", "PORT", "port", "HOST", "FORGE_SECRET", "1ABC", "HAS-DASH", "HAS SPACE"}
	for _, key := range cases {
		if err := ValidateEnvKey(key); err == nil {
			t.Fatalf("expected error for %q", key)
		}
	}
}

func TestValidateEnvKeyAcceptsIdentifier(t *testing.T) {
	if err := ValidateEnvKey("DATABASE_URL"); err != nil {
		t.Fatal(err)
	}
}

func TestValidateEnvValueRejectsControl(t *testing.T) {
	if err := ValidateEnvValue("ok\nline"); err == nil {
		t.Fatal("expected error")
	}
	if err := ValidateEnvValue("plain"); err != nil {
		t.Fatal(err)
	}
}
