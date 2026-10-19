package detect

import (
	"os"
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
	write(t, filepath.Join(dir, "main.go"), "package main\nfunc main() {}\n")
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

func TestTreeNodeWithoutStartFailsBeforeBuild(t *testing.T) {
	dir := t.TempDir()
	// Same shape as Resume Analyzer's root manifest: dependencies, no
	// start script, no framework. main must not become the command.
	write(t, filepath.Join(dir, "package.json"), `{
		"main": "server.js",
		"dependencies": {"mongodb": "^7.1.1", "sharp": "^0.34.5"}
	}`)
	_, err := Tree(dir)
	if err == nil {
		t.Fatal("expected a detecting error")
	}
	msg := err.Error()
	for _, want := range []string{"Node.js", "start script", "forge.json", "Procfile"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
	if strings.Contains(msg, "server.js") {
		t.Fatalf("invented a command from main: %s", msg)
	}
}

func TestTreeNodeWhitespaceStartIsMissing(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"   "}}`)
	_, err := Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "start script") {
		t.Fatal(err)
	}
}

func TestTreeNodeDevStartIsRejected(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"vite"}}`)
	_, err := Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "start script") {
		t.Fatal(err)
	}
}

func TestTreeNodeStartScriptUsesNpmStart(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"node server.js"}}`)
	write(t, filepath.Join(dir, "package-lock.json"), `{"lockfileVersion":3}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindNode || got.Runtime != RuntimeServer {
		t.Fatalf("got %+v", got)
	}
	if !strings.Contains(got.GeneratedDockerfile, "npm ci") || !strings.Contains(got.GeneratedDockerfile, `CMD ["npm","start"]`) {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeNodeYarnStart(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"scripts":{"start":"node server.js"}}`)
	write(t, filepath.Join(dir, "yarn.lock"), "# yarn\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.GeneratedDockerfile, `CMD ["yarn","start"]`) {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeNodeForgeStartWithoutScript(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"dependencies":{"mongodb":"7.0.0"}}`)
	write(t, filepath.Join(dir, "forge.json"), `{"start":["node","server.js"]}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.GeneratedDockerfile, `CMD ["node","server.js"]`) {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeNodeProcfileWithoutScript(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"dependencies":{"mongodb":"7.0.0"}}`)
	write(t, filepath.Join(dir, "Procfile"), "web: node server.js\n")
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.GeneratedDockerfile, `CMD ["node","server.js"]`) {
		t.Fatal(got.GeneratedDockerfile)
	}
}

func TestTreeNodeStaticRuntimeOverride(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"dependencies":{"mongodb":"7.0.0"}}`)
	write(t, filepath.Join(dir, "forge.json"), `{"runtime":"static","output":"dist"}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Runtime != RuntimeStatic || !strings.Contains(got.GeneratedDockerfile, "nginx:1.27-alpine") {
		t.Fatalf("runtime=%q dockerfile=%s", got.Runtime, got.GeneratedDockerfile)
	}
}

func TestTreeNestBuildWithoutStart(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{
		"dependencies": {"@nestjs/core": "10.0.0"},
		"scripts": {"build": "nest build"}
	}`)
	got, err := Tree(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Framework != "nest" || !strings.Contains(got.GeneratedDockerfile, `CMD ["node","dist/main.js"]`) {
		t.Fatalf("framework=%q dockerfile=%s", got.Framework, got.GeneratedDockerfile)
	}
}

func TestDirectoryHasMainDoesNotReadPastPrefix(t *testing.T) {
	dir := t.TempDir()
	early := []byte("package main\n")
	early = append(early, bytesRepeat('x', 1024*1024)...)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), early, 0o600); err != nil {
		t.Fatal(err)
	}
	if !directoryHasMain(dir) {
		t.Fatal("package main in the prefix was missed")
	}
	lateDir := t.TempDir()
	late := append(bytesRepeat('y', goPackageScanBytes+10), []byte("\npackage main\n")...)
	if err := os.WriteFile(filepath.Join(lateDir, "late.go"), late, 0o600); err != nil {
		t.Fatal(err)
	}
	if directoryHasMain(lateDir) {
		t.Fatal("package clause past the scan cap was read")
	}
}

func bytesRepeat(b byte, n int) []byte {
	return []byte(strings.Repeat(string(b), n))
}

func TestTreeExpressWithoutStartFails(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "package.json"), `{"dependencies":{"express":"4.18.0"},"scripts":{"build":"tsc"}}`)
	_, err := Tree(dir)
	if err == nil || !strings.Contains(err.Error(), "express") || !strings.Contains(err.Error(), "start script") {
		t.Fatal(err)
	}
}
