package detect

import (
	"fmt"
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
	Name      string `json:"name"`
	Root      string `json:"root"`
	Package   string `json:"package,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Framework string `json:"framework,omitempty"`
	Runtime   string `json:"runtime,omitempty"`
	Port      int    `json:"port,omitempty"`
	Health    string `json:"health,omitempty"`
	// Public is nil when forge.yaml did not set it. The role default
	// then decides routing. A non-nil value is explicit.
	Public     *bool        `json:"public,omitempty"`
	DependsOn  []string     `json:"depends_on,omitempty"`
	Dockerfile string       `json:"dockerfile,omitempty"`
	Context    string       `json:"context,omitempty"`
	Env        []ServiceEnv `json:"env,omitempty"`
	Volumes    []ServiceVol `json:"volumes,omitempty"`
	Deployable bool         `json:"deployable"`
	Detail     string       `json:"detail"`
}

// ServiceEnv is a generated variable. It is not an operator secret.
// Visibility public is a proxy URL. internal is the project-bridge
// address. Origin drops the path so CORS_ORIGIN is a browser origin.
type ServiceEnv struct {
	Key        string `json:"key"`
	Service    string `json:"service"`
	Visibility string `json:"visibility"`
	Origin     bool   `json:"origin,omitempty"`
}

// ServiceVol is a Forge named volume. The path is inside the container.
type ServiceVol struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Discover lists services in a repository without running user code.
//
// It classifies the root and each child directory. A child that is not
// itself a service is opened once more, so apps/web and services/api
// are found. A directory that already matched is not opened again, so
// frontend/src and a Node app's own packages stay part of that app.
// Deeper than that (group/team/web) is not discovered. node_modules,
// vendor, VCS, and virtualenv folders are not services. A Go module
// with cmd/api and cmd/worker becomes two services. A module with no
// main package is reported as a library, not deployed.
func Discover(root string) ([]Service, error) {
	if fileExists(filepath.Join(root, "forge.yaml")) {
		// The manifest is the plan. Heuristic discovery must not add
		// or drop a service the file did not name.
		return parseManifest(root)
	}
	// A production Compose project with no root Dockerfile is not one
	// application. Child Dockerfiles are not deployed under directory
	// names that disagree with the Compose DNS names.
	if !fileExists(filepath.Join(root, "Dockerfile")) {
		if msg := composeProjectAdvice(root); msg != "" {
			return []Service{{
				Name:       "app",
				Root:       ".",
				Deployable: false,
				Detail:     msg,
			}}, nil
		}
	}
	var out []Service
	if err := discoverAt(root, ".", &out); err != nil {
		return nil, err
	}
	if err := discoverNested(root, ".", 0, &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Root == out[j].Root {
			return out[i].Package < out[j].Package
		}
		return out[i].Root < out[j].Root
	})
	return out, nil
}

// discoverNested lists child directories of rel. depth 0 is the
// repository root. When a child adds no service, depth 1 looks inside
// it. depth stops there: the walk is not a full recursive scan.
func discoverNested(repo, rel string, depth int, out *[]Service) error {
	if depth > 1 {
		return nil
	}
	dir := repo
	if rel != "." {
		dir = filepath.Join(repo, filepath.FromSlash(rel))
	}
	names, err := listDiscoverDirs(dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		// cmd/ under a root go.mod is that module's programs, already
		// emitted by discoverAt on ".".
		if rel == "." && name == "cmd" && fileExists(filepath.Join(repo, "go.mod")) {
			continue
		}
		child := name
		if rel != "." {
			child = rel + "/" + name
		}
		before := len(*out)
		if err := discoverAt(repo, child, out); err != nil {
			return err
		}
		if len(*out) == before && depth < 1 {
			if err := discoverNested(repo, child, depth+1, out); err != nil {
				return err
			}
		}
	}
	return nil
}

func listDiscoverDirs(dir string) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, ent := range ents {
		// IsDir follows the entry type. A symlink is not a directory
		// here, so a link cannot pull another tree into the service list.
		if !ent.IsDir() || ent.Type()&os.ModeSymlink != 0 || skipDiscoverDir(ent.Name()) {
			continue
		}
		names = append(names, ent.Name())
	}
	sort.Strings(names)
	return names, nil
}

func discoverAt(repo, rel string, out *[]Service) error {
	dir := repo
	if rel != "." {
		dir = filepath.Join(repo, filepath.FromSlash(rel))
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
		leaf := strings.TrimPrefix(pkg, "./cmd/")
		// A root module's cmd/api stays "api". The same command inside
		// services/api must keep that directory in the name or two
		// modules would both try to create an application named worker.
		if rel == "." || rel == "" {
			return leaf
		}
		return rel + "/" + leaf
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
	// Origin is true when the value must be scheme://host[:port] with
	// no path. CORS checks fail if a trailing slash or route is included.
	Origin bool `json:"origin,omitempty"`
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

// ResolveLinks uses forge.yaml env refs when any service declares them.
// Otherwise it uses SuggestLinks. Mixing guessed and declared edges
// would inject a URL the manifest did not ask for.
func ResolveLinks(services []Service) (links []SuggestedLink, note string, err error) {
	var explicit []SuggestedLink
	declared := false
	names := map[string]bool{}
	for _, svc := range services {
		if svc.Name != "" {
			names[svc.Name] = true
		}
		if len(svc.Env) > 0 {
			declared = true
		}
		for _, ref := range svc.Env {
			if ref.Service == "" || ref.Key == "" {
				return nil, "", fmt.Errorf("service %s has an env reference without a key and target", svc.Name)
			}
			if !names[ref.Service] && !serviceNamed(services, ref.Service) {
				return nil, "", fmt.Errorf("service %s env %s points at unknown service %s", svc.Name, ref.Key, ref.Service)
			}
			if ref.Visibility != "public" && ref.Visibility != "internal" {
				return nil, "", fmt.Errorf("service %s env %s visibility must be public or internal", svc.Name, ref.Key)
			}
			if ref.Origin && ref.Key != "CORS_ORIGIN" {
				return nil, "", fmt.Errorf("service %s env %s cannot be an origin; only CORS_ORIGIN drops the path", svc.Name, ref.Key)
			}
			if ref.Visibility == "public" && !isPublicEnvKey(ref.Key) && ref.Key != "CORS_ORIGIN" {
				return nil, "", fmt.Errorf("service %s env %s is public but is not a browser build key", svc.Name, ref.Key)
			}
			explicit = append(explicit, SuggestedLink{
				Source: svc.Name,
				Target: ref.Service,
				EnvKey: ref.Key,
				Origin: ref.Origin || ref.Key == "CORS_ORIGIN",
			})
		}
	}
	if declared {
		return explicit, "", nil
	}
	links, note = SuggestLinks(services)
	return links, note, nil
}

func serviceNamed(services []Service, name string) bool {
	for _, svc := range services {
		if svc.Name == name {
			return true
		}
	}
	return false
}

func isPublicEnvKey(key string) bool {
	return strings.HasPrefix(key, "NEXT_PUBLIC_") || strings.HasPrefix(key, "VITE_") || strings.HasPrefix(key, "REACT_APP_")
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
		links = append(links, SuggestedLink{
			Source: backs[0].Name,
			Target: fronts[0].Name,
			EnvKey: "CORS_ORIGIN",
			Origin: true,
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
		return "NEXT_PUBLIC_BACKEND_URL"
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
	if len(service.DependsOn) > 0 {
		for _, name := range service.DependsOn {
			if name == other.Name {
				return true
			}
		}
		return false
	}
	return roleRank(ServiceRole(other)) < roleRank(ServiceRole(service))
}

// OrderServices is a stable topological order. An explicit depends_on
// list replaces the role ranking for that service. A cycle is an error
// so Forge does not pick an order.
func OrderServices(services []Service) ([]Service, error) {
	if err := dependencyCycle(services); err != nil {
		return nil, err
	}
	pending := append([]Service(nil), services...)
	var out []Service
	for len(pending) > 0 {
		ready := -1
		for i, svc := range pending {
			if waiting(svc, pending) {
				continue
			}
			if ready < 0 || lessService(svc, pending[ready]) {
				ready = i
			}
		}
		if ready < 0 {
			return nil, fmt.Errorf("service dependencies cycle; Forge will not guess an order")
		}
		out = append(out, pending[ready])
		pending = append(pending[:ready], pending[ready+1:]...)
	}
	return out, nil
}

func waiting(svc Service, pending []Service) bool {
	for _, other := range pending {
		if other.Name == svc.Name {
			continue
		}
		if DependsOn(svc, other) {
			return true
		}
	}
	return false
}

func lessService(a, b Service) bool {
	ra, rb := roleRank(ServiceRole(a)), roleRank(ServiceRole(b))
	if ra != rb {
		return ra < rb
	}
	return a.Name < b.Name
}

func dependencyCycle(services []Service) error {
	byName := map[string]Service{}
	for _, svc := range services {
		byName[svc.Name] = svc
	}
	state := map[string]int{}
	var walk func(string, []string) error
	walk = func(name string, stack []string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("service dependency cycle: %s", strings.Join(append(stack, name), " -> "))
		case 2:
			return nil
		}
		state[name] = 1
		svc := byName[name]
		var next []string
		if len(svc.DependsOn) > 0 {
			next = svc.DependsOn
		} else {
			for _, other := range services {
				if DependsOn(svc, other) {
					next = append(next, other.Name)
				}
			}
		}
		for _, dep := range next {
			if _, ok := byName[dep]; !ok {
				continue
			}
			if err := walk(dep, append(stack, name)); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	names := make([]string, 0, len(services))
	for _, svc := range services {
		names = append(names, svc.Name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := walk(name, nil); err != nil {
			return err
		}
	}
	return nil
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
