package detect

import (
	"regexp"
	"strconv"
	"strings"
)

// listenScan is a read-only guess of how a small JS/TS/Python file
// binds its HTTP server. It never executes the file.
//
// PaaS health failures are often "we published 8080, the process
// listened on 5000" or "it bound 127.0.0.1 inside the container".
// Scanning a short allowlist of entry files lets Forge map the real
// port or explain the mismatch instead of waiting 45s.
type listenScan struct {
	Port       int
	PortSource string
	HonorsPORT bool
	Localhost  bool
	File       string
}

var (
	listenLiteral   = regexp.MustCompile(`(?i)\.listen\(\s*(\d{2,5})\b`)
	portAssign      = regexp.MustCompile(`(?im)^\s*(?:const|let|var)\s+port\s*=\s*(\d{2,5})\s*;?\s*$`)
	envPortFallback = regexp.MustCompile(`(?i)process\.env\.PORT|os\.environ\.get\(\s*["']PORT["']|os\.getenv\(\s*["']PORT["']`)
	localhostBind   = regexp.MustCompile(`(?i)listen\([^)]*(127\.0\.0\.1|localhost)|host\s*[:=]\s*['"]?(127\.0\.0\.1|localhost)`)
)

// entryCandidates are conventional HTTP entry files. Recursing the
// tree would pick up tests and client bundles and is a host-side DoS
// if a repo is huge. These names are the ones tutorials actually use.
var entryCandidates = []string{
	"server.js", "index.js", "app.js", "main.js",
	"server.ts", "index.ts", "app.ts", "main.ts",
	"src/index.js", "src/server.js", "src/app.js", "src/main.js",
	"src/index.ts", "src/server.ts", "src/app.ts", "src/main.ts",
	"app.py", "main.py", "server.py", "wsgi.py",
}

func scanListen(idx fileIndex) listenScan {
	var out listenScan
	for _, rel := range entryCandidates {
		raw, err := readEntry(idx, rel)
		if err != nil || len(raw) == 0 {
			continue
		}
		body := string(raw)
		if envPortFallback.MatchString(body) {
			out.HonorsPORT = true
		}
		if localhostBind.MatchString(body) {
			out.Localhost = true
			out.File = rel
		}
		if m := listenLiteral.FindStringSubmatch(body); len(m) == 2 {
			if p, ok := atoiPort(m[1]); ok {
				out.Port, out.PortSource, out.File = p, "source_listen", rel
				return out
			}
		}
		if m := portAssign.FindStringSubmatch(body); len(m) == 2 {
			if p, ok := atoiPort(m[1]); ok {
				out.Port, out.PortSource, out.File = p, "source_listen", rel
				// Keep scanning for a more specific listen() in a later file.
			}
		}
	}
	return out
}

func readEntry(idx fileIndex, rel string) ([]byte, error) {
	if strings.Contains(rel, "/") {
		return idx.readRel(rel)
	}
	if !idx.has(rel) {
		return nil, errNotEntry
	}
	return idx.read(rel)
}

type notEntry struct{}

func (notEntry) Error() string { return "not an entry file" }

var errNotEntry notEntry

func atoiPort(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}

// getRouteLiteral finds Express/Fastify/Koa-style GET mounts with a
// constant path. Parameterized routes (`/users/:id`) are skipped —
// those often 404 without a real id and would make health worse.
var getRouteLiteral = regexp.MustCompile(`(?i)(?:app|router|fastify|server)\.get\(\s*['"](/[^'":*?]+)['"]`)

// scanGETPaths is a read-only guess of HTTP GET routes in entry files.
//
// Tutorial Express apps often listen on a port but only mount
// `/api/...`. Forge's default health is GET /. That returns 404, the
// worker then Stop/rm's the container, and Docker Desktop looks empty.
// Inferring a real GET path is not "mark 404 as healthy" — it probes
// a route the source actually declared.
func scanGETPaths(idx fileIndex) []string {
	seen := map[string]bool{"/": true}
	var out []string
	for _, rel := range entryCandidates {
		raw, err := readEntry(idx, rel)
		if err != nil || len(raw) == 0 {
			continue
		}
		for _, m := range getRouteLiteral.FindAllStringSubmatch(string(raw), 8) {
			if len(m) != 2 {
				continue
			}
			p := m[1]
			if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, " \r\n\x00") {
				continue
			}
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
			if len(out) >= 5 {
				return out
			}
		}
	}
	return out
}

func applyListenScan(res Result, scan listenScan) Result {
	if scan.Localhost {
		res.BindHint = "127.0.0.1"
	} else {
		res.BindHint = "0.0.0.0"
	}
	// Apps that read process.env.PORT already honor Forge's injection.
	// A hardcoded listen(5000) does not — map that port instead of
	// publishing 8080 and waiting for a health timeout.
	if scan.HonorsPORT {
		return res
	}
	if scan.Port >= 1 && scan.Port <= 65535 && res.PortSource != "forge_json" && res.PortSource != "dockerfile_expose" {
		return withPort(res, scan.Port, scan.PortSource)
	}
	return res
}
