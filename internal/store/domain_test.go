package store

import "testing"

func TestValidateHostname(t *testing.T) {
	got, err := ValidateHostname("  App.Example.COM.  ")
	if err != nil || got != "app.example.com" {
		t.Fatalf("got %q err=%v", got, err)
	}
	for _, bad := range []string{
		"", "localhost", "hello.localhost", "foo.local",
		"192.168.1.1", "::1", "http://app.example.com",
		"app.example.com/path", "app.example.com:443",
		"*.example.com", "example", "-bad.example.com",
		"foo..com", "app_example.com", "C:\\evil",
	} {
		if _, err := ValidateHostname(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func TestDomainChallengeName(t *testing.T) {
	got := DomainChallengeName("app.example.com")
	if got != "_forge-challenge.app.example.com" {
		t.Fatalf("got %q", got)
	}
	if DomainChallengeName("localhost") != "" {
		t.Fatal("reserved hostname must not produce a challenge name")
	}
}

func TestPlatformHostname(t *testing.T) {
	got, err := PlatformHostname("My-App", "Forge.com")
	if err != nil || got != "my-app.forge.com" {
		t.Fatalf("got %q err=%v", got, err)
	}
	if PlatformURL("my-app", "forge.com") != "https://my-app.forge.com/" {
		t.Fatal(PlatformURL("my-app", "forge.com"))
	}
	for _, slug := range []string{"www", "forge", "api", "console", ""} {
		if _, err := PlatformHostname(slug, "forge.com"); err == nil {
			t.Fatalf("slug %q was accepted", slug)
		}
	}
	if _, err := PlatformHostname("my-app", "*.forge.com"); err == nil {
		t.Fatal("wildcard zone")
	}
	if !HostnameUnderZone("My-App.Forge.com.", "forge.com") {
		t.Fatal("subdomain")
	}
	if HostnameUnderZone("forge.com.evil.com", "forge.com") {
		t.Fatal("suffix lookalike")
	}
}
