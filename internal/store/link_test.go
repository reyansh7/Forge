package store

import "testing"

func TestHostURLForSlug(t *testing.T) {
	got := HostURLForSlug("http://127.0.0.1:9080/", "api")
	if got != "http://api.localhost:9080/" {
		t.Fatalf("got %q", got)
	}
	if HostURLForSlug("http://127.0.0.1:9080/", "") != "" {
		t.Fatal("empty slug")
	}
}

func TestServiceHostURLUsesSuggestedSlug(t *testing.T) {
	app := Application{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Name: "backend"}
	got := ServiceHostURL("http://127.0.0.1:9080", app)
	want := "http://backend.localhost:9080/"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestServiceHostURLPrefersLocalHost(t *testing.T) {
	app := Application{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Name: "backend", LocalHost: "api"}
	got := ServiceHostURL("http://127.0.0.1:9080", app)
	if got != "http://api.localhost:9080/" {
		t.Fatalf("got %q", got)
	}
}
