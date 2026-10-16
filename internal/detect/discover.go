package detect

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Service is one deployable (or explicitly skipped) unit inside a repository.
//
// Root is the directory Tree would classify, relative to the repo (`.`
// is the repository root). Package is set for a Go module that has
// several programs: `./cmd/api` is built from the module root, not by
// pretending cmd/api is its own module.
type Service struct {
	Name       string `json:"name"`
	Root       string `json:"root"`
	Package    string `json:"package,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Framework  string `json:"framework,omitempty"`
	Runtime    string `json:"runtime,omitempty"`
	Port       int    `json:"port,omitempty"`
	Deployable bool   `json:"deployable"`
	Detail     string `json:"detail"`
}

// Discover lists services in a repository without running user code.
//
// It looks at the root and one directory level. node_modules, vendor,
// VCS, and virtualenv folders are not services. A Go module with
// cmd/api and cmd/worker becomes two services. A module with no main
// package is reported as a library, not deployed.
func Discover(root string) ([]Service, error) {
	var out []Service
	if err := discoverAt(root, ".", &out); err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ent := range ents {
		if ent.IsDir() && !skipDiscoverDir(ent.Name()) {
			names = append(names, ent.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		// cmd/ under a root go.mod is that module's programs, already
		// emitted by discoverAt on ".".
		if name == "cmd" && fileExists(filepath.Join(root, "go.mod")) {
			continue
		}
		if err := discoverAt(root, name, &out); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Root == out[j].Root {
			return out[i].Package < out[j].Package
		}
		return out[i].Root < out[j].Root
	})
	return out, nil
}

func discoverAt(repo, rel string, out *[]Service) error {
	dir := repo
	if rel != "." {
		dir = filepath.Join(repo, rel)
	}
	idx, err := indexRoot(dir)
	if err != nil {
		return err
	}
	if !serviceMarker(idx) {
		return nil
	}
	if idx.has("go.mod") {
		*out = append(*out, goServices(rel, idx)...)
		return nil
	}
	got, err := Tree(dir)
	if err != nil {
		*out = append(*out, Service{
			Name:       serviceName(rel, ""),
			Root:       rel,
			Deployable: false,
			Detail:     err.Error(),
		})
		return nil
	}
	*out = append(*out, Service{
		Name:       serviceName(rel, ""),
		Root:       rel,
		Kind:       string(got.Kind),
		Framework:  got.Framework,
		Runtime:    got.RuntimeOrDefault(),
		Port:       got.ListenPort(),
		Deployable: true,
		Detail:     got.Source.Reason,
	})
	return nil
}

func goServices(rel string, idx fileIndex) []Service {
	pkgs := goMainPackages(idx)
	if len(pkgs) == 0 {
		return []Service{{
			Name:       serviceName(rel, ""),
			Root:       rel,
			Kind:       string(KindGo),
			Deployable: false,
			Detail:     "Go module with no package main. Shared library, not a service.",
		}}
	}
	var out []Service
	for _, pkg := range pkgs {
		out = append(out, Service{
			Name:       serviceName(rel, pkg),
			Root:       rel,
			Package:    pkg,
			Kind:       string(KindGo),
			Runtime:    RuntimeServer,
			Port:       DefaultPort,
			Deployable: true,
			Detail:     "go build " + pkg,
		})
	}
	return out
}

func goMainPackages(idx fileIndex) []string {
	if directoryHasMain(idx.root) {
		return []string{"."}
	}
	if !idx.hasDir("cmd") {
		return nil
	}
	ents, err := os.ReadDir(filepath.Join(idx.root, "cmd"))
	if err != nil {
		return nil
	}
	var mains []string
	for _, ent := range ents {
		if !ent.IsDir() || !goCmdNameOK(ent.Name()) {
			continue
		}
		if directoryHasMain(filepath.Join(idx.root, "cmd", ent.Name())) {
			mains = append(mains, "./cmd/"+ent.Name())
		}
	}
	sort.Strings(mains)
	return mains
}

func serviceName(rel, pkg string) string {
	if strings.HasPrefix(pkg, "./cmd/") {
		return strings.TrimPrefix(pkg, "./cmd/")
	}
	if rel == "." || rel == "" {
		return "app"
	}
	return rel
}

func serviceMarker(idx fileIndex) bool {
	if idx.hasAny("Dockerfile", "docker-compose.yml", "package.json", "go.mod", "requirements.txt", "pyproject.toml", "Pipfile", "Cargo.toml", "Gemfile", "composer.json", "pom.xml", "build.gradle", "build.gradle.kts", "CMakeLists.txt", "Makefile", "Procfile", "forge.json", "index.html") {
		return true
	}
	return idx.hasExt(".csproj")
}

func skipDiscoverDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", ".next", "venv", ".venv", "__pycache__", "testdata", ".github", "docs":
		return true
	default:
		return false
	}
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// SuggestedLink is an edge Deploy All may persist as an application
// link. Source receives Target's stable Host URL in EnvKey.
type SuggestedLink struct {
	Source string `json:"source"`
	Target string `json:"target"`
	EnvKey string `json:"env_key"`
}

// ServiceRole classifies a discovered service for ordering and for
// automatic links. Unknown names stay "app" so Forge does not invent
// a frontend/backend edge.
func ServiceRole(s Service) string {
	n := strings.ToLower(s.Name)
	switch {
	case strings.Contains(n, "worker") || strings.Contains(n, "queue"):
		return "worker"
	case strings.Contains(n, "ml") || strings.Contains(n, "model"):
		return "ml"
	case strings.Contains(n, "front") || strings.Contains(n, "web") || strings.Contains(n, "client") || strings.HasPrefix(s.Framework, "vite") || s.Framework == "next" || s.Framework == "cra" || (s.Kind == "node" && s.Runtime == "static"):
		return "frontend"
	case strings.Contains(n, "api") || strings.Contains(n, "backend") || strings.Contains(n, "server") || s.Kind == "python" || s.Kind == "go":
		return "backend"
	default:
		return "app"
	}
}

// SuggestLinks returns edges only when each role appears once.
// Two backends, or two frontends, is ambiguous: the caller should ask
// the operator instead of guessing which URL to inject.
func SuggestLinks(services []Service) (links []SuggestedLink, note string) {
	var fronts, backs, mls []Service
	for _, s := range services {
		if !s.Deployable {
			continue
		}
		switch ServiceRole(s) {
		case "frontend":
			fronts = append(fronts, s)
		case "backend":
			backs = append(backs, s)
		case "ml":
			mls = append(mls, s)
		}
	}
	var notes []string
	if len(fronts) == 1 && len(backs) == 1 {
		links = append(links, SuggestedLink{
			Source: fronts[0].Name,
			Target: backs[0].Name,
			EnvKey: frontendAPIKey(fronts[0]),
		})
	} else if len(fronts) > 1 || len(backs) > 1 {
		notes = append(notes, "several frontends or backends; confirm service links before relying on them")
	}
	if len(backs) == 1 && len(mls) == 1 {
		links = append(links, SuggestedLink{
			Source: backs[0].Name,
			Target: mls[0].Name,
			EnvKey: "ML_SERVICE_URL",
		})
	} else if len(mls) > 1 {
		notes = append(notes, "several ML services; confirm which URL the backend should call")
	}
	return links, strings.Join(notes, " ")
}

func frontendAPIKey(s Service) string {
	switch {
	case s.Framework == "next":
		return "NEXT_PUBLIC_API_URL"
	case strings.HasPrefix(s.Framework, "vite"):
		return "VITE_API_URL"
	case s.Framework == "cra":
		return "REACT_APP_API_URL"
	default:
		return "API_URL"
	}
}

// DeployOrder queues dependencies before the services that call them.
// One worker still runs jobs one at a time; this order is what makes
// the API exist before the frontend is promoted.
func DeployOrder(services []Service) []Service {
	out := append([]Service(nil), services...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := roleRank(ServiceRole(out[i])), roleRank(ServiceRole(out[j]))
		if ri != rj {
			return ri < rj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// DependsOn reports whether service should wait until other is ready.
//
// A frontend waits for a backend or ML service. Two backends do not
// wait for each other, so their builds can run at the same time.
// The check uses the service name only. It does not read the repository.
func DependsOn(service, other Service) bool {
	if service.Name == "" || other.Name == "" || service.Name == other.Name {
		return false
	}
	return roleRank(ServiceRole(other)) < roleRank(ServiceRole(service))
}

func roleRank(role string) int {
	switch role {
	case "ml":
		return 0
	case "backend", "worker":
		return 1
	case "frontend":
		return 3
	default:
		return 2
	}
}
