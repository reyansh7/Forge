package proxy

import (
	"strings"
	"testing"

	"github.com/reyansh7/Forge/internal/store"
)

func TestRenderCaddyfileIncludesValidatedRoute(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	raw := string(RenderCaddyfile("host.docker.internal", []store.Deployment{
		{ID: id, HostPort: 49152},
	}))
	if !strings.Contains(raw, "handle_path /d/"+id+"/*") {
		t.Fatalf("missing handle: %s", raw)
	}
	if !strings.Contains(raw, "host.docker.internal:49152") {
		t.Fatalf("missing upstream: %s", raw)
	}
}

func TestRenderCaddyfileSkipsInvalidID(t *testing.T) {
	raw := string(RenderCaddyfile("host.docker.internal", []store.Deployment{
		{ID: "not-a-uuid", HostPort: 80},
	}))
	if strings.Contains(raw, "not-a-uuid") {
		t.Fatalf("must not interpolate untrusted id: %s", raw)
	}
}

func TestRenderCaddyfileIncludesHostMatcher(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	raw := string(RenderCaddyfile("host.docker.internal", []store.Deployment{
		{ID: id, HostPort: 49152, LocalHost: "hello"},
	}))
	if !strings.Contains(raw, `header_regexp host (?i)^hello\.localhost(?::\d+)?$`) {
		t.Fatalf("missing host matcher: %s", raw)
	}
	if strings.Contains(raw, "evil") {
		t.Fatal("unexpected")
	}
}

func TestRenderCaddyfileSkipsUnsafeSlug(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	raw := string(RenderCaddyfile("host.docker.internal", []store.Deployment{
		{ID: id, HostPort: 80, LocalHost: "foo.bar"},
	}))
	if strings.Contains(raw, "foo.bar") {
		t.Fatalf("must not interpolate unsafe slug: %s", raw)
	}
}

func TestHostPublicURL(t *testing.T) {
	got := HostPublicURL("http://127.0.0.1:9080/", "hello")
	if got != "http://hello.localhost:9080/" {
		t.Fatalf("got %q", got)
	}
	if HostPublicURL("http://127.0.0.1:9080/", "") != "" {
		t.Fatal("empty slug")
	}
}
