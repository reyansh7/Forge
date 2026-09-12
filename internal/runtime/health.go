package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Probe is one go-live health attempt against a published loopback port.
//
// It is not "wait 45s and hope". Before each HTTP GET we re-check that
// the container is still running. A dead process becomes an immediate
// FAILED(deploying) with exit code and logs. An HTTP response that is
// not 2xx fails immediately with the status code — retrying a 404 for
// 45 seconds hides the real problem.
type Probe struct {
	Port       int
	Path       string
	Timeout    time.Duration
	ListenPort int
	PortSource string
	BindHint   string
	Container  string
	Inspect    func(context.Context) (ContainerState, error)
	TailLogs   func(context.Context) string
}

// ProbeHTTP runs the diagnostic health loop. Tests still use HealthGETPath
// when they only care about the HTTP side.
func ProbeHTTP(ctx context.Context, p Probe) error {
	if p.Port < 1 || p.Port > 65535 {
		return fmt.Errorf("health: invalid port")
	}
	path := strings.TrimSpace(p.Path)
	if path == "" {
		path = "/"
	}
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "://") || strings.ContainsAny(path, "\r\n\x00 ") {
		return fmt.Errorf("health: invalid path")
	}
	if p.Timeout <= 0 {
		p.Timeout = 45 * time.Second
	}
	deadline := time.Now().Add(p.Timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d%s", p.Port, path)
	var lastNet error
	sawRefused := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p.Inspect != nil {
			st, err := p.Inspect(ctx)
			if err == nil && st.Exists && !st.Running {
				return exitedError(p, st)
			}
		}
		if time.Now().After(deadline) {
			return timeoutError(p, url, lastNet, sawRefused)
		}
		reqCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		req, err := newGET(reqCtx, url)
		if err != nil {
			cancel()
			return err
		}
		resp, err := httpClient.Do(req)
		if err != nil {
			cancel()
			lastNet = err
			if isRefused(err) {
				sawRefused = true
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(400 * time.Millisecond):
			}
			continue
		}
		_ = resp.Body.Close()
		cancel()
		if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
			return nil
		}
		return httpStatusError(p, url, resp.StatusCode)
	}
}

func exitedError(p Probe, st ContainerState) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Container %s is not running.\n\n", displayName(p, st))
	fmt.Fprintf(&b, "Status: %s\nExit code: %d\n", orDash(st.Status), st.ExitCode)
	if st.Error != "" {
		fmt.Fprintf(&b, "Docker: %s\n", st.Error)
	}
	if logs := tailSnippet(p); logs != "" {
		b.WriteString("\nLogs:\n")
		b.WriteString(logs)
		b.WriteByte('\n')
	}
	b.WriteString("\nThe process exited before it became healthy. This is not a health-check timeout.")
	return errors.New(b.String())
}

func httpStatusError(p Probe, url string, code int) error {
	var b strings.Builder
	fmt.Fprintf(&b, "GET %s returned HTTP %d.\n\n", url, code)
	switch {
	case code == 404:
		b.WriteString("The HTTP server is reachable, but this path is not a success response.\n")
		b.WriteString("Set the application health path to a route that returns 2xx, or add a GET / handler.\n")
		b.WriteString("A missing local-host slug is not the cause. After this failure Forge removes the container so Docker Desktop does not keep a failed attempt.\n")
	case code >= 300 && code <= 399:
		b.WriteString("The server redirected. Health checks do not follow redirects; point health at the final 2xx path.\n")
	default:
		b.WriteString("The server responded but not with 2xx. Fix the application or configure a different health path.\n")
	}
	if p.ListenPort > 0 {
		fmt.Fprintf(&b, "\nApplication port: %d (%s)\n", p.ListenPort, orDash(p.PortSource))
	}
	return errors.New(b.String())
}

func timeoutError(p Probe, url string, lastNet error, refused bool) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Nothing accepted HTTP on %s within the health timeout.\n\n", url)
	if refused {
		fmt.Fprintf(&b, "The published port refused connections. ")
		if p.ListenPort > 0 {
			fmt.Fprintf(&b, "Forge mapped container port %d (source: %s). ", p.ListenPort, orDash(p.PortSource))
		}
		b.WriteString("If the process listens on a different port, set forge.json {\"port\": N} or listen on $PORT.\n")
	} else if lastNet != nil {
		fmt.Fprintf(&b, "Last network error: %s\n", sanitizeNet(lastNet))
	}
	if p.BindHint == "127.0.0.1" {
		b.WriteString("\nDetection saw a localhost/127.0.0.1 bind in the source. Bind 0.0.0.0 so the published port can reach the process.\n")
	}
	if logs := tailSnippet(p); logs != "" {
		b.WriteString("\nLogs:\n")
		b.WriteString(logs)
		b.WriteByte('\n')
	}
	return errors.New(b.String())
}

func displayName(p Probe, st ContainerState) string {
	if p.Container != "" {
		return p.Container
	}
	if st.Name != "" {
		return st.Name
	}
	return "workload"
}

func tailSnippet(p Probe) string {
	if p.TailLogs == nil {
		return ""
	}
	s := strings.TrimSpace(p.TailLogs(context.Background()))
	if s == "" {
		return ""
	}
	if len(s) > 1500 {
		s = s[len(s)-1500:]
	}
	return s
}

func isRefused(err error) bool {
	if err == nil {
		return false
	}
	var op *net.OpError
	if errors.As(err, &op) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connection refused") || strings.Contains(s, "connectex")
}

func sanitizeNet(err error) string {
	s := err.Error()
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func drainBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 2048))
	_ = resp.Body.Close()
}
