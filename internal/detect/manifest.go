package detect

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseManifest reads forge.yaml at the repository root.
//
// The file is the service list. A missing name, a path that escapes
// the repository, an unknown dependency, or a cycle is returned to the
// caller. Forge does not fill those in from directory names.
func parseManifest(repo string) ([]Service, error) {
	raw, err := os.ReadFile(filepath.Join(repo, "forge.yaml"))
	if err != nil {
		return nil, fmt.Errorf("read forge.yaml: %w", err)
	}
	if len(raw) > 64<<10 {
		return nil, fmt.Errorf("forge.yaml is larger than 64 KiB")
	}
	var file manifestFile
	if err := yaml.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("forge.yaml: %w", err)
	}
	if len(file.Services) == 0 {
		return nil, fmt.Errorf("forge.yaml has no services")
	}
	if len(file.Services) > 16 {
		return nil, fmt.Errorf("forge.yaml lists more than 16 services")
	}
	seen := map[string]bool{}
	var out []Service
	for _, item := range file.Services {
		svc, err := item.service()
		if err != nil {
			return nil, err
		}
		if seen[svc.Name] {
			return nil, fmt.Errorf("forge.yaml repeats service %s", svc.Name)
		}
		seen[svc.Name] = true
		dir := repo
		if svc.Root != "." {
			dir = filepath.Join(repo, filepath.FromSlash(svc.Root))
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("forge.yaml service %s root %s is not a directory", svc.Name, svc.Root)
		}
		if svc.Framework == "" || svc.Kind == "" {
			filled, ferr := fillFromTree(dir, svc)
			if ferr != nil {
				// An explicit kind is enough to queue the service.
				// An empty directory has nothing for the detector to
				// read, and that must not hide a cycle or a bad path.
				if svc.Kind == "" {
					return nil, ferr
				}
			} else {
				svc = filled
			}
		}
		out = append(out, svc)
	}
	for _, svc := range out {
		for _, dep := range svc.DependsOn {
			if !seen[dep] {
				return nil, fmt.Errorf("forge.yaml service %s depends on unknown service %s", svc.Name, dep)
			}
			if dep == svc.Name {
				return nil, fmt.Errorf("forge.yaml service %s depends on itself", svc.Name)
			}
		}
		for _, ref := range svc.Env {
			if !seen[ref.Service] {
				return nil, fmt.Errorf("forge.yaml service %s env %s points at unknown service %s", svc.Name, ref.Key, ref.Service)
			}
		}
		if svc.Public == nil && ServiceRole(svc) == "app" {
			return nil, fmt.Errorf("forge.yaml service %s must set public: true or false", svc.Name)
		}
	}
	if _, err := OrderServices(out); err != nil {
		return nil, err
	}
	if _, _, err := ResolveLinks(out); err != nil {
		return nil, err
	}
	return out, nil
}

func fillFromTree(dir string, svc Service) (Service, error) {
	idx, err := indexRoot(dir)
	if err != nil {
		return Service{}, fmt.Errorf("forge.yaml service %s: %w", svc.Name, err)
	}
	res, err := analyzeIndex(idx)
	if err != nil {
		return Service{}, fmt.Errorf("forge.yaml service %s: %w", svc.Name, err)
	}
	if svc.Kind == "" {
		svc.Kind = string(res.Kind)
	}
	if svc.Framework == "" {
		svc.Framework = res.Framework
	}
	if svc.Runtime == "" {
		svc.Runtime = res.Runtime
	}
	if svc.Port == 0 {
		svc.Port = res.Port
	}
	return svc, nil
}

type manifestFile struct {
	Services []manifestService `yaml:"services"`
}

type manifestService struct {
	Name       string                 `yaml:"name"`
	Root       string                 `yaml:"root"`
	Dockerfile string                 `yaml:"dockerfile"`
	Context    string                 `yaml:"context"`
	Port       int                    `yaml:"port"`
	Health     string                 `yaml:"health"`
	Public     *bool                  `yaml:"public"`
	DependsOn  []string               `yaml:"depends_on"`
	Framework  string                 `yaml:"framework"`
	Kind       string                 `yaml:"kind"`
	Runtime    string                 `yaml:"runtime"`
	Env        map[string]manifestEnv `yaml:"env"`
	Volumes    []manifestVolume       `yaml:"volumes"`
}

type manifestEnv struct {
	Service    string `yaml:"service"`
	Visibility string `yaml:"visibility"`
	Origin     bool   `yaml:"origin"`
}

type manifestVolume struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

func (m manifestService) service() (Service, error) {
	name := strings.TrimSpace(m.Name)
	if name == "" || strings.ContainsAny(name, " /\t") {
		return Service{}, fmt.Errorf("forge.yaml service name must be a single token")
	}
	root := strings.Trim(strings.TrimSpace(m.Root), "/")
	if root == "" {
		root = "."
	}
	if filepath.IsAbs(root) || strings.Contains(root, "\\") || strings.Contains(root, "..") {
		return Service{}, fmt.Errorf("forge.yaml service %s root escapes the repository", name)
	}
	docker := strings.TrimSpace(m.Dockerfile)
	if docker != "" && docker != "Dockerfile" {
		return Service{}, fmt.Errorf("forge.yaml service %s dockerfile must be named Dockerfile in its root", name)
	}
	ctx := strings.Trim(strings.TrimSpace(m.Context), "/")
	if ctx == "" {
		ctx = root
	}
	if ctx != root {
		return Service{}, fmt.Errorf("forge.yaml service %s context must match its root", name)
	}
	if m.Port < 0 || m.Port > 65535 {
		return Service{}, fmt.Errorf("forge.yaml service %s port is outside 1-65535", name)
	}
	health := strings.TrimSpace(m.Health)
	if health == "" {
		health = "/"
	}
	if !strings.HasPrefix(health, "/") || strings.Contains(health, "://") || strings.Contains(health, "..") {
		return Service{}, fmt.Errorf("forge.yaml service %s health must be a path starting with /", name)
	}
	var env []ServiceEnv
	keys := make([]string, 0, len(m.Env))
	for key := range m.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		ref := m.Env[key]
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t\r\n=") {
			return Service{}, fmt.Errorf("forge.yaml service %s has an invalid env key", name)
		}
		vis := strings.TrimSpace(ref.Visibility)
		if vis != "public" && vis != "internal" {
			return Service{}, fmt.Errorf("forge.yaml service %s env %s visibility must be public or internal", name, key)
		}
		target := strings.TrimSpace(ref.Service)
		if target == "" {
			return Service{}, fmt.Errorf("forge.yaml service %s env %s needs a service", name, key)
		}
		env = append(env, ServiceEnv{Key: key, Service: target, Visibility: vis, Origin: ref.Origin})
	}
	var vols []ServiceVol
	for _, vol := range m.Volumes {
		vname := strings.TrimSpace(vol.Name)
		path := strings.TrimSpace(vol.Path)
		if vname == "" || strings.Contains(vname, "/") || strings.Contains(path, "..") || !strings.HasPrefix(path, "/") {
			return Service{}, fmt.Errorf("forge.yaml service %s volume must be a name and an absolute container path", name)
		}
		vols = append(vols, ServiceVol{Name: vname, Path: path})
	}
	return Service{
		Name:       name,
		Root:       root,
		Kind:       strings.TrimSpace(m.Kind),
		Framework:  strings.TrimSpace(m.Framework),
		Runtime:    strings.TrimSpace(m.Runtime),
		Port:       m.Port,
		Health:     health,
		Public:     m.Public,
		DependsOn:  append([]string(nil), m.DependsOn...),
		Dockerfile: docker,
		Context:    ctx,
		Env:        env,
		Volumes:    vols,
		Deployable: true,
		Detail:     "forge.yaml",
	}, nil
}
