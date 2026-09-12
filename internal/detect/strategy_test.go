package detect

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestTreeViteWithoutStartIsStatic(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{
		"name":"portfolio",
		"scripts":{"dev":"vite","build":"vite build","preview":"vite preview"},
		"dependencies":{"react":"19.0.0","react-dom":"19.0.0"},
		"devDependencies":{"vite":"7.0.0"}
	}`)
	write(t, filepath.Join(dir, "package-lock.json"), `{"lockfileVersion":3}`)
	write(t, filepath.Join(dir, "vite.config.js"), `export default { plugins: [] }\n`)
	write(t, filepath.Join(dir, "index.html"), `<div id="root"></div>`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindNode || got.Framework != "vite-react" || got.Runtime != RuntimeStatic {
		t.Fatalf("got %+v", got)
	}
	if got.OutputDir != "dist" {
		t.Fatalf("output = %q", got.OutputDir)
	}
	if !strings.Contains(got.GeneratedDockerfile, "nginx:1.27-alpine") {
		t.Fatal(got.GeneratedDockerfile)
	}
	if !strings.Contains(got.GeneratedDockerfile, "npm ci") || !strings.Contains(got.GeneratedDockerfile, "run build -- --base ./") {
		t.Fatal(got.GeneratedDockerfile)
	}
	if strings.Contains(got.GeneratedDockerfile, "npm\",\"start") || strings.Contains(got.GeneratedDockerfile, "vite preview") {
		t.Fatal("must not use npm start or vite preview")
	}
	if got.ExtraFiles[nginxForgeName] == "" || !strings.Contains(got.ExtraFiles[nginxForgeName], "/var/lib/nginx-tmp/nginx.pid") {
		t.Fatal("missing isolated nginx.conf")
	}
	if !strings.Contains(got.GeneratedDockerfile, `ENTRYPOINT ["nginx"]`) {
		t.Fatal("must bypass the stock nginx entrypoint that chowns")
	}
}

func TestTreeExpressHardcodedPort(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4.18.0"}}`)
	write(t, filepath.Join(dir, "server.js"), "const port = 5000;\napp.get('/api/customers', handler);\napp.listen(port);\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "express" || got.Runtime != RuntimeServer {
		t.Fatalf("got %+v", got)
	}
	if got.Port != 5000 || got.PortSource != "source_listen" {
		t.Fatalf("port %+v", got)
	}
	if got.HealthPath != "/api/customers" {
		t.Fatalf("health = %q", got.HealthPath)
	}
	if !strings.Contains(got.GeneratedDockerfile, "PORT=5000") {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeExpressParameterizedGETIsIgnored(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4.18.0"}}`)
	write(t, filepath.Join(dir, "server.js"), "app.get('/users/:id', handler);\napp.listen(process.env.PORT);\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.HealthPath != "" {
		t.Fatalf("parameterized GET must not become health, got %q", got.HealthPath)
	}
}

func TestTreeExpressHonorsPORT(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4.18.0"}}`)
	write(t, filepath.Join(dir, "server.js"), "const port = process.env.PORT || 5000;\napp.listen(port);\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != DefaultPort || got.PortSource != "pack" {
		t.Fatalf("PORT-aware app should keep Forge port, got %+v", got)
	}
}

func TestTreeFastifyStart(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"node server.js"},"dependencies":{"fastify":"4.0.0"}}`)
	write(t, filepath.Join(dir, "server.js"), "app.listen({ port: 8080 })\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "fastify" {
		t.Fatalf("framework = %q", got.Framework)
	}
}

func TestTreeStaticHTML(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.html"), "<h1>hi</h1>\n")
	write(t, filepath.Join(dir, "style.css"), "body{}\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindStatic || got.Runtime != RuntimeStatic {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(got.GeneratedDockerfile, "nginx") {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeIndexHTMLDoesNotBeatGo(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example\n")
	write(t, filepath.Join(dir, "index.html"), "<h1>docs</h1>\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindGo {
		t.Fatalf("kind = %q", got.Kind)
	}
}

func TestTreeFastAPIUniqueMain(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "requirements.txt"), "fastapi==0.110.0\n")
	write(t, filepath.Join(dir, "main.py"), "from fastapi import FastAPI\napp = FastAPI()\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "fastapi" || !strings.Contains(got.GeneratedDockerfile, "uvicorn") {
		t.Fatalf("%+v\n%s", got, got.GeneratedDockerfile)
	}
	if strings.Contains(got.GeneratedDockerfile, "flask run") {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeFlaskUniqueApp(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "requirements.txt"), "flask\n")
	write(t, filepath.Join(dir, "app.py"), "from flask import Flask\napp = Flask(__name__)\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "flask" || !strings.Contains(got.GeneratedDockerfile, "gunicorn") {
		t.Fatalf("%+v\n%s", got, got.GeneratedDockerfile)
	}
	if strings.Contains(got.GeneratedDockerfile, "flask run") {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeForgeJSONHealthAndOutput(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{
		"scripts":{"build":"vite build"},
		"devDependencies":{"vite":"7.0.0"}
	}`)
	write(t, filepath.Join(dir, "forge.json"), `{"port":4173,"health":"/ready","runtime":"static","output":"build"}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Port != 4173 || got.HealthPath != "/ready" || got.OutputDir != "build" || got.Runtime != RuntimeStatic {
		t.Fatalf("got %+v", got)
	}
}

func TestTreeForgeJSONInvalidHealth(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"node x.js"}}`)
	write(t, filepath.Join(dir, "forge.json"), `{"health":"http://169.254.169.254/"}`)
	_, err := Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "health") {
		t.Fatalf("err = %v", err)
	}
}

func TestTreeLocalhostBindHint(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"node server.js"},"dependencies":{"express":"4.18.0"}}`)
	write(t, filepath.Join(dir, "server.js"), "app.listen(3000, '127.0.0.1')\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.BindHint != "127.0.0.1" || got.Port != 3000 {
		t.Fatalf("got %+v", got)
	}
}

func TestTreeAssetsDoNotCreateStaticSite(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "logo.png"), "x")
	write(t, filepath.Join(dir, "README.md"), "docs\n")
	_, err := Tree(dir)
	if err == nil {
		t.Fatal("images are not a deployable app")
	}
}

func TestNodeDevStartIsNotProduction(t *testing.T) {
	if !isDevStartScript("vite") || !isDevStartScript("next dev") || !isDevStartScript("vite preview") {
		t.Fatal("dev scripts")
	}
	if isDevStartScript("node server.js") || isDevStartScript("next start") {
		t.Fatal("prod scripts")
	}
}
