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
	if !strings.Contains(raw, "redir /d/"+id+" /d/"+id+"/ 308") {
		t.Fatalf("missing trailing-slash redirect: %s", raw)
	}
	if !strings.Contains(raw, "header_regexp Referer (?i)/d/"+id+`(?:/|$)`) {
		t.Fatalf("missing referer fallback: %s", raw)
	}
	if !strings.Contains(raw, "host.docker.internal:49152") {
		t.Fatalf("missing upstream: %s", raw)
	}
	if !strings.Contains(raw, "tls internal") || !strings.Contains(raw, ":443") {
		t.Fatalf("missing app-facing TLS block: %s", raw)
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

func TestRenderCaddyfileRecoversSlugFromPublicURL(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	raw := string(RenderCaddyfile("host.docker.internal", []store.Deployment{
		{ID: id, HostPort: 49152, PublicURL: "http://my-portfolio.localhost:9080/"},
	}))
	if !strings.Contains(raw, `header_regexp host (?i)^my-portfolio\.localhost(?::\d+)?$`) {
		t.Fatalf("missing recovered host matcher: %s", raw)
	}
}

func TestRenderCaddyfileUsesNodeAdvertiseHost(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	raw := string(RenderCaddyfile("host.docker.internal", []store.Deployment{
		{ID: id, HostPort: 49152, NodeAdvertiseHost: "192.168.1.10"},
	}))
	if !strings.Contains(raw, "192.168.1.10:49152") {
		t.Fatalf("missing node upstream: %s", raw)
	}
	if strings.Count(raw, "host.docker.internal:49152") != 0 {
		t.Fatalf("must not use fallback when advertise_host is set: %s", raw)
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
