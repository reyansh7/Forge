// Package detect classifies a fetched source tree without executing it.
//
// Detection is a pack registry (buildpack-style), not a closed language
// enum. Each pack answers: do I match this tree, and if so what image
// recipe should Forge write? A user Dockerfile is the first pack: it
// works for any language, including ones we have no native recipe for.
//
// The worker never runs language toolchains on the host. Packs only
// read file names and small manifests, then emit a Forge-owned
// Dockerfile. `docker build` is the isolation boundary.
//
// Kind constants (dockerfile, node, go, …) are pack IDs persisted as
// runtime_kind. Adding an ecosystem means a new Pack, not another
// branch in Tree.
package detect

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// Kind is a pack ID stored on deployments.runtime_kind.
//
// These are identifiers, not the extension mechanism. Tree walks the
// pack table. Unknown IDs must not be invented from random files.
type Kind string

const (
	KindDockerfile Kind = "dockerfile"
	KindGo         Kind = "go"
	KindNode       Kind = "node"
	KindPython     Kind = "python"
	KindJava       Kind = "java"
	KindRust       Kind = "rust"
	KindRuby       Kind = "ruby"
	KindPHP        Kind = "php"
	KindDotnet     Kind = "dotnet"
	KindCC         Kind = "cc"
	KindStatic     Kind = "static"
)

// DefaultPort is what Forge-written recipes listen on. Generated
// Dockerfiles set ENV PORT and EXPOSE to this value so docker run
// mapping is not a silent guess.
const DefaultPort = 8080

// Runtime is how the built artifact is served. Kind is the ecosystem
// pack; Runtime is the process model. A Vite app is KindNode +
// RuntimeStatic: npm run build is not an HTTP server.
const (
	RuntimeServer = "server"
	RuntimeStatic = "static"
	RuntimeImage  = "image"
)

// Result is detection + build strategy + runtime strategy for one root.
//
// Kind stays the persisted runtime_kind (backward compatible for
// dockerfile/node/go). Language/Framework are extra metadata. Port is
// the container listen port (not the host publish port). Runtime says
// whether Forge must provide a static file server after build.
type Result struct {
	Kind       Kind
	Language   string
	Framework  string
	Runtime    string
	Port       int
	PortSource string
	HealthPath string
	OutputDir  string
	BindHint   string
	Source     DetectionSource
	Files      []string
	// GeneratedDockerfile is a Forge-owned recipe. Empty means the
	// repository already has a usable Dockerfile.
	GeneratedDockerfile string
	// ExtraFiles are Forge-owned sidecar files written next to the
	// generated Dockerfile (nginx config). Keys are basenames only.
	ExtraFiles map[string]string
}

// DetectionSource explains why a pack won. Used in tests and errors.
type DetectionSource struct {
	Pack     string
	Manifest string
	Reason   string
}

// ListenPort is the container-side port for docker run -p.
//
// 0 is treated as DefaultPort so rollback (no re-detect) and older
// images keep working.
func (r Result) ListenPort() int {
	if r.Port < 1 || r.Port > 65535 {
		return DefaultPort
	}
	return r.Port
}

func (r Result) RuntimeOrDefault() string {
	if r.Runtime == "" {
		return RuntimeServer
	}
	return r.Runtime
}

func (r Result) HealthOrDefault() string {
	if strings.TrimSpace(r.HealthPath) == "" {
		return "/"
	}
	return r.HealthPath
}

// LogLines is the detect stage written into build_log so the dashboard
// shows strategy, not only docker build output.
func (r Result) LogLines() string {
	var b strings.Builder
	b.WriteString("[detect] kind=")
	b.WriteString(string(r.Kind))
	if r.Language != "" {
		b.WriteString(" language=")
		b.WriteString(r.Language)
	}
	if r.Framework != "" {
		b.WriteString(" framework=")
		b.WriteString(r.Framework)
	}
	b.WriteString(" runtime=")
	b.WriteString(r.RuntimeOrDefault())
	b.WriteByte('\n')
	if r.OutputDir != "" {
		b.WriteString("[detect] output directory: ")
		b.WriteString(r.OutputDir)
		b.WriteByte('\n')
	}
	b.WriteString("[detect] application port: ")
	b.WriteString(fmt.Sprintf("%d", r.ListenPort()))
	b.WriteString(" (")
	b.WriteString(r.PortSource)
	b.WriteString(")\n")
	b.WriteString("[detect] health path: ")
	b.WriteString(r.HealthOrDefault())
	b.WriteByte('\n')
	if r.BindHint != "" {
		b.WriteString("[detect] bind hint: ")
		b.WriteString(r.BindHint)
		b.WriteByte('\n')
	}
	return b.String()
}

// Tree inspects dir. It must not exec scripts or follow repository hooks.
func Tree(dir string) (Result, error) {
	if dir == "" {
		return Result{}, fmt.Errorf("detect: empty directory")
	}
	info, err := os.Stat(dir)
	if err != nil {
		return Result{}, fmt.Errorf("detect: %w", err)
	}
	if !info.IsDir() {
		return Result{}, fmt.Errorf("detect: %s is not a directory", dir)
	}

	idx, err := indexRoot(dir)
	if err != nil {
		return Result{}, err
	}

	for _, p := range packs() {
		if !p.Match(idx) {
			continue
		}
		res, err := p.Analyze(idx)
		if err != nil {
			return Result{}, err
		}
		res.Files = idx.names()
		return res, nil
	}
	return Result{}, unknownStrategy(idx)
}

func unknownStrategy(idx fileIndex) error {
	var b strings.Builder
	b.WriteString("Unable to determine a deployment strategy.\n\nDetected files:\n")
	names := idx.relevantNames()
	if len(names) == 0 {
		b.WriteString("- (none)\n")
	} else {
		sort.Strings(names)
		limit := 32
		if len(names) < limit {
			limit = len(names)
		}
		for _, n := range names[:limit] {
			b.WriteString("- ")
			b.WriteString(n)
			b.WriteString("\n")
		}
	}
	if n := idx.assetCount(); n > 0 {
		b.WriteString("\nIgnored ")
		b.WriteString(fmt.Sprintf("%d", n))
		b.WriteString(" asset file(s) (images, video, fonts, archives). They are still copied into the image when a strategy exists.\n")
	}
	b.WriteString("\nNo supported build strategy or Dockerfile was found.\n\n")
	b.WriteString("Add a Dockerfile, or a forge.json with a \"start\" command, or a Procfile web process.")
	return fmt.Errorf("%s", b.String())
}
