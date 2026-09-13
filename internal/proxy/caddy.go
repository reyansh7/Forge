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
}

func (c Caddy) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 5 * time.Second}
}

func (c Caddy) Apply(ctx context.Context, live []store.Deployment) error {
	body := RenderCaddyfile(c.UpstreamHost, live)
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
func RenderCaddyfile(upstreamHost string, live []store.Deployment) []byte {
	if strings.TrimSpace(upstreamHost) == "" {
		upstreamHost = "host.docker.internal"
	}
	var b bytes.Buffer
	// auto_https off: we do not want ACME on loopback. :443 uses
	// Caddy's local CA (`tls internal`). Public Let's Encrypt waits
	// for a real hostname (Phase 7).
	b.WriteString("{\n\tadmin 0.0.0.0:2019\n\tauto_https off\n}\n\n")
	b.WriteString(":80 {\n")
	writeAppRoutes(&b, upstreamHost, live)
	b.WriteString("\trespond \"Forge proxy: no live deployment at this path\" 404\n")
	b.WriteString("}\n\n")
	b.WriteString(":443 {\n\ttls internal\n")
	writeAppRoutes(&b, upstreamHost, live)
	b.WriteString("\trespond \"Forge proxy: no live deployment at this path\" 404\n")
	b.WriteString("}\n")
	return b.Bytes()
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
