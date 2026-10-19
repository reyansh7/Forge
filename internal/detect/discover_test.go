package detect

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverMonorepoAndGoCommands(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example\n")
	write(t, filepath.Join(dir, "cmd", "api", "main.go"), "package main\nfunc main() {}\n")
	write(t, filepath.Join(dir, "cmd", "worker", "main.go"), "package main\nfunc main() {}\n")
	write(t, filepath.Join(dir, "frontend", "package.json"), `{"scripts":{"build":"vite build","dev":"vite"},"dependencies":{"react":"18.0.0","react-dom":"18.0.0"},"devDependencies":{"vite":"5.0.0"}}`)
	write(t, filepath.Join(dir, "frontend", "index.html"), "<div id=\"root\"></div>\n")
	write(t, filepath.Join(dir, "backend", "requirements.txt"), "fastapi==0.110.0\n")
	write(t, filepath.Join(dir, "backend", "main.py"), "from fastapi import FastAPI\napp = FastAPI()\n")
	write(t, filepath.Join(dir, "pkg", "go.mod"), "module example.com/lib\n")
	write(t, filepath.Join(dir, "pkg", "lib.go"), "package lib\n")
	write(t, filepath.Join(dir, "node_modules", "leftpad", "package.json"), `{"name":"leftpad"}`)
	write(t, filepath.Join(dir, "docs", "index.html"), "<p>docs</p>\n")

	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Service{}
	for _, s := range got {
		byName[s.Name] = s
	}
	for _, name := range []string{"api", "worker", "frontend", "backend"} {
		s, ok := byName[name]
		if !ok || !s.Deployable {
			t.Fatalf("missing deployable %s in %+v", name, got)
		}
	}
	if byName["api"].Package != "./cmd/api" || byName["worker"].Package != "./cmd/worker" {
		t.Fatalf("go packages %+v %+v", byName["api"], byName["worker"])
	}
	if byName["frontend"].Kind != string(KindNode) || byName["frontend"].Root != "frontend" {
		t.Fatalf("frontend %+v", byName["frontend"])
	}
	if byName["backend"].Kind != string(KindPython) || byName["backend"].Framework != "fastapi" {
		t.Fatalf("backend %+v", byName["backend"])
	}
	if s, ok := byName["pkg"]; !ok || s.Deployable {
		t.Fatalf("library should be listed and not deployable: %+v", s)
	}
	if _, ok := byName["leftpad"]; ok {
		t.Fatal("node_modules must not be a service")
	}
}

func TestDiscoverNestedServiceDirs(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "apps", "web", "package.json"), `{"scripts":{"build":"vite build"},"dependencies":{"react":"18.0.0","react-dom":"18.0.0"},"devDependencies":{"vite":"5.0.0"}}`)
	write(t, filepath.Join(dir, "apps", "web", "index.html"), "<div id=\"root\"></div>\n")
	write(t, filepath.Join(dir, "apps", "web", "src", "package.json"), `{"name":"not-a-service"}`)
	write(t, filepath.Join(dir, "services", "api", "go.mod"), "module example.com/api\n")
	write(t, filepath.Join(dir, "services", "api", "cmd", "worker", "main.go"), "package main\nfunc main() {}\n")
	write(t, filepath.Join(dir, "services", "api", "cmd", "api", "main.go"), "package main\nfunc main() {}\n")
	write(t, filepath.Join(dir, "frontend", "package.json"), `{"scripts":{"build":"vite build","start":"node server.js"},"dependencies":{"express":"4.0.0"}}`)
	write(t, filepath.Join(dir, "frontend", "server.js"), "const express = require('express')\n")
	write(t, filepath.Join(dir, "frontend", "widget", "package.json"), `{"name":"widget"}`)
	write(t, filepath.Join(dir, "group", "team", "web", "package.json"), `{"name":"too-deep"}`)
	write(t, filepath.Join(dir, "apps", "node_modules", "leftpad", "package.json"), `{"name":"leftpad"}`)

	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Service{}
	for _, s := range got {
		if _, ok := byName[s.Name]; ok {
			t.Fatalf("duplicate service name %s", s.Name)
		}
		byName[s.Name] = s
	}
	web := byName["apps/web"]
	if !web.Deployable || web.Root != "apps/web" || web.Kind != string(KindNode) {
		t.Fatalf("web %+v", web)
	}
	if _, ok := byName["apps/web/src"]; ok {
		t.Fatal("source directory inside a service must not be its own service")
	}
	api := byName["services/api/api"]
	worker := byName["services/api/worker"]
	if !api.Deployable || api.Root != "services/api" || api.Package != "./cmd/api" {
		t.Fatalf("api %+v", api)
	}
	if !worker.Deployable || worker.Root != "services/api" || worker.Package != "./cmd/worker" {
		t.Fatalf("worker %+v", worker)
	}
	if _, ok := byName["frontend/widget"]; ok {
		t.Fatal("a matched app must not also deploy a nested manifest")
	}
	if _, ok := byName["group/team/web"]; ok || byName["group/team"].Deployable {
		t.Fatal("a third directory level must not be discovered")
	}
	if _, ok := byName["leftpad"]; ok || byName["apps/node_modules/leftpad"].Deployable {
		t.Fatal("node_modules must not be a service")
	}
}

func TestSuggestLinksAndDeployOrder(t *testing.T) {
	services := []Service{
		{Name: "frontend", Kind: string(KindNode), Framework: "vite-react", Runtime: "static", Deployable: true},
		{Name: "backend", Kind: string(KindPython), Framework: "fastapi", Deployable: true},
		{Name: "ml-model", Kind: string(KindPython), Deployable: true},
		{Name: "worker", Kind: string(KindGo), Package: "./cmd/worker", Deployable: true},
	}
	links, note := SuggestLinks(services)
	if note != "" {
		t.Fatal(note)
	}
	if len(links) != 3 || links[0].EnvKey != "VITE_API_URL" || links[1].EnvKey != "CORS_ORIGIN" || !links[1].Origin || links[2].EnvKey != "ML_SERVICE_URL" {
		t.Fatalf("%+v", links)
	}
	ordered := DeployOrder(services)
	if ordered[0].Name != "ml-model" || ordered[len(ordered)-1].Name != "frontend" {
		t.Fatalf("%+v", ordered)
	}

	ambiguous := append(services, Service{Name: "api", Kind: string(KindGo), Deployable: true})
	links, note = SuggestLinks(ambiguous)
	if note == "" || len(links) != 0 {
		t.Fatalf("links %+v note %q", links, note)
	}
}

func TestDiscoverDockerfileWinsAtRoot(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "Dockerfile"), "FROM scratch\nEXPOSE 8080\n")
	write(t, filepath.Join(dir, "package.json"), `{"name":"x"}`)
	got, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != string(KindDockerfile) || !got[0].Deployable {
		t.Fatalf("%+v", got)
	}
}

func TestResolveLinksRejectsPublicSecret(t *testing.T) {
	_, _, err := ResolveLinks([]Service{
		{Name: "backend", Env: []ServiceEnv{{Key: "DATABASE_URL", Service: "ml-model", Visibility: "public"}}},
		{Name: "ml-model"},
	})
	if err == nil || !strings.Contains(err.Error(), "browser") {
		t.Fatalf("got %v", err)
	}
}
