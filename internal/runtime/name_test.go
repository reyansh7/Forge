package runtime

import "testing"

func TestSlugifyName(t *testing.T) {
	cases := map[string]string{
		"My Portfolio":  "my-portfolio",
		"React/API":     "react-api",
		"  Storefront ": "storefront",
		"":              "app",
		"!!!":           "app",
		"9lives":        "app-9lives",
	}
	for in, want := range cases {
		if got := SlugifyName(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestWorkloadNameDeterministic(t *testing.T) {
	id := "a7f3c21d-1111-2222-3333-444444444444"
	got := WorkloadName("My Portfolio", id)
	if got != "my-portfolio-a7f3" {
		t.Fatalf("got %q", got)
	}
	if WorkloadName("My Portfolio", id) != got {
		t.Fatal("not deterministic")
	}
}

func TestResolveContainerNameFallback(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	if got := ResolveContainerName(id, ""); got != LegacyContainerName(id) {
		t.Fatalf("empty stored: %q", got)
	}
	if got := ResolveContainerName(id, "my-portfolio-a7f3"); got != "my-portfolio-a7f3" {
		t.Fatalf("stored: %q", got)
	}
}

func TestShortID(t *testing.T) {
	if ShortID("a7f3c21d-1111-2222-3333-444444444444") != "a7f3" {
		t.Fatal(ShortID("a7f3c21d-1111-2222-3333-444444444444"))
	}
}
