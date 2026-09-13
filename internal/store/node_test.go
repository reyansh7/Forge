package store

import "testing"

func TestValidateNodeName(t *testing.T) {
	ok, err := ValidateNodeName("  Lab-2  ")
	if err != nil {
		t.Fatal(err)
	}
	if ok != "lab-2" {
		t.Fatalf("got %q", ok)
	}
	for _, raw := range []string{"", "LOCAL_HOST", "-x", "x-", "has.dot", "UPPER"} {
		if _, err := ValidateNodeName(raw); err == nil && raw != "UPPER" {
			t.Fatalf("expected error for %q", raw)
		}
	}
	if got, err := ValidateNodeName("UPPER"); err != nil || got != "upper" {
		t.Fatalf("UPPER: %q %v", got, err)
	}
}

func TestValidateAdvertiseHost(t *testing.T) {
	if got, err := ValidateAdvertiseHost(""); err != nil || got != "" {
		t.Fatalf("empty: %q %v", got, err)
	}
	if got, err := ValidateAdvertiseHost("192.168.1.10"); err != nil || got != "192.168.1.10" {
		t.Fatalf("ipv4: %q %v", got, err)
	}
	if got, err := ValidateAdvertiseHost("worker.lab"); err != nil || got != "worker.lab" {
		t.Fatalf("name: %q %v", got, err)
	}
	if _, err := ValidateAdvertiseHost("http://evil"); err == nil {
		t.Fatal("scheme")
	}
	if _, err := ValidateAdvertiseHost("192.168.1.10:8080"); err == nil {
		t.Fatal("port")
	}
	if _, err := ValidateAdvertiseHost("0.0.0.0"); err == nil {
		t.Fatal("unspecified")
	}
	if got, err := ValidateAdvertiseHost("127.0.0.1"); err != nil || got != "127.0.0.1" {
		t.Fatalf("loopback lab: %q %v", got, err)
	}
}
