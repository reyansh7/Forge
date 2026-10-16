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

func TestRenderCaddyfilePreviewHost(t *testing.T) {
	prodID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	prevID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	raw := string(RenderCaddyfile("host.docker.internal", []store.Deployment{
		{ID: prodID, HostPort: 49152, LocalHost: "hello", Environment: store.EnvProduction},
		{ID: prevID, HostPort: 49153, LocalHost: "hello", Environment: store.EnvPreview},
	}))
	if !strings.Contains(raw, `header_regexp host (?i)^hello\.localhost(?::\d+)?$`) {
		t.Fatalf("missing production host: %s", raw)
	}
	if !strings.Contains(raw, `header_regexp host (?i)^hello-preview\.localhost(?::\d+)?$`) {
		t.Fatalf("missing preview host: %s", raw)
	}
}

func TestRenderCaddyfileCustomDomainACME(t *testing.T) {
	id := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	raw := string(RenderCaddyfile("host.docker.internal", []store.Deployment{
		{ID: id, ApplicationID: "bbbbbbbb-bbbb-4000-8000-000000000001", HostPort: 49152, LocalHost: "hello", Environment: store.EnvProduction},
	}, []store.ApplicationDomain{{
		ApplicationID: "bbbbbbbb-bbbb-4000-8000-000000000001",
		Hostname:      "app.example.com",
		Environment:   store.EnvProduction,
		Status:        store.DomainActive,
	}}))
	if !strings.Contains(raw, "auto_https off") || !strings.Contains(raw, "tls internal") {
		t.Fatalf("localhost TLS contract changed: %s", raw)
	}
	if !strings.Contains(raw, `header_regexp host (?i)^hello\.localhost(?::\d+)?$`) {
		t.Fatalf(".localhost route must remain: %s", raw)
	}
	if !strings.Contains(raw, "https://app.example.com") || !strings.Contains(raw, "issuer acme") {
		t.Fatalf("missing ACME site: %s", raw)
	}
	if !strings.Contains(raw, "http://app.example.com") {
		t.Fatalf("missing HTTP site for HTTP-01: %s", raw)
	}
	if !strings.Contains(raw, "host.docker.internal:49152") {
		t.Fatalf("missing custom-domain upstream: %s", raw)
	}
}

func TestRenderCaddyfileCustomDomainSkipsPendingAndWrongEnv(t *testing.T) {
	prodID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee1"
	prevID := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeee2"
	app := "bbbbbbbb-bbbb-4000-8000-000000000001"
	other := "cccccccc-cccc-4000-8000-000000000002"
	raw := string(RenderCaddyfile("host.docker.internal", []store.Deployment{
		{ID: prodID, ApplicationID: app, HostPort: 49152, Environment: store.EnvProduction},
		{ID: prevID, ApplicationID: app, HostPort: 49153, Environment: store.EnvPreview},
	}, []store.ApplicationDomain{
		{ApplicationID: app, Hostname: "pending.example.com", Environment: store.EnvProduction, Status: store.DomainPending},
		{ApplicationID: app, Hostname: "preview.example.com", Environment: store.EnvPreview, Status: store.DomainActive},
		{ApplicationID: other, Hostname: "stolen.example.com", Environment: store.EnvProduction, Status: store.DomainActive},
		{ApplicationID: app, Hostname: "not a host", Environment: store.EnvProduction, Status: store.DomainActive},
	}))
	if strings.Contains(raw, "pending.example.com") {
		t.Fatal("pending must not be routed")
	}
	if strings.Contains(raw, "stolen.example.com") {
		t.Fatal("another application's domain must not attach")
	}
	if strings.Contains(raw, "not a host") {
		t.Fatal("invalid hostname must not be interpolated")
	}
	if !strings.Contains(raw, "https://preview.example.com") {
		t.Fatalf("missing preview domain: %s", raw)
	}
	httpsIdx := strings.Index(raw, "https://preview.example.com")
	chunk := raw[httpsIdx:]
	if !strings.Contains(chunk, "host.docker.internal:49153") {
		t.Fatalf("preview domain must use preview upstream: %s", chunk)
	}
	if strings.Contains(chunk, ":49152") {
		t.Fatal("preview domain used production port")
	}
}

func TestPlatformHostsStayOffUntilZoneIsSet(t *testing.T) {
	domains := []store.ApplicationDomain{
		{Hostname: "mine.example.com", Origin: store.DomainOriginCustom, Status: store.DomainActive, ApplicationID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Environment: store.EnvProduction},
		{Hostname: "app.forge.com", Origin: store.DomainOriginPlatform, Status: store.DomainActive, ApplicationID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", Environment: store.EnvProduction},
	}
	filtered := routableDomains(domains, "")
	raw := string(RenderCaddyfile("host.docker.internal", []store.Deployment{
		{ID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", ApplicationID: "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", HostPort: 49152, Environment: store.EnvProduction},
	}, filtered))
	if strings.Contains(raw, "app.forge.com") {
		t.Fatal("platform host routed without FORGE_APPS_DOMAIN")
	}
	if !strings.Contains(raw, "mine.example.com") {
		t.Fatal("custom domain dropped")
	}
	if controlPlaneSite("", "console.forge.com", "host.docker.internal:3000") != "" {
		t.Fatal("control plane site without a zone")
	}
	site := controlPlaneSite("forge.com", "console.forge.com", "host.docker.internal:3000")
	if !strings.Contains(site, "https://console.forge.com") || !strings.Contains(site, "issuer acme") {
		t.Fatal(site)
	}
}
