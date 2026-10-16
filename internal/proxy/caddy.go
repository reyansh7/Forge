// Package proxy updates the Phase 0 Caddy reverse proxy.
//
// Caddy routes traffic; it does not orchestrate containers. The worker
// decides which deployments are LIVE, then POSTs a generated Caddyfile
// to the loopback admin API.
package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/reyansh7/Forge/internal/store"
)

// Router applies the current set of live upstreams.
type Router interface {
	Apply(ctx context.Context, live []store.Deployment) error
}

// Caddy talks to POST {admin}/load with Content-Type: text/caddyfile.
//
// Admin must be loopback-only in Compose. A reachable admin API is
// equivalent to "rewrite every route" — do not publish :2019 on the LAN.
type Caddy struct {
	AdminURL     string
	UpstreamHost string
	Client       *http.Client
	// Domains is optional. When set, Apply includes verified custom
	// hostnames so Caddy can obtain ACME certs. Tests leave it nil.
	Domains DomainSource
	// AppsDomain enables routing of origin=platform rows. Empty skips
	// them so a local worker does not ask Let's Encrypt for names that
	// do not resolve here.
	AppsDomain string
	// ControlHost is the dashboard site (only emitted with AppsDomain).
	// ControlUpstream is host:port inside the Caddy network, usually
	// host.docker.internal:3000. It is not an application container.
	ControlHost     string
	ControlUpstream string
}

// DomainSource is the Caddy → application_domains boundary.
type DomainSource interface {
	ListActiveDomains(ctx context.Context) ([]store.ApplicationDomain, error)
}

func (c Caddy) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 5 * time.Second}
}

func (c Caddy) Apply(ctx context.Context, live []store.Deployment) error {
	var domains []store.ApplicationDomain
	if c.Domains != nil {
		var err error
		domains, err = c.Domains.ListActiveDomains(ctx)
		if err != nil {
			return fmt.Errorf("caddy domains: %w", err)
		}
	}
	body := RenderCaddyfile(c.UpstreamHost, live, routableDomains(domains, c.AppsDomain))
	if site := controlPlaneSite(c.AppsDomain, c.ControlHost, c.ControlUpstream); site != "" {
		body = append(body, []byte(site)...)
	}
	admin, err := url.JoinPath(strings.TrimRight(c.AdminURL, "/"), "load")
	if err != nil {
		return fmt.Errorf("caddy admin url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, admin, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/caddyfile")

	resp, err := c.client().Do(req)
	if err != nil {
		return fmt.Errorf("caddy load: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("caddy load: status %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	return nil
}

// RenderCaddyfile is deterministic so tests can lock the contract.
// Deployment IDs must already be UUIDs; ports must be in range.
//
// .localhost and /d/{id}/ stay on the catch-all :80 / :443 listeners
// with `tls internal` and `auto_https off` so loopback never hits
// Let's Encrypt. Verified custom domains are separate site blocks
// with `tls { issuer acme }` so Caddy obtains and renews certificates
// for those hostnames only. Pending domains are not passed in.
func RenderCaddyfile(upstreamHost string, live []store.Deployment, domains ...[]store.ApplicationDomain) []byte {
	if strings.TrimSpace(upstreamHost) == "" {
		upstreamHost = "host.docker.internal"
	}
	var extra []store.ApplicationDomain
	if len(domains) > 0 {
		extra = domains[0]
	}
	var b bytes.Buffer
	// auto_https off: ACME is per named site below, never for
	// :80/:443 catch-alls or *.localhost.
	b.WriteString("{\n\tadmin 0.0.0.0:2019\n\tauto_https off\n}\n\n")
	b.WriteString(":80 {\n")
	writeAppRoutes(&b, upstreamHost, live)
	b.WriteString("\trespond \"Forge proxy: no live deployment at this path\" 404\n")
	b.WriteString("}\n\n")
	b.WriteString(":443 {\n\ttls internal\n")
	writeAppRoutes(&b, upstreamHost, live)
	b.WriteString("\trespond \"Forge proxy: no live deployment at this path\" 404\n")
	b.WriteString("}\n")
	writeCustomDomainSites(&b, upstreamHost, live, extra)
	return b.Bytes()
}

// routableDomains drops platform hostnames unless the operator set
// the apps zone. Custom domains are always routed: they were proved
// with DNS TXT and already expect ACME.
func routableDomains(domains []store.ApplicationDomain, appsDomain string) []store.ApplicationDomain {
	if strings.TrimSpace(appsDomain) != "" {
		return domains
	}
	out := make([]store.ApplicationDomain, 0, len(domains))
	for _, d := range domains {
		if d.Origin == store.DomainOriginPlatform {
			continue
		}
		out = append(out, d)
	}
	return out
}

// controlPlaneSite proxies the dashboard hostname to Next.js so GitHub
// can POST webhooks at /forge-api without publishing the API port.
// Both the zone and the host must be set; otherwise local Caddyfiles
// stay exactly the catch-all they were.
func controlPlaneSite(appsDomain, host, upstream string) string {
	if strings.TrimSpace(appsDomain) == "" || strings.TrimSpace(host) == "" || strings.TrimSpace(upstream) == "" {
		return ""
	}
	if _, err := store.ValidateHostname(host); err != nil {
		return ""
	}
	if strings.ContainsAny(upstream, " \t\n;/{}") {
		return ""
	}
	return "\nhttp://" + host + " {\n\treverse_proxy " + upstream + "\n}\n\nhttps://" + host + " {\n\ttls {\n\t\tissuer acme\n\t}\n\treverse_proxy " + upstream + "\n}\n"
}

func writeCustomDomainSites(b *bytes.Buffer, upstreamHost string, live []store.Deployment, domains []store.ApplicationDomain) {
	if len(domains) == 0 {
		return
	}
	index := map[string]store.Deployment{}
	for _, d := range live {
		if d.HostPort < 1 || d.HostPort > 65535 {
			continue
		}
		if _, err := store.ParseUUID(d.ID); err != nil {
			continue
		}
		env, err := store.ParseEnvironment(d.Environment)
		if err != nil {
			continue
		}
		index[d.ApplicationID+"|"+env] = d
	}
	for _, dom := range domains {
		host, err := store.ValidateHostname(dom.Hostname)
		if err != nil {
			continue
		}
		if dom.Status != "" && dom.Status != store.DomainActive {
			continue
		}
		env, err := store.ParseEnvironment(dom.Environment)
		if err != nil {
			continue
		}
		d, ok := index[dom.ApplicationID+"|"+env]
		if !ok {
			continue
		}
		upstream := net.JoinHostPort(deploymentUpstreamHost(upstreamHost, d), strconv.Itoa(d.HostPort))
		// HTTP site is more specific than :80 so ACME HTTP-01 can
		// answer on this hostname. HTTPS uses issuer acme (Let's
		// Encrypt by default). Do not use on_demand: that would mint
		// a cert for any Host header pointed at this process.
		b.WriteString("\nhttp://")
		b.WriteString(host)
		b.WriteString(" {\n\treverse_proxy ")
		b.WriteString(upstream)
		b.WriteString("\n}\n\nhttps://")
		b.WriteString(host)
		b.WriteString(" {\n\ttls {\n\t\tissuer acme\n\t}\n\treverse_proxy ")
		b.WriteString(upstream)
		b.WriteString("\n}\n")
	}
}

func writeAppRoutes(b *bytes.Buffer, upstreamHost string, live []store.Deployment) {
	for _, d := range live {
		if d.HostPort < 1 || d.HostPort > 65535 {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(d.ID))
		if _, err := store.ParseUUID(id); err != nil {
			continue
		}
		upstream := net.JoinHostPort(deploymentUpstreamHost(upstreamHost, d), strconv.Itoa(d.HostPort))
		tag := strings.ReplaceAll(id, "-", "")
		// Optional Host route: http://{slug}.localhost:9080/ → same
		// container as /d/{id}/. This is local DX, not public DNS.
		// SPAs that request /assets/* from the origin root work here
		// because the Host is mounted at /.
		//
		// ListLiveDeployments joins applications.local_host. A
		// worker-assigned slug is stored on public_url, not always
		// on the application row — recover it so the next Caddy
		// reload does not drop the Host matcher.
		slug := localHostSlug(d.LocalHost)
		if slug == "" {
			slug = slugFromPublicURL(d.PublicURL)
		}
		// Preview Hosts are {slug}-preview. Production keeps {slug}.
		// Do not recover preview from PublicURL then suffix again —
		// RouteLocalHost is idempotent for an already-suffixed slug.
		if env, err := store.ParseEnvironment(d.Environment); err == nil && env == store.EnvPreview {
			if prod := localHostSlug(d.LocalHost); prod != "" {
				slug = store.PreviewLocalHost(prod)
			} else if slug != "" && !strings.HasSuffix(slug, "-preview") {
				slug = store.PreviewLocalHost(slug)
			}
		}
		if slug != "" {
			b.WriteString("\t@host_")
			b.WriteString(tag)
			b.WriteString(" header_regexp host (?i)^")
			b.WriteString(slug)
			b.WriteString(`\.localhost(?::\d+)?$`)
			b.WriteString("\n\thandle @host_")
			b.WriteString(tag)
			b.WriteString(" {\n\t\treverse_proxy ")
			b.WriteString(upstream)
			b.WriteString("\n\t}\n")
		}
		// /d/{id} without a slash would make relative ./assets resolve
		// to /d/assets (wrong). Force the trailing slash.
		b.WriteString("\tredir /d/")
		b.WriteString(id)
		b.WriteString(" /d/")
		b.WriteString(id)
		b.WriteString("/ 308\n")
		b.WriteString("\thandle_path /d/")
		b.WriteString(id)
		b.WriteString("/* {\n\t\treverse_proxy ")
		b.WriteString(upstream)
		b.WriteString("\n\t}\n")
		// Vite/CRA default builds request /index-HASH.js from the
		// origin root, not /d/{id}/assets/.... The HTML came from
		// /d/{id}/, so the browser sends that path as Referer. Route
		// those orphan origin-root GETs back to the same upstream.
		// This is not a new exposure: the app is already public on
		// its path and Host URLs. Referer is not an authorization
		// check — it only picks which live deployment receives a
		// request that would otherwise 404 at the proxy.
		b.WriteString("\t@ref_")
		b.WriteString(tag)
		b.WriteString(" {\n\t\theader_regexp Referer (?i)/d/")
		b.WriteString(id)
		b.WriteString("(?:/|$)\n\t\tnot path /d/*\n\t}\n")
		b.WriteString("\thandle @ref_")
		b.WriteString(tag)
		b.WriteString(" {\n\t\treverse_proxy ")
		b.WriteString(upstream)
		b.WriteString("\n\t}\n")
	}
}

// deploymentUpstreamHost prefers the node's advertise_host when the
// worker published it. Empty means this process shares a Docker host
// with Caddy (FORGE_CADDY_UPSTREAM_HOST / host.docker.internal).
func deploymentUpstreamHost(fallback string, d store.Deployment) string {
	host, err := store.ValidateAdvertiseHost(d.NodeAdvertiseHost)
	if err != nil || host == "" {
		return fallback
	}
	return host
}

func slugFromPublicURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	const suffix = ".localhost"
	if !strings.HasSuffix(host, suffix) {
		return ""
	}
	return localHostSlug(strings.TrimSuffix(host, suffix))
}

func localHostSlug(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return ""
		}
	}
	if s[0] == '-' || s[len(s)-1] == '-' {
		return ""
	}
	return s
}

// PublicURL is the path-based address on the published proxy port.
func PublicURL(base, deploymentID string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	return base + "/d/" + deploymentID + "/"
}

// HostPublicURL is http://{slug}.localhost:{port}/ when a slug is set.
//
// Browsers resolve *.localhost to loopback. The published Compose port
// is taken from ProxyBase (Phase 0 default 9080). Not a public domain.
func HostPublicURL(base, slug string) string {
	slug = localHostSlug(slug)
	if slug == "" {
		return ""
	}
	port := "9080"
	if u, err := url.Parse(strings.TrimSpace(base)); err == nil && u.Port() != "" {
		port = u.Port()
	}
	return "http://" + slug + ".localhost:" + port + "/"
}
